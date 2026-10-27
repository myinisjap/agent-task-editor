package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// Dispatcher sweeps the database on an interval, picks up tasks that are
// in agent-triggerable labels, and submits them to the Pool.
type Dispatcher struct {
	pool      *Pool
	db        *sql.DB
	q         *gen.Queries
	engine    *workflow.Engine
	interval  time.Duration
	uploadDir string
	// ProviderFactory builds a Provider for a given AgentConfig.
	ProviderFactory func(cfg AgentConfig) Provider
	// RateLimits is the shared rate-limit registry (optional — no-op when nil).
	RateLimits *RateLimitRegistry
	// ProviderPauses is the provider circuit breaker (optional — no-op when
	// nil). Sweep dispatch skips agent configs whose provider kind is paused.
	ProviderPauses *ProviderPauseRegistry
	// Subtasks coordinates child→parent merge-back (optional — nil disables the
	// subtask branching model). Used here to branch a child off its parent's
	// branch and to inject merge-conflict context into a parent's run.
	Subtasks *SubtaskCoordinator
	// Publisher broadcasts WS events (optional — no-op when nil). Used by the
	// cost-budget guard (see checkCostBudget) to publish task.needs_human
	// when a sweep-dispatch is skipped for budget-exhaustion, mirroring how
	// Pool.handleTransientFailure publishes the same event on escalation.
	Publisher Publisher
	// MaxDailyCostUSD/MaxMonthlyCostUSD mirror config.Config's global spend
	// ceiling settings (see checkGlobalCostBudget). 0 means unlimited for
	// that period; set once at startup from config, never mutated after.
	MaxDailyCostUSD   float64
	MaxMonthlyCostUSD float64
	// lastSweep records (as UnixNano) the time the dispatch loop last began
	// a sweep tick. Read via LastSweep by the /readyz readiness probe to
	// detect a wedged dispatch loop (e.g. a hung git op inside a sweep).
	// Stored atomically since Run's ticker goroutine writes it while an
	// HTTP handler goroutine reads it concurrently.
	lastSweep atomic.Int64
	// sweeps counts the number of sweep ticks that have fully completed.
	// Test-only support for deterministic synchronization (see SweepCount):
	// tests can wait for N additional sweeps instead of sleeping a fixed
	// wall-clock duration and hoping the dispatcher got enough ticks in.
	sweeps atomic.Int64
	// globalCostTripped records the current global-spend-ceiling state (see
	// checkGlobalCostBudget), read by GlobalCostStatus for the health
	// endpoints and written under globalCostMu each sweep. A separate mutex
	// (rather than atomics) because the struct carries multiple fields that
	// must be read/written together as one consistent snapshot.
	globalCostMu     sync.Mutex
	globalCostState  GlobalCostStatus
	globalCostAlerts bool // whether the one-shot trip alert has already fired for the current trip
}

// NewDispatcher creates a Dispatcher with a 5-second sweep interval.
func NewDispatcher(db *sql.DB, pool *Pool, engine *workflow.Engine, factory func(AgentConfig) Provider) *Dispatcher {
	return &Dispatcher{
		pool:            pool,
		db:              db,
		q:               gen.New(db),
		engine:          engine,
		interval:        5 * time.Second,
		ProviderFactory: factory,
	}
}

// SetUploadDir configures the directory where task attachment images are stored.
func (d *Dispatcher) SetUploadDir(dir string) {
	d.uploadDir = dir
}

// Run sweeps on interval until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	// Record an initial heartbeat before the loop starts so /readyz doesn't
	// report "never swept" during the up-to-interval window before the first
	// tick (e.g. right after the backend starts).
	d.lastSweep.Store(time.Now().UnixNano())

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Record the heartbeat at the start of the tick, before sweep
			// runs, so a sweep that hangs mid-execution causes the
			// heartbeat to go stale — that's the "wedged loop" /readyz is
			// meant to detect. Recording after sweep() returns would hide
			// a hang for as long as it lasts.
			d.lastSweep.Store(time.Now().UnixNano())
			d.sweep(ctx)
			d.sweeps.Add(1)
		}
	}
}

// LastSweep returns the time the dispatch loop last began a sweep tick.
// Used by the /readyz readiness probe to detect a wedged dispatch loop.
// Returns the zero Time if Run has never been started.
func (d *Dispatcher) LastSweep() time.Time {
	ns := d.lastSweep.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// SweepCount returns the number of sweep ticks that have fully completed.
// Test-only support for deterministic synchronization; see the sweeps field.
func (d *Dispatcher) SweepCount() int64 {
	return d.sweeps.Load()
}

func (d *Dispatcher) sweep(ctx context.Context) {
	// Refresh the global spend-ceiling status every sweep, even if there are
	// no pending tasks — /health and /healthz surface this live, so it must
	// stay current whether or not there's anything to dispatch right now.
	// If the cap is tripped, dispatch is halted globally for the rest of
	// this sweep: running work is left to finish, only *starting* new work
	// stops (see checkGlobalCostBudget's doc comment for the reasoning).
	if d.refreshGlobalCostStatus(ctx) {
		return
	}

	// A burst of memory-guard cancels pauses *starting* new work for a short
	// while. Like the cost ceiling, this gates only the sweep; human-reply
	// dispatch (DispatchReply) is never gated.
	if until, active := d.pool.MemoryPressurePause(); active {
		metrics.DispatchMemoryPauseActive.Set(1)
		slog.Debug("dispatcher: new dispatch paused after a burst of low-memory cancels", "component", "dispatcher", "paused_until", until)
		return
	}
	metrics.DispatchMemoryPauseActive.Set(0)

	tasks, err := d.q.ListAgentPickupTasks(ctx)
	if err != nil {
		slog.Error("dispatcher sweep failed", "component", "dispatcher", "err", err)
		return
	}
	slog.Debug("dispatcher sweep", "component", "dispatcher", "pending_tasks", len(tasks))
	metrics.DispatchEligibleTasks.Set(float64(len(tasks)))
	if len(tasks) == 0 {
		return
	}

	// Fetch active configs once per sweep, not once per task.
	configs, err := d.q.ListAgentConfigs(ctx)
	if err != nil {
		slog.Error("dispatcher: list active agent configs", "component", "dispatcher", "err", err)
		return
	}
	slog.Debug("dispatcher sweep: active configs", "component", "dispatcher", "config_count", len(configs))

	// Usage pools (provider accounts) per provider config, for rate-limit
	// checks. A load failure degrades to each config being its own pool.
	pools, err := loadUsagePools(ctx, d.q)
	if err != nil {
		slog.Warn("dispatcher: list provider configs for usage pools", "component", "dispatcher", "err", err)
	}

	// Per-repo in-flight run counts, refreshed once per sweep and decremented
	// in-process as this sweep dispatches tasks (see repoInUse below) so a
	// repo at its configured limit is skipped for the rest of this sweep too,
	// not just re-evaluated on the next tick.
	inUse, err := d.repoInUseCounts(ctx)
	if err != nil {
		slog.Error("dispatcher: count active runs by repo", "component", "dispatcher", "err", err)
		return
	}

	for _, t := range tasks {
		d.dispatch(ctx, t, configs, pools, inUse)
	}
}

// repoInUseCounts returns the current number of pending/running agent runs
// per repo (see CountActiveRunsByRepo), keyed by repo_id. Tasks locked on a
// waiting_human run are deliberately excluded: they hold no worker.
func (d *Dispatcher) repoInUseCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := d.q.CountActiveRunsByRepo(ctx)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.RepoID] = r.InUse
	}
	return counts, nil
}

// repoAtLimit reports whether repoID has no free slot under its effective
// concurrency limit: repos.max_concurrent_runs when set (non-nil), otherwise
// the pool's global MAX_WORKERS — this is the "unset limit preserves today's
// behavior exactly" fallback from the acceptance criteria. inUse is the
// sweep-scoped in-flight count map from repoInUseCounts, mutated by dispatch
// as tasks are dispatched so limits are enforced within a single sweep too.
func (d *Dispatcher) repoAtLimit(repo gen.Repo, inUse map[string]int64) bool {
	limit := d.pool.MaxWorkers()
	if repo.MaxConcurrentRuns != nil && *repo.MaxConcurrentRuns > 0 {
		limit = int(*repo.MaxConcurrentRuns)
	}
	return inUse[repo.ID] >= int64(limit)
}

func (d *Dispatcher) dispatch(ctx context.Context, t gen.Task, configs []gen.AgentConfig, pools usagePools, inUse map[string]int64) {
	log := slog.With("component", "dispatcher", "task_id", t.ID)

	if t.Paused != 0 { // defense-in-depth; ListAgentPickupTasks already filters paused tasks
		log.Debug("dispatcher: skipping paused task")
		return
	}

	repo, err := d.q.GetRepo(ctx, t.RepoID)
	if err != nil {
		log.Error("dispatcher: get repo", "err", err)
		return
	}
	if d.repoAtLimit(repo, inUse) {
		log.Debug("dispatcher: skipping, repo at its concurrency limit", "repo_id", repo.ID)
		return
	}

	matches := matchConfigs(configs, t.Label)
	if len(matches) == 0 {
		log.Debug("dispatcher: no active config for label", "label", t.Label)
		return
	}

	// Walk matches in priority order (matches is already sorted by the SQL
	// query); the first config whose usage pool (provider account) isn't
	// rate-limit-blocked wins. This turns "primary account ran out of usage
	// credits" into automatic failover to a backup config sharing the same
	// label, and skips every config on that account, not just the one whose
	// run hit the limit.
	//
	// Within one agent config, its providers are tried in order (primary,
	// then fallback_provider_config_ids), so a tapped-out primary moves the
	// task to the next provider on the same agent before any lower-priority
	// agent config is considered.
	var matched *gen.AgentConfig
	var providerConfigID string
	var ladderRung *int
	failedAttempts := -1 // computed lazily, once per task per sweep
candidates:
	for _, cand := range matches {
		pcIDs := agentProviderIDs(cand)
		ladder := ParseLadder(cand.EscalationLadder)
		rung := 0
		if len(ladder) > 0 {
			if failedAttempts < 0 {
				failedAttempts = d.failedAttempts(ctx, t)
			}
			rung = selectRung(failedAttempts, len(ladder))
			pcIDs = rungProviderIDs(cand, ladder, rung)
		}
		for _, pcID := range pcIDs {
			if d.RateLimits != nil {
				if blocked, until := d.RateLimits.IsBlocked(pools.ofProvider(pcID)); blocked {
					log.Info("dispatcher: skipping rate-limited provider, trying next", "agent_config_id", cand.ID, "provider_config_id", pcID, "usage_pool", pools.ofProvider(pcID), "unblocked_at", until)
					continue
				}
			}
			if d.providerPaused(ctx, pcID) {
				log.Info("dispatcher: skipping provider that is paused, trying next", "agent_config_id", cand.ID, "provider_config_id", pcID)
				continue
			}
			matched, providerConfigID = cand, pcID
			if len(ladder) > 0 {
				r := rung
				ladderRung = &r
				if r > 0 {
					log.Info("dispatcher: escalating to ladder rung", "agent_config_id", cand.ID, "rung", r, "provider_config_id", pcID)
				}
			}
			break candidates
		}
	}
	if matched == nil {
		log.Info("dispatcher: all matching configs rate-limited or provider-paused, skipping dispatch", "label", t.Label)
		return
	}

	// Cost-budget guard: only gates the sweep path. DispatchReply (a human
	// actively replying/intervening) is intentionally never budget-gated.
	//
	// Fails closed (#336): escalateCostBudget returns (true, err) when the
	// phantom-run write fails, and a read failure while computing spend
	// returns (false, err). Either way an unverifiable budget must not let a
	// run start, so any error skips dispatch this sweep (retried next sweep).
	if blocked, err := d.checkCostBudget(ctx, t, *matched); err != nil {
		log.Error("dispatcher: cost budget check failed, skipping dispatch this sweep", "err", err, "blocked", blocked)
		return
	} else if blocked {
		return
	}

	// WIP backpressure: only gates the sweep path, same as the cost-budget
	// guard above. A hard-limited label that is already full simply isn't
	// dispatched into this sweep — the task stays on its current label and is
	// re-evaluated next sweep. This never blocks a run that is already in
	// flight from completing into a full column (that stays soft/visual).
	// A read failure stays fail-open (WIP is only backpressure, not a spend
	// cap), but a blocked result is honored regardless of err.
	blocked, err := d.checkWIPLimit(ctx, t)
	if err != nil {
		log.Error("dispatcher: wip limit check", "err", err)
	}
	if blocked {
		return
	}

	if _, err := d.startRun(ctx, t, *matched, runOptions{providerConfigID: providerConfigID, ladderRung: ladderRung}); err != nil {
		if errors.Is(err, ErrDispatchConflict) {
			log.Debug("dispatcher: skipping, task changed since it was listed (lost dispatch claim)")
			return
		}
		var transientErr transientErr
		log.Error("dispatcher: start run", "err", err, "transient", errors.As(err, &transientErr))
		return
	}
	// Reflect this dispatch in the sweep-scoped in-use map immediately so a
	// repo hitting its limit mid-sweep is skipped for the remaining tasks in
	// this same sweep, not just re-evaluated on the next tick.
	inUse[repo.ID]++
}

// providerPaused reports whether the provider config's provider kind is
// paused by the provider circuit breaker. The task is simply left queued (not
// escalated). A failed provider-config lookup is treated as not paused:
// startRun surfaces that error through its own path.
func (d *Dispatcher) providerPaused(ctx context.Context, providerConfigID string) bool {
	if d.ProviderPauses == nil {
		return false
	}
	paused := d.ProviderPauses.List()
	if len(paused) == 0 {
		return false
	}
	pc, err := d.q.GetProviderConfig(ctx, providerConfigID)
	if err != nil {
		return false
	}
	for _, p := range paused {
		if p.Provider == pc.Provider {
			return true
		}
	}
	return false
}

// PausedProviders returns the providers currently paused by the circuit
// breaker (for GET /health/providers).
func (d *Dispatcher) PausedProviders() []PausedProvider { return d.ProviderPauses.List() }

// ResumeProvider manually clears a provider's pause and reports whether it
// was paused.
func (d *Dispatcher) ResumeProvider(provider string) bool { return d.ProviderPauses.Resume(provider) }

// effectiveBudget resolves the effective cost budget for a task from its
// own max_cost_usd and its matched agent config's max_cost_usd. A value of
// 0 means "no cap from that source" (consistent with max_retries=0 meaning
// "disabled" elsewhere). When both are set, the lower (stricter) of the two
// wins; when only one is set, that one wins; when neither is set, the
// result is 0 (unlimited).
func effectiveBudget(taskBudget, configBudget float64) float64 {
	switch {
	case taskBudget <= 0:
		return configBudget
	case configBudget <= 0:
		return taskBudget
	case taskBudget < configBudget:
		return taskBudget
	default:
		return configBudget
	}
}

// defaultCostWarnRatio is the fallback early-warning threshold (see
// resolveCostWarnRatio) used if the cost_warning_settings row can't be read
// (e.g. a DB error) — matches the migration's seeded default.
const defaultCostWarnRatio = 0.8

// resolveCostWarnRatio reads the global cost-warning threshold (see
// 050_cost_warning / GetCostWarningSettings), falling back to
// defaultCostWarnRatio on any error so a transient DB hiccup never disables
// the watchdog's warning path entirely (it only ever affects when the
// early-warning event fires, not the hard budget kill switch/exhaustion
// gate, which are unaffected by this setting).
func (d *Dispatcher) resolveCostWarnRatio(ctx context.Context) float64 {
	row, err := d.q.GetCostWarningSettings(ctx)
	if err != nil {
		return defaultCostWarnRatio
	}
	if row.WarnRatio <= 0 || row.WarnRatio > 1 {
		return defaultCostWarnRatio
	}
	return row.WarnRatio
}

// checkCostBudget compares a task's cumulative recorded run cost (across
// every run regardless of status — see SumTaskCost) against its effective
// cost budget (the min of the task's and its matched agent config's
// max_cost_usd, whichever nonzero value is lower). If the budget is
// exhausted, it escalates the task to waiting_human WITHOUT starting a new
// provider run: waiting_human is a run-status, not a task label (the task
// itself stays on its current label — see Pool.handleTransientFailure for
// the analogous pattern), so this creates a "phantom" agent_runs row
// directly in that status, locks it as the task's active run so the
// dispatcher skips it on future sweeps, and publishes task.needs_human so
// the dashboard/task-detail UI picks it up live, exactly like a real
// waiting_human escalation. Returns true if dispatch should be skipped.
//
// Before the hard-exhaustion check, it also surfaces the early-warning
// threshold (see resolveCostWarnRatio) for tasks whose provider doesn't
// support the mid-run watchdog: if spend has already crossed warnRatio*budget
// but hasn't exhausted it, this publishes a one-shot task.cost_warning (gated
// on the task's cost_warned flag so it doesn't refire every sweep) and lets
// dispatch proceed normally.
//
// If the task is otherwise under budget but has at least one run flagged
// cost_unknown (see CountTaskCostUnknownRuns) -- i.e. tokens were consumed
// on a model with no resolvable price (see providers.PriceResolver) -- the
// accumulated spend can't be trusted as complete, since that run's real
// cost was recorded as $0 rather than estimated. Rather than silently
// letting the task run unbounded against a budget that no longer means
// anything, this escalates the same way an exhausted budget does, with a
// distinct message pointing at Configuration -> Pricing. This only fires
// while spent < budget: once the budget is independently exhausted on its
// own (possibly incomplete) recorded spend, the ordinary "budget exhausted"
// escalation below already covers it, so unknown-cost runs are never used
// to mask an otherwise-exhausted budget.
func (d *Dispatcher) checkCostBudget(ctx context.Context, t gen.Task, matched gen.AgentConfig) (bool, error) {
	budget := effectiveBudget(t.MaxCostUsd, matched.MaxCostUsd)
	if budget <= 0 {
		return false, nil
	}

	spent, err := d.q.SumTaskCost(ctx, t.ID)
	if err != nil {
		return false, fmt.Errorf("sum task cost: %w", err)
	}

	log := slog.With("component", "dispatcher", "task_id", t.ID)

	if spent < budget {
		unknownRuns, err := d.q.CountTaskCostUnknownRuns(ctx, t.ID)
		if err != nil {
			return false, fmt.Errorf("count task cost-unknown runs: %w", err)
		}
		if unknownRuns > 0 {
			msg := fmt.Sprintf("cost budget cannot be enforced: %d run(s) have unknown cost - add pricing for the model at Configuration -> Pricing", unknownRuns)
			log.Warn("dispatcher: skipping dispatch, task has cost-unknown runs under an active budget", "spent", spent, "budget", budget, "unknown_runs", unknownRuns)
			return d.escalateCostBudget(ctx, t, matched, ReasonCostUnknown, msg, "cost-unknown")
		}

		warnRatio := d.resolveCostWarnRatio(ctx)
		if t.CostWarned == 0 && warnRatio > 0 && spent >= warnRatio*budget {
			log.Warn("dispatcher: cost budget warning threshold crossed", "spent", spent, "budget", budget, "warn_ratio", warnRatio)
			if err := d.q.SetTaskCostWarned(ctx, t.ID); err != nil {
				log.Warn("dispatcher: set task cost_warned", "err", err)
			}
			if d.Publisher != nil {
				d.Publisher.Publish(wsevent.TypeTaskCostWarning, wsevent.TaskCostWarning{
					TaskID:    t.ID,
					SpentUSD:  spent,
					BudgetUSD: budget,
				})
			}
		}
		return false, nil
	}
	msg := fmt.Sprintf("budget exhausted: $%.2f of $%.2f", spent, budget)
	log.Warn("dispatcher: skipping dispatch, cost budget exhausted", "spent", spent, "budget", budget)
	return d.escalateCostBudget(ctx, t, matched, ReasonCostBudget, msg, "budget-exhausted")
}

// escalateCostBudget creates a "phantom" agent_runs row directly in
// waiting_human status (no provider invocation happens), locks it as the
// task's active run, and publishes task.needs_human -- the shared mechanism
// behind both of checkCostBudget's escalation paths (budget exhausted, and
// budget unenforceable due to cost-unknown runs). reason labels the wrapped
// error messages for each step (e.g. "budget-exhausted", "cost-unknown").
// Returns true (skip dispatch) alongside any error, matching
// checkCostBudget's contract that a failure here should still prevent
// dispatch this sweep.
func (d *Dispatcher) escalateCostBudget(ctx context.Context, t gen.Task, matched gen.AgentConfig, code EscalationReason, msg, reason string) (bool, error) {
	runID := uuid.NewString()
	if _, err := d.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:            runID,
		TaskID:        t.ID,
		AgentConfigID: &matched.ID,
	}); err != nil {
		return true, fmt.Errorf("create %s run: %w", reason, err)
	}
	// Lock the task on this run, same as a real waiting_human escalation —
	// stays locked until a human acts (raises the budget, or replies via
	// DispatchReply, which is not budget-gated). Deliberately set ONLY
	// active_agent_run_id, not current_agent_run_id: this phantom run has no
	// logs and no feedback, so it must never become the run WS replay shows
	// (ws/client.go keys off current_agent_run_id) or the run the next
	// dispatch reads rework feedback from (startRun's prior-feedback lookup
	// also keys off current_agent_run_id). Leaving current_agent_run_id
	// pointing at the last real run fixes both. See issue #344.
	if err := d.lifecycle().escalate(ctx, escalation{
		TaskID: t.ID, RunID: runID, Reason: code, Message: msg, Lock: lockPhantomOnly, FailFast: true,
	}); err != nil {
		return true, fmt.Errorf("escalate %s run: %w", reason, err)
	}

	return true, nil
}

// GlobalCostStatus is the current state of the global daily/monthly spend
// ceiling (see checkGlobalCostBudget), read by GET /health and GET /healthz
// so an operator sees a tripped cap immediately rather than only in logs —
// this is the one condition where the whole system has stopped dispatching
// new work while otherwise appearing healthy. Computed at read time from the
// last sweep's refreshGlobalCostStatus, not per-request, so polling /health
// doesn't add extra SumCostForDay/SumCostForMonth queries on top of the
// dispatcher's own once-per-sweep check.
type GlobalCostStatus struct {
	// DailyLimitUSD/MonthlyLimitUSD are the configured caps (0 = unlimited).
	DailyLimitUSD   float64 `json:"daily_limit_usd"`
	MonthlyLimitUSD float64 `json:"monthly_limit_usd"`
	// DailySpentUSD/MonthlySpentUSD are cumulative recorded cost (across ALL
	// runs regardless of status — see SumCostForDay/SumCostForMonth) for the
	// current UTC calendar day/month.
	DailySpentUSD   float64 `json:"daily_spent_usd"`
	MonthlySpentUSD float64 `json:"monthly_spent_usd"`
	// Tripped is true once either configured (nonzero) limit has been
	// reached or exceeded. While true, the dispatcher does not start any new
	// runs (existing in-flight runs are left to finish).
	Tripped bool `json:"tripped"`
	// TrippedReason is "daily", "monthly", or "" (not tripped) — whichever
	// cap tripped first, checked daily-then-monthly in refreshGlobalCostStatus.
	TrippedReason string `json:"tripped_reason,omitempty"`
}

// GlobalCostStatus returns the dispatcher's last-computed global
// spend-ceiling snapshot (see refreshGlobalCostStatus, which runs once per
// sweep). Safe for concurrent use.
func (d *Dispatcher) GlobalCostStatus() GlobalCostStatus {
	d.globalCostMu.Lock()
	defer d.globalCostMu.Unlock()
	return d.globalCostState
}

// refreshGlobalCostStatus recomputes the global daily/monthly spend-ceiling
// status (see GlobalCostStatus) and returns whether dispatch should be
// halted globally this sweep. Calendar-aligned to UTC (both "day" and
// "month" boundaries), not a rolling window — deliberately: the operator's
// mental model is a monthly bill, CostByDay already buckets by calendar day
// so this reuses the same alignment, and rolling windows are harder to
// reason about ("why did it un-trip at 3:47am?"). A configured limit of 0
// means unlimited for that period and is never itself a trip condition.
//
// Unlike checkCostBudget (per-task, escalates every exhausted task
// individually to waiting_human), tripping the global cap does NOT touch
// any task: it only stops the sweep from starting new runs. Escalating every
// pickup-eligible task to waiting_human at global scale would create
// hundreds of phantom runs (see escalateCostBudget) and, per #344, clobber
// current_agent_run_id on each — instead this halts dispatch here, fires a
// one-shot alert publish on the transition into the tripped state (never
// re-fired every sweep while it stays tripped), and lets GET /tasks report
// the new cost_budget_global block reason (see blockreason.go) so tasks
// still explain why they're not moving.
func (d *Dispatcher) refreshGlobalCostStatus(ctx context.Context) bool {
	if d.MaxDailyCostUSD <= 0 && d.MaxMonthlyCostUSD <= 0 {
		// No global cap configured — skip the queries entirely and clear any
		// stale tripped state (e.g. the cap was just turned off).
		d.globalCostMu.Lock()
		d.globalCostState = GlobalCostStatus{}
		d.globalCostAlerts = false
		d.globalCostMu.Unlock()
		return false
	}

	now := time.Now().UTC()
	dayStart, dayEnd, monthStart, monthEnd := costPeriodBounds(now)

	var dailySpent, monthlySpent float64
	if d.MaxDailyCostUSD > 0 {
		var err error
		dailySpent, err = d.q.SumCostForDay(ctx, gen.SumCostForDayParams{RangeStart: dayStart, RangeEnd: dayEnd})
		if err != nil {
			slog.Error("dispatcher: sum cost for day", "component", "dispatcher", "err", err)
		}
	}
	if d.MaxMonthlyCostUSD > 0 {
		var err error
		monthlySpent, err = d.q.SumCostForMonth(ctx, gen.SumCostForMonthParams{RangeStart: monthStart, RangeEnd: monthEnd})
		if err != nil {
			slog.Error("dispatcher: sum cost for month", "component", "dispatcher", "err", err)
		}
	}

	status := GlobalCostStatus{
		DailyLimitUSD:   d.MaxDailyCostUSD,
		MonthlyLimitUSD: d.MaxMonthlyCostUSD,
		DailySpentUSD:   dailySpent,
		MonthlySpentUSD: monthlySpent,
	}
	switch {
	case d.MaxDailyCostUSD > 0 && dailySpent >= d.MaxDailyCostUSD:
		status.Tripped = true
		status.TrippedReason = "daily"
	case d.MaxMonthlyCostUSD > 0 && monthlySpent >= d.MaxMonthlyCostUSD:
		status.Tripped = true
		status.TrippedReason = "monthly"
	}

	d.globalCostMu.Lock()
	wasTripped := d.globalCostAlerts
	d.globalCostState = status
	if status.Tripped {
		d.globalCostAlerts = true
	} else {
		d.globalCostAlerts = false
	}
	d.globalCostMu.Unlock()

	if status.Tripped && !wasTripped {
		msg := fmt.Sprintf("global cost budget tripped (%s): $%.2f of $%.2f", status.TrippedReason, dailySpentOrMonthly(status), limitFor(status))
		slog.Warn("dispatcher: global cost budget tripped, halting all new dispatch", "component", "dispatcher", "reason", status.TrippedReason, "daily_spent", dailySpent, "daily_limit", d.MaxDailyCostUSD, "monthly_spent", monthlySpent, "monthly_limit", d.MaxMonthlyCostUSD)
		if d.Publisher != nil {
			d.Publisher.Publish(wsevent.TypeSystemCostBudgetTripped, wsevent.SystemCostBudgetTripped{
				Reason:          status.TrippedReason,
				Message:         msg,
				DailySpentUSD:   status.DailySpentUSD,
				DailyLimitUSD:   status.DailyLimitUSD,
				MonthlySpentUSD: status.MonthlySpentUSD,
				MonthlyLimitUSD: status.MonthlyLimitUSD,
			})
		}
	}

	return status.Tripped
}

// dailySpentOrMonthly and limitFor pick the spent/limit figure matching
// whichever cap tripped, for the one-shot alert log/publish message.
func dailySpentOrMonthly(s GlobalCostStatus) float64 {
	if s.TrippedReason == "monthly" {
		return s.MonthlySpentUSD
	}
	return s.DailySpentUSD
}

func limitFor(s GlobalCostStatus) float64 {
	if s.TrippedReason == "monthly" {
		return s.MonthlyLimitUSD
	}
	return s.DailyLimitUSD
}

// checkWIPLimit resolves the task's agent-triggerable "success" transition
// target and, if that target label opted into hard WIP enforcement
// (wip_limit_hard) and is already at or over its wip_limit, returns true so
// the dispatcher skips this sweep's dispatch — pure backpressure, no error,
// no run created. The task simply stays put and is re-evaluated next sweep.
//
// An ambiguous or missing success target (e.g. no unambiguous agent
// transition out of the current label) is treated as "unknown" and never
// blocks dispatch — we never guess our way into starving a task.
func (d *Dispatcher) checkWIPLimit(ctx context.Context, t gen.Task) (bool, error) {
	transitions, err := d.q.ListWorkflowTransitions(ctx, t.WorkflowID)
	if err != nil {
		return false, fmt.Errorf("list workflow transitions: %w", err)
	}
	target, ok := workflow.SuccessTarget(transitions, t.Label)
	if !ok || target == "" {
		return false, nil
	}

	labels, err := d.q.ListWorkflowLabels(ctx, t.WorkflowID)
	if err != nil {
		return false, fmt.Errorf("list workflow labels: %w", err)
	}
	var targetLabel *gen.WorkflowLabel
	for i := range labels {
		if labels[i].Name == target {
			targetLabel = &labels[i]
			break
		}
	}
	if targetLabel == nil || targetLabel.WipLimitHard == 0 || targetLabel.WipLimit == nil {
		return false, nil
	}
	limit := *targetLabel.WipLimit
	if limit <= 0 {
		return false, nil // treat non-positive limits as unlimited
	}

	count, err := d.q.CountTasksByLabel(ctx, gen.CountTasksByLabelParams{
		WorkflowID: t.WorkflowID,
		Label:      target,
	})
	if err != nil {
		return false, fmt.Errorf("count tasks by label: %w", err)
	}

	if count < limit {
		return false, nil
	}

	slog.With("component", "dispatcher", "task_id", t.ID).Info(
		"dispatcher: WIP limit reached, applying backpressure",
		"target_label", target, "count", count, "limit", limit,
	)
	return true, nil
}

// Sentinel errors for DispatchReply, mapped to HTTP statuses by the handler.
var (
	// ErrRunNotWaiting means the task has no active run in waiting_human state.
	ErrRunNotWaiting = errors.New("task has no agent run waiting for human input")
	// ErrNoMatchingConfig means no enabled agent config could serve the reply run.
	ErrNoMatchingConfig = errors.New("no enabled agent config available for this task")
	// ErrPoolSaturated means the worker pool queue was full and the run was dropped.
	ErrPoolSaturated = errors.New("agent worker pool is full")
	// ErrProviderUnavailable means the agent config's provider is disabled or
	// unrecognized, so ProviderFactory returned nil and the run could not be
	// dispatched.
	ErrProviderUnavailable = errors.New("agent config's provider is disabled or unknown")
	// ErrRuntimePrepFailed means the repo's pinned toolchains (repos.
	// runtime_languages) failed to install/prepare (mise install or, for a
	// python pin, uv venv) before the provider could be invoked. See
	// prepareRuntime.
	ErrRuntimePrepFailed = errors.New("runtime toolchain prep failed")
	// ErrDispatchConflict means the task's dispatch lock or label changed
	// between the dispatcher reading the task and claiming it (a concurrent
	// reply/dispatch won, or a human moved the task), so no run was started.
	ErrDispatchConflict = errors.New("task changed concurrently; dispatch lock was not acquired")
)

// DispatchReply starts a new run for a task whose active run is waiting_human,
// carrying the human's textual answer to the agent's request_human question.
// The new run resumes the prior provider session where supported (claude,
// unless the config opts out via resume_sessions), so the reply lands as the
// next message of the same conversation; otherwise it starts cold with the
// reply injected into the prompt. The replied-to run keeps its waiting_human
// status (matching the approve/reject flows); the task's active-run lock moves
// to the new run. Returns the new run's ID.
func (d *Dispatcher) DispatchReply(ctx context.Context, taskID, message string) (string, error) {
	t, err := d.q.GetTask(ctx, taskID)
	if err != nil {
		return "", err // sql.ErrNoRows → 404 in the handler
	}
	if t.ActiveAgentRunID == nil {
		return "", ErrRunNotWaiting
	}
	run, err := d.q.GetAgentRun(ctx, *t.ActiveAgentRunID)
	if err != nil || run.Status != "waiting_human" {
		return "", ErrRunNotWaiting
	}

	// Prefer the config that asked the question; fall back to label matching
	// if it has since been deleted or disabled.
	var matched *gen.AgentConfig
	if run.AgentConfigID != nil {
		if cfg, cerr := d.q.GetAgentConfig(ctx, *run.AgentConfigID); cerr == nil && cfg.Enabled == 1 {
			matched = &cfg
		}
	}
	if matched == nil {
		configs, cerr := d.q.ListAgentConfigs(ctx)
		if cerr != nil {
			return "", cerr
		}
		if matches := matchConfigs(configs, t.Label); len(matches) > 0 {
			matched = matches[0]
		}
	}
	if matched == nil {
		return "", ErrNoMatchingConfig
	}

	// A human reply is a human acting: give the task a fresh failure budget.
	if err := d.q.ResetTaskFailureCount(ctx, taskID); err != nil {
		slog.Warn("dispatcher: reset failure count on reply", "component", "dispatcher", "task_id", taskID, "err", err)
	}
	// Reply on the provider that asked, so its session can be resumed, as
	// long as it is still in the agent config's list.
	opts := runOptions{humanReply: &message}
	if run.ProviderConfigID != nil && run.AgentConfigID != nil && *run.AgentConfigID == matched.ID {
		ladder := ParseLadder(matched.EscalationLadder)
		pcIDs := agentProviderIDs(matched)
		if len(ladder) > 0 {
			rung := 0
			if run.LadderRung != nil {
				rung = selectRung(int(*run.LadderRung), len(ladder))
			}
			opts.ladderRung = &rung
			pcIDs = rungProviderIDs(matched, ladder, rung)
		}
		for _, id := range pcIDs {
			if id == *run.ProviderConfigID {
				opts.providerConfigID = id
				break
			}
		}
	}
	newRunID, err := d.startRun(ctx, t, *matched, opts)
	if err != nil {
		return newRunID, err
	}
	// Record after startRun so the reply run's own prompt does not list the
	// current reply twice (it carries RESPONSE FROM HUMAN) and a failed
	// dispatch records nothing. Every later run sees it.
	question := run.Notes
	if run.Feedback != nil && *run.Feedback != "" {
		question = run.Feedback
	}
	reason := ""
	if run.EscalationReason != nil {
		reason = *run.EscalationReason
	}
	q := ""
	if question != nil {
		q = *question
	}
	if rerr := d.q.RecordHumanReply(ctx, gen.RecordHumanReplyParams{
		RunID: run.ID, TaskID: t.ID, Label: t.Label, EscalationReason: reason,
		Question: q, Reply: &message, ReplyRunID: &newRunID,
	}); rerr != nil {
		slog.Warn("dispatcher: record human reply", "component", "dispatcher", "task_id", taskID, "err", rerr)
	}
	return newRunID, nil
}

// runOptions carries the extras a non-sweep dispatch (currently only the
// human-reply flow) layers on top of a standard run.
type runOptions struct {
	humanReply *string
	// providerConfigID is the provider (from the agent config's list) the run
	// uses; empty means the agent config's primary.
	providerConfigID string
	// ladderRung is the escalation-ladder rung in effect (nil = agent config has
	// no ladder).
	ladderRung *int
}

// startRun creates the run row,
// marks it as the task's active run, and submits the job to the pool. Shared
// by the sweep dispatch path and DispatchReply. It reads as a sequence of
// phases, each delegated to a helper below.
func (d *Dispatcher) startRun(ctx context.Context, t gen.Task, matched gen.AgentConfig, opts runOptions) (string, error) {
	log := slog.With("component", "dispatcher", "task_id", t.ID)

	repo, err := d.q.GetRepo(ctx, t.RepoID)
	if err != nil {
		return "", fmt.Errorf("get repo: %w", err)
	}

	// Only a local stat happens here. A missing worktree is provisioned later,
	// from the pool worker (see worktreeSetup / Pool.setupWorktree), because
	// provisioning runs `git fetch --prune`, which can block on the network and
	// must never stall the single-threaded sweep or a DispatchReply request.
	workDir, haveWorktree := existingWorktree(t)

	runID := uuid.NewString()
	log = log.With("run_id", runID)

	pcID := opts.providerConfigID
	if pcID == "" {
		pcID = matched.ProviderConfigID
	}
	agentCfg, resumeSessionID, err := d.resolveAgentConfig(ctx, t, matched, pcID, opts.ladderRung)
	if err != nil {
		return "", err
	}
	resumeSessionCostUSD := d.sessionCostBaseline(ctx, t.ID, resumeSessionID)

	var feedback *string
	if t.CurrentAgentRunID != nil {
		prior, _ := d.q.GetAgentRun(ctx, *t.CurrentAgentRunID)
		feedback = prior.Feedback
	}

	if err := d.persistRunRow(ctx, t, matched, pcID, runID, feedback, opts.ladderRung); err != nil {
		return "", err
	}

	// Resolving pins is pure JSON parsing (no I/O), so it's cheap enough to do
	// synchronously here — unlike the actual toolchain prep (mise install /
	// uv venv), which the pool now runs from the run's own goroutine (see
	// Pool.prepareRuntime) so a slow/cold install can never block the
	// single-threaded sweep loop or a DispatchReply request. A malformed
	// stored config is still caught here, before the job is ever submitted.
	runtimeSpec, err := d.resolveRuntimeSpec(repo, workDir)
	if err != nil {
		return "", d.escalateRuntimePrepFailure(ctx, t, runID, err, log)
	}

	// Record the human's reply at the top of the new run's log so the
	// conversation reads coherently in the UI (and in WS replay).
	if opts.humanReply != nil && *opts.humanReply != "" {
		if err := d.q.CreateAgentLog(ctx, gen.CreateAgentLogParams{
			ID:         uuid.NewString(),
			AgentRunID: runID,
			Timestamp:  time.Now(),
			Type:       "system",
			Content:    "Human reply: " + *opts.humanReply,
		}); err != nil {
			log.Warn("dispatcher: record human reply log", "err", err)
		}
	}

	attachmentRels, attachmentAbsPaths := d.resolveAttachments(t, workDir, log)
	var setup func(context.Context) (WorktreeResult, error)
	if !haveWorktree {
		setup = d.worktreeSetup(t, repo, attachmentAbsPaths, log)
	}
	reviewComments := d.loadReviewComments(ctx, t.ID, log)
	sourceComments := d.loadSourceComments(ctx, t.ID, log)
	humanDecisions := d.loadHumanDecisions(ctx, t.ID, log)

	var agentNotes *string
	if t.AgentNotes != "" {
		agentNotes = &t.AgentNotes
	}

	transitions := d.buildTransitionHints(ctx, t.ID, t.WorkflowID, t.Label)
	provider := d.ProviderFactory(agentCfg)
	if provider == nil {
		// The provider is disabled (deprecated write-path rejection doesn't
		// apply retroactively to rows already in the DB, but the factory
		// still has no runner for it) or the provider string is otherwise
		// unrecognized. This is a permanent, config-level problem, not a
		// transient one, so it must NOT clear the task's active-run lock:
		// ListAgentPickupTasks only re-selects a task once active_agent_run_id
		// is NULL, and this failure happens before any real provider work runs
		// (unlike a normal "failed" terminal run, which is naturally
		// rate-limited by however long the real attempt took). Clearing the
		// lock here let the 15ms-interval sweep immediately re-dispatch the
		// same task, hot-looping runs every tick until a human intervened —
		// caught by TestE2E_NilProviderFailsRunCleanly flaking under -race as
		// multiple same-second-resolution created_at rows raced for "first".
		// Instead, escalate straight to waiting_human (same shape as
		// checkCostBudget's exhausted-budget escalation) so the task stays
		// locked on this run until a human fixes the config and replies.
		msg := ProviderUnavailableMessage(agentCfg.Provider)
		log.Error("dispatcher: no runner for provider", "provider", agentCfg.Provider)
		// persistRunRow (above) already set BOTH current_agent_run_id and
		// active_agent_run_id to this phantom run before the provider was
		// resolved. This run has no logs and no feedback, so — same reasoning
		// as escalateCostBudget — restore current_agent_run_id to the prior
		// real run (t.CurrentAgentRunID, captured before persistRunRow ran) so
		// WS replay and the next dispatch's feedback lookup don't hit this
		// phantom row. active_agent_run_id is deliberately left pointing at
		// runID: it still needs to hold the re-dispatch lock until a human
		// fixes the config. See issue #344.
		if err := d.lifecycle().escalate(ctx, escalation{
			TaskID: t.ID, RunID: runID, Reason: ReasonProviderMissing, Message: msg,
			Lock: lockRestoreCurrent, CurrentRunID: t.CurrentAgentRunID, // may be nil if this was the task's first run - that's correct
		}); err != nil {
			log.Warn("dispatcher: nil-provider escalation", "err", err)
		}
		if IsRemovedProvider(agentCfg.Provider) {
			return "", fmt.Errorf("%w: %s", ErrProviderUnavailable, msg)
		}
		return "", fmt.Errorf("%w: %q", ErrProviderUnavailable, agentCfg.Provider)
	}

	var fileScope []string
	if fo, ferr := d.q.GetTaskFanout(ctx, t.ID); ferr == nil {
		fileScope = splitFileScope(fo.FileScope)
	}

	// If this is a parent with subtasks that conflicted on merge-back, hand the
	// work agent the conflict context so it resolves the merges on this branch.
	var subtaskIntegration *string
	var subtaskConflicts *string
	if d.Subtasks != nil {
		subtaskIntegration = d.Subtasks.BuildIntegrationContext(ctx, t.ID, repo.VerifyCommand)
		if subtaskIntegration == nil {
			subtaskConflicts = d.Subtasks.BuildConflictContext(ctx, t.ID)
		}
	}

	// Cost-budget plumbing for the provider's mid-run kill switch (see
	// providers/cost_watchdog.go). checkCostBudget already vetoed dispatch
	// entirely if the budget was already exhausted, so this run always starts
	// with spent < budget (or budget == 0, meaning no cap — costBudgetUSD
	// stays 0 either way, since effectiveBudget already returns 0 for that
	// case). Best-effort: a SumTaskCost error here degrades to "no budget
	// info for the watchdog" rather than blocking the run entirely, since the
	// hard pre-dispatch gate above is the authoritative enforcement point.
	costBudgetUSD := effectiveBudget(t.MaxCostUsd, matched.MaxCostUsd)
	var costSpentUSD float64
	if costBudgetUSD > 0 {
		if spent, serr := d.q.SumTaskCost(ctx, t.ID); serr == nil {
			costSpentUSD = spent
		} else {
			log.Warn("dispatcher: sum task cost for watchdog", "err", serr)
		}
	}

	enqueued := d.pool.Submit(Job{
		RunID:         runID,
		Provider:      provider,
		SetupWorktree: setup,
		SelectLessons: d.lessonSelector(t, log),
		Input: RunInput{
			RunID:                runID,
			Task:                 Task{ID: t.ID, Title: t.Title, Description: t.Description, Type: t.Type, Label: t.Label, WorkflowID: t.WorkflowID, AgentNotes: t.AgentNotes, Branch: t.Branch, ParentID: derefStr(t.ParentTaskID), FileScope: fileScope, RepoPath: repo.Path, Attachments: attachmentRels},
			AgentConfig:          agentCfg,
			RepoPath:             workDir,
			RepoRemoteURL:        derefStr(repo.RemoteUrl),
			Transitions:          transitions,
			Feedback:             feedback,
			PriorPlan:            agentNotes,
			OpenReviewComments:   reviewComments,
			SourceComments:       sourceComments,
			HumanDecisions:       humanDecisions,
			AttachmentAbsPaths:   attachmentAbsPaths,
			ResumeSessionID:      resumeSessionID,
			ResumeSessionCostUSD: resumeSessionCostUSD,
			HumanReply:           opts.humanReply,
			SubtaskConflicts:     subtaskConflicts,
			SubtaskIntegration:   subtaskIntegration,
			CostBudgetUSD:        costBudgetUSD,
			CostSpentUSD:         costSpentUSD,
			CostWarnRatio:        d.resolveCostWarnRatio(ctx),
			Runtime:              runtimeSpec,
		},
	})
	if !enqueued {
		// Refused because the server is shutting down: not a failure.
		dropStatus := "failed"
		if d.pool.Draining() {
			dropStatus = "interrupted"
		}
		_, _ = d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
			Status: dropStatus,
			ID:     runID,
		})
		// Owner-scoped: this run just claimed the lock (SetTaskActiveRun above),
		// so it should still own it here, but scope the clear defensively and
		// consistently with every other run-owned release (see issue #244).
		if n, cerr := d.q.ClearActiveAgentRunIfOwner(ctx, gen.ClearActiveAgentRunIfOwnerParams{ID: t.ID, ActiveAgentRunID: &runID}); cerr != nil {
			log.Warn("dispatcher: release lock after pool-saturated enqueue failure", "err", cerr)
		} else if n == 0 {
			log.Warn("dispatcher: skipped clearing dispatch lock owned by another run", "run_id", runID)
		}
		return "", ErrPoolSaturated
	}

	metrics.DispatchedRunsTotal.Inc()
	log.Info("dispatcher: agent dispatched", "label", t.Label, "agent", matched.Name, "provider", agentCfg.Provider, "agent_id", matched.ID, "agent_enabled", matched.Enabled, "resume_session", resumeSessionID != "", "human_reply", opts.humanReply != nil)
	return runID, nil
}

// resolveRuntimeSpec parses and validates the repo's pinned toolchains
// (repos.runtime_languages) into a RuntimeSpec, ahead of the job being
// submitted to the pool. A repo with no runtime configured (the column
// empty) returns (nil, nil) — this is the §4.1 byte-identical guarantee: no
// I/O happens in that case, so every downstream caller (providers.
// applyRuntime) sees the same nil RuntimeSpec it always has and spawns
// exactly as before this feature existed.
//
// This is deliberately pure (JSON parse + regex validation only, via
// runtime.ParsePins) — it does NOT install anything. The actual toolchain
// prep (`mise install`, and for a python pin `uv venv`) is comparatively
// slow (a cold install can take minutes) and runs later, from the pool's
// per-run goroutine (see Pool.prepareRuntime), so it can never block the
// dispatcher's single-threaded sweep loop or a DispatchReply request running
// under an HTTP request context. Any failure here (e.g. corrupt stored
// config — persisted config should already be valid since the API validates
// on write, but this defends against a bad direct DB edit) is returned as-is;
// the caller (startRun) escalates to waiting_human the same way a later prep
// failure does (see escalateRuntimePrepFailure).
func (d *Dispatcher) resolveRuntimeSpec(repo gen.Repo, worktreeDir string) (*RuntimeSpec, error) {
	pins, err := runtime.ParsePins(repo.RuntimeLanguages)
	if err != nil {
		return nil, fmt.Errorf("parse repo runtime_languages: %w", err)
	}
	if len(pins) == 0 {
		return nil, nil
	}
	return &RuntimeSpec{Pins: pins, WorktreeDir: worktreeDir}, nil
}

// escalateRuntimePrepFailure marks the given run waiting_human after
// prepareRuntime failed, with prepErr's message recorded as the run's notes
// (mise's own combined-output tail, capped by runtime.Prep, is embedded in
// prepErr so a human sees the real toolchain failure, not just "prep
// failed"). Mirrors startRun's nil-provider escalation immediately below:
// persistRunRow already pointed both current_agent_run_id and
// active_agent_run_id at this phantom run, so current_agent_run_id is
// restored to the task's prior real run (this run has no logs/feedback of
// its own) while active_agent_run_id is deliberately left locked on runID
// until a human intervenes — re-dispatch must never fall back to a plain,
// unprepared spawn. Returns ErrRuntimePrepFailed (wrapping prepErr) for
// startRun to propagate.
func (d *Dispatcher) escalateRuntimePrepFailure(ctx context.Context, t gen.Task, runID string, prepErr error, log *slog.Logger) error {
	msg := fmt.Sprintf("agent runtime prep failed: %v", prepErr)
	log.Error("dispatcher: runtime prep failed", "err", prepErr)
	if err := d.lifecycle().escalate(ctx, escalation{
		TaskID: t.ID, RunID: runID, Reason: ReasonRuntimePrep, Message: msg,
		Lock: lockRestoreCurrent, CurrentRunID: t.CurrentAgentRunID, // may be nil if this was the task's first run - that's correct
	}); err != nil {
		log.Warn("dispatcher: runtime-prep escalation", "err", err)
	}
	return fmt.Errorf("%w: %v", ErrRuntimePrepFailed, prepErr)
}

// existingWorktree returns the task's recorded worktree directory when it is
// still present on disk. It only stats the filesystem (no git, no network), so
// it is safe to call from the sweep.
//
// t.WorktreePath can be stale -- pointing at a directory that no longer exists
// -- for a task that was archived (worktree reclaimed; see
// api/handlers.reclaimWorktreeOnArchive) and later unarchived, or one whose
// worktree the periodic sweeper reclaimed out from under it (see
// internal/worktreesweep, which never touches the DB row). A missing directory
// is treated the same as an empty WorktreePath: the worker reprovisions it
// rather than handing the agent a nonexistent cwd.
func existingWorktree(t gen.Task) (string, bool) {
	workDir := t.WorktreePath
	if workDir == "" {
		return "", false
	}
	if fi, statErr := os.Stat(workDir); statErr == nil && fi.IsDir() {
		return workDir, true
	}
	slog.Warn("dispatcher: task's recorded worktree is missing; reprovisioning", "task_id", t.ID, "worktree_path", workDir)
	return "", false
}

// worktreeSetup returns the closure the pool worker runs (see
// Pool.setupWorktree) to provision the task's git worktree. Each task works in
// its own worktree on its own branch so concurrent agents on the same repo
// don't conflict. A subtask's branch is cut from its parent's branch (not the
// repo base) so its work merges back cleanly.
//
// The closure runs `git fetch --prune` and `git worktree add -b`, both of
// which mutate the repo's shared ref store, so it serializes against sibling
// worktrees' commits/merges/branch-deletes/worktree-adds -- and the worktree
// sweeper -- via the per-repo lock (see worktree.go's repoGitLocks doc comment
// / issue #344). The lock is a plain, non-reentrant mutex, never taken inside
// provisionWorktree itself, since subtasks.go already holds it around calls to
// provisionWorktree/RemoveWorktree. The task row is updated while the lock is
// still held so the sweeper never sees a directory without its recorded owner.
func (d *Dispatcher) worktreeSetup(t gen.Task, repo gen.Repo, attachmentAbsPaths []string, log *slog.Logger) func(context.Context) (WorktreeResult, error) {
	return func(ctx context.Context) (WorktreeResult, error) {
		// parentBranchBase does a DB read; resolve it before taking the repo git
		// lock so a slow DB doesn't hold the lock.
		base := d.parentBranchBase(ctx, t)

		var wtPath, branch, baseRef string
		var perr error
		lock := RepoGitLock(repo.Path)
		lock.Lock()
		defer lock.Unlock()
		if base != "" {
			wtPath, branch, baseRef, perr = provisionWorktreeFrom(ctx, repo.Path, t.ID, t.Title, base)
		} else {
			wtPath, branch, baseRef, perr = provisionWorktree(ctx, repo.Path, t.ID, t.Title)
		}
		if perr != nil {
			return WorktreeResult{}, fmt.Errorf("provision worktree: %w", perr)
		}
		if err := d.q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{
			Branch:       branch,
			WorktreePath: wtPath,
			BaseRef:      baseRef,
			ID:           t.ID,
		}); err != nil {
			return WorktreeResult{}, fmt.Errorf("persist worktree: %w", err)
		}
		if len(attachmentAbsPaths) > 0 {
			if err := copyAttachmentsToWorktree(wtPath, attachmentAbsPaths); err != nil {
				log.Warn("dispatcher: copy attachments to worktree", "err", err)
			}
		}
		return WorktreeResult{Path: wtPath, Branch: branch}, nil
	}
}

// providerSupportsResume reports whether the given provider's session-resume
// path is verified end-to-end: it records a session id, and the runner's
// resume invocation is correct. claude, qwen_code, codex_cli, and opencode all
// qualify (see issue #281).
func providerSupportsResume(provider string) bool {
	switch provider {
	case "claude", "qwen_code", "codex_cli", "opencode":
		return true
	default:
		return false
	}
}

// resolveAgentConfig builds the effective agent config for the run and resolves
// the provider session to resume, if any. Resume is honored for claude,
// qwen_code, codex_cli, and opencode (and only when the config hasn't opted
// out); the runner falls back to a cold start if the session no longer exists.
func (d *Dispatcher) resolveAgentConfig(ctx context.Context, t gen.Task, matched gen.AgentConfig, providerConfigID string, ladderRung *int) (AgentConfig, string, error) {
	pc, err := d.q.GetProviderConfig(ctx, providerConfigID)
	if err != nil {
		return AgentConfig{}, "", fmt.Errorf("get provider config: %w", err)
	}
	agentCfg := toAgentConfig(matched, pc)
	if ladderRung != nil {
		agentCfg.Effort = rungEffort(matched, ParseLadder(matched.EscalationLadder), *ladderRung)
	}

	var resumeSessionID string
	if agentCfg.ResumeSessions && providerSupportsResume(agentCfg.Provider) {
		if sid, serr := d.q.GetLatestTaskSession(ctx, gen.GetLatestTaskSessionParams{
			TaskID:           t.ID,
			AgentConfigID:    &matched.ID,
			ProviderConfigID: &providerConfigID,
		}); serr == nil && sid != "" {
			resumeSessionID = sid
		}
	}
	return agentCfg, resumeSessionID, nil
}

// sessionCostBaseline returns the cumulative session cost recorded by the
// latest earlier run of sessionID on this task, or 0 when there is no session
// to resume or none was recorded. Best-effort: a lookup error degrades to 0,
// which records the provider's full session total as before.
func (d *Dispatcher) sessionCostBaseline(ctx context.Context, taskID, sessionID string) float64 {
	if sessionID == "" {
		return 0
	}
	v, err := d.q.GetSessionCostBaseline(ctx, gen.GetSessionCostBaselineParams{TaskID: taskID, SessionID: sessionID})
	if err != nil || v == nil {
		return 0
	}
	return *v
}

// persistRunRow creates the run row and claims the task's dispatch lock in a
// single transaction. These two writes must be atomic: if the run were created
// but the task never pointed at it (a crash or error between the statements), an
// orphaned 'pending' run would linger with nothing gating re-dispatch.
//
// The claim is a compare-and-swap against the task state the caller read (t):
// the task must still be on t.Label and its lock must still be exactly what t
// saw (free for a sweep dispatch, held by the waiting run for a human reply).
// If another dispatch/reply took the lock, or a human moved the task, in the
// meantime, zero rows are affected, the transaction (including the just-created
// run row) is rolled back, and ErrDispatchConflict is returned so the caller
// does not start the run.
func (d *Dispatcher) persistRunRow(ctx context.Context, t gen.Task, matched gen.AgentConfig, providerConfigID, runID string, feedback *string, ladderRung *int) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	tq := d.q.WithTx(tx)
	if _, err := tq.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:            runID,
		TaskID:        t.ID,
		AgentConfigID: &matched.ID,
		Feedback:      feedback,
	}); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("create agent run: %w", err)
	}
	if err := tq.SetAgentRunProviderConfig(ctx, gen.SetAgentRunProviderConfigParams{
		ProviderConfigID: &providerConfigID,
		ID:               runID,
	}); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("set agent run provider config: %w", err)
	}
	if ladderRung != nil {
		r := int64(*ladderRung)
		if err := tq.SetAgentRunLadderRung(ctx, gen.SetAgentRunLadderRungParams{LadderRung: &r, ID: runID}); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set agent run ladder rung: %w", err)
		}
	}
	// Claim the task's active run so the next sweep skips it.
	var claimed int64
	if t.ActiveAgentRunID == nil {
		claimed, err = tq.ClaimTaskActiveRunIfFree(ctx, gen.ClaimTaskActiveRunIfFreeParams{
			CurrentAgentRunID: &runID,
			ActiveAgentRunID:  &runID,
			ID:                t.ID,
			Label:             t.Label,
		})
	} else {
		claimed, err = tq.ClaimTaskActiveRunIfHeldBy(ctx, gen.ClaimTaskActiveRunIfHeldByParams{
			CurrentAgentRunID:  &runID,
			ActiveAgentRunID:   &runID,
			ID:                 t.ID,
			Label:              t.Label,
			ActiveAgentRunID_2: t.ActiveAgentRunID,
		})
	}
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("set task active run: %w", err)
	}
	if claimed == 0 {
		_ = tx.Rollback()
		return ErrDispatchConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit run creation: %w", err)
	}
	return nil
}

// resolveAttachments parses the task's attachment list, builds absolute paths
// under the upload dir, and copies the images into the worktree so the agent can
// read them via file tools. Returns the relative paths (for the run input) and
// the absolute paths.
func (d *Dispatcher) resolveAttachments(t gen.Task, workDir string, log *slog.Logger) ([]string, []string) {
	var attachmentRels []string
	if t.Attachments != "" && t.Attachments != "[]" {
		_ = json.Unmarshal([]byte(t.Attachments), &attachmentRels)
	}

	var attachmentAbsPaths []string
	for _, rel := range attachmentRels {
		if d.uploadDir != "" {
			attachmentAbsPaths = append(attachmentAbsPaths, filepath.Join(d.uploadDir, rel))
		}
	}

	if len(attachmentAbsPaths) > 0 && workDir != "" {
		if err := copyAttachmentsToWorktree(workDir, attachmentAbsPaths); err != nil {
			log.Warn("dispatcher: copy attachments to worktree", "err", err)
		}
	}
	return attachmentRels, attachmentAbsPaths
}

// loadReviewComments loads the task's open inline diff review comments. They are
// injected into the prompt on every run until an agent (or a human) resolves them.
func (d *Dispatcher) loadReviewComments(ctx context.Context, taskID string, log *slog.Logger) []ReviewComment {
	var reviewComments []ReviewComment
	if rows, err := d.q.ListOpenTaskReviewComments(ctx, taskID); err != nil {
		log.Warn("dispatcher: list open review comments", "err", err)
	} else {
		for _, c := range rows {
			reviewComments = append(reviewComments, ReviewComment{
				ID:         c.ID,
				FilePath:   c.FilePath,
				Side:       c.Side,
				StartLine:  c.StartLine,
				EndLine:    c.EndLine,
				QuotedText: c.QuotedText,
				Body:       c.Body,
			})
		}
	}
	return reviewComments
}

// loadHumanDecisions loads the human replies given on this task so far.
// Best-effort: a query failure is logged and yields none.
func (d *Dispatcher) loadHumanDecisions(ctx context.Context, taskID string, log *slog.Logger) []HumanDecision {
	rows, err := d.q.ListTaskHumanDecisions(ctx, taskID)
	if err != nil {
		log.Warn("dispatcher: list task human decisions", "err", err)
		return nil
	}
	var out []HumanDecision
	for _, r := range rows {
		if r.Reply == nil || *r.Reply == "" {
			continue
		}
		at := r.AskedAt
		if r.RepliedAt != nil {
			at = *r.RepliedAt
		}
		out = append(out, HumanDecision{
			Question:  r.Question,
			Reply:     *r.Reply,
			RepliedAt: at.UTC().Format("2006-01-02 15:04 UTC"),
			Label:     r.Label,
			AgentName: r.AgentName,
		})
	}
	return out
}

// loadSourceComments loads the task's ingested source-issue comment thread
// (see tasksource's importer). Best-effort, mirroring loadReviewComments: a
// query failure is logged and yields no comments rather than failing the run.
func (d *Dispatcher) loadSourceComments(ctx context.Context, taskID string, log *slog.Logger) []SourceComment {
	var sourceComments []SourceComment
	if rows, err := d.q.ListTaskSourceComments(ctx, taskID); err != nil {
		log.Warn("dispatcher: list task source comments", "err", err)
	} else {
		for _, c := range rows {
			sourceComments = append(sourceComments, SourceComment{
				Author:    c.Author,
				Body:      c.Body,
				CreatedAt: c.ExternalCreatedAt,
			})
		}
	}
	return sourceComments
}

// parentBranchBase returns the branch a subtask should fork from: its parent's
// branch. Returns "" for a top-level task, or when the parent has no branch yet
// (falls back to the repo base). The parent's branch always exists by the time a
// child is dispatched — the planning run that created the child provisioned the
// parent's worktree at dispatch.
func (d *Dispatcher) parentBranchBase(ctx context.Context, t gen.Task) string {
	if t.ParentTaskID == nil || *t.ParentTaskID == "" {
		return ""
	}
	parent, err := d.q.GetTask(ctx, *t.ParentTaskID)
	if err != nil || parent.Branch == "" {
		return ""
	}
	return parent.Branch
}

func (d *Dispatcher) buildTransitionHints(ctx context.Context, taskID, workflowID, fromLabel string) []TransitionHint {
	all, err := d.q.ListWorkflowTransitions(ctx, workflowID)
	if err != nil {
		slog.Warn("dispatcher: build transition hints", "component", "dispatcher", "task_id", taskID, "workflow_id", workflowID, "err", err)
		return nil
	}
	var hints []TransitionHint
	for _, t := range all {
		if t.FromLabel != fromLabel || t.Path == nil {
			continue
		}
		hints = append(hints, TransitionHint{ToLabel: t.ToLabel, Path: *t.Path})
	}
	return hints
}

// matchConfigs returns every enabled config whose labels include the task's
// label, in the order configs is given (ListAgentConfigs already sorts by
// priority ASC, created_at DESC, so the returned slice is priority order,
// newest-first tiebreak). A parse failure is logged and the config skipped.
// Multiple matches are a supported feature (dispatch failover), not a warning.
func matchConfigs(configs []gen.AgentConfig, label string) []*gen.AgentConfig {
	var matched []*gen.AgentConfig
	for i := range configs {
		cfg := &configs[i]
		if cfg.Enabled != 1 {
			continue // ponytail: defense-in-depth; ListAgentConfigs already filters enabled=1
		}
		var labels []string
		if err := json.Unmarshal([]byte(cfg.Labels), &labels); err != nil {
			slog.Error("dispatcher: skipping config with unparseable labels", "component", "dispatcher", "config_id", cfg.ID, "config_name", cfg.Name, "err", err)
			continue
		}
		for _, l := range labels {
			if l != label {
				continue
			}
			matched = append(matched, cfg)
			break
		}
	}
	if len(matched) > 1 {
		slog.Debug("dispatcher: multiple configs match label", "component", "dispatcher", "label", label, "count", len(matched))
	}
	return matched
}

// copyAttachmentsToWorktree copies attachment files into <worktreePath>/.task_attachments/
// so the agent can read them using its file-access tools.
func copyAttachmentsToWorktree(worktreePath string, absPaths []string) error {
	dst := filepath.Join(worktreePath, ".task_attachments")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, src := range absPaths {
		filename := filepath.Base(src)
		dstFile := filepath.Join(dst, filename)
		if err := copyFile(src, dstFile); err != nil {
			slog.Warn("copyAttachmentsToWorktree: skip file", "component", "dispatcher", "src", src, "err", err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck
	_, err = io.Copy(out, in)
	return err
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toAgentConfig(cfg gen.AgentConfig, pc gen.ProviderConfig) AgentConfig {
	var env map[string]string
	_ = json.Unmarshal([]byte(pc.Env), &env)
	if env == nil {
		env = map[string]string{}
	}
	var enabledPlugins []string
	_ = json.Unmarshal([]byte(cfg.EnabledPlugins), &enabledPlugins)
	var enabledMCPServers []string
	_ = json.Unmarshal([]byte(cfg.EnabledMcpServers), &enabledMCPServers)
	var commandAllowlist []string
	_ = json.Unmarshal([]byte(cfg.CommandAllowlist), &commandAllowlist)
	var commandDenylist []string
	_ = json.Unmarshal([]byte(cfg.CommandDenylist), &commandDenylist)
	return AgentConfig{
		ID:                cfg.ID,
		Name:              cfg.Name,
		Provider:          pc.Provider,
		Model:             pc.Model,
		UsagePool:         UsagePoolKey(pc.ID, pc.Provider, env),
		SystemPrompt:      cfg.SystemPrompt,
		MaxTokens:         cfg.MaxTokens,
		TimeoutSecs:       cfg.TimeoutSecs,
		MaxTurns:          cfg.MaxTurns,
		MaxRetries:        cfg.MaxRetries,
		RetryBackoffSecs:  cfg.RetryBackoffSecs,
		ResumeSessions:    cfg.ResumeSessions != 0,
		SubtasksEnabled:   cfg.SubtasksEnabled != 0,
		MaxSubtasks:       cfg.MaxSubtasks,
		MaxCostUSD:        cfg.MaxCostUsd,
		Effort:            cfg.Effort,
		PermissionMode:    cfg.PermissionMode,
		Env:               env,
		EnabledPlugins:    enabledPlugins,
		EnabledMCPServers: enabledMCPServers,
		CommandAllowlist:  commandAllowlist,
		CommandDenylist:   commandDenylist,
	}
}

// lifecycle returns the run-lifecycle helper that owns escalation (see
// escalate.go).
func (d *Dispatcher) lifecycle() runLifecycle {
	return runLifecycle{q: d.q, pub: d.Publisher}
}

// costPeriodBounds returns the half-open UTC [start, end) bounds of the
// calendar day and month containing now, formatted as the canonical
// 'YYYY-MM-DD HH:MM:SS' used by agent_runs.created_at.
func costPeriodBounds(now time.Time) (dayStart, dayEnd, monthStart, monthEnd string) {
	const layout = "2006-01-02 15:04:05"
	now = now.UTC()
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	m := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return d.Format(layout), d.AddDate(0, 0, 1).Format(layout),
		m.Format(layout), m.AddDate(0, 1, 0).Format(layout)
}
