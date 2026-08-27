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
		d.dispatch(ctx, t, configs, inUse)
	}
}

// repoInUseCounts returns the current number of in-flight agent runs per
// repo (see CountActiveRunsByRepo), keyed by repo_id.
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

func (d *Dispatcher) dispatch(ctx context.Context, t gen.Task, configs []gen.AgentConfig, inUse map[string]int64) {
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
	// query); the first config that isn't rate-limit-blocked wins. This turns
	// "primary config ran out of usage credits" into automatic failover to a
	// backup config sharing the same label.
	var matched *gen.AgentConfig
	for _, cand := range matches {
		if d.RateLimits != nil {
			if blocked, until := d.RateLimits.IsBlocked(cand.ID); blocked {
				log.Info("dispatcher: skipping rate-limited config, trying next", "agent_config_id", cand.ID, "unblocked_at", until)
				continue
			}
		}
		matched = cand
		break
	}
	if matched == nil {
		log.Info("dispatcher: all matching configs rate-limited, skipping dispatch", "label", t.Label)
		return
	}

	// Cost-budget guard: only gates the sweep path. DispatchReply (a human
	// actively replying/intervening) is intentionally never budget-gated.
	if blocked, err := d.checkCostBudget(ctx, t, *matched); err != nil {
		log.Error("dispatcher: cost budget check", "err", err)
	} else if blocked {
		return
	}

	// WIP backpressure: only gates the sweep path, same as the cost-budget
	// guard above. A hard-limited label that is already full simply isn't
	// dispatched into this sweep — the task stays on its current label and is
	// re-evaluated next sweep. This never blocks a run that is already in
	// flight from completing into a full column (that stays soft/visual).
	if blocked, err := d.checkWIPLimit(ctx, t); err != nil {
		log.Error("dispatcher: wip limit check", "err", err)
	} else if blocked {
		return
	}

	if _, err := d.startRun(ctx, t, *matched, runOptions{}); err != nil {
		var transientErr transientErr
		log.Error("dispatcher: start run", "err", err, "transient", errors.As(err, &transientErr))
		return
	}
	// Reflect this dispatch in the sweep-scoped in-use map immediately so a
	// repo hitting its limit mid-sweep is skipped for the remaining tasks in
	// this same sweep, not just re-evaluated on the next tick.
	inUse[repo.ID]++
}

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
			return d.escalateCostBudget(ctx, t, matched, msg, "cost-unknown")
		}

		warnRatio := d.resolveCostWarnRatio(ctx)
		if t.CostWarned == 0 && warnRatio > 0 && spent >= warnRatio*budget {
			log.Warn("dispatcher: cost budget warning threshold crossed", "spent", spent, "budget", budget, "warn_ratio", warnRatio)
			if err := d.q.SetTaskCostWarned(ctx, t.ID); err != nil {
				log.Warn("dispatcher: set task cost_warned", "err", err)
			}
			if d.Publisher != nil {
				d.Publisher.Publish("task.cost_warning", map[string]any{
					"task_id":    t.ID,
					"spent_usd":  spent,
					"budget_usd": budget,
				})
			}
		}
		return false, nil
	}
	msg := fmt.Sprintf("budget exhausted: $%.2f of $%.2f", spent, budget)
	log.Warn("dispatcher: skipping dispatch, cost budget exhausted", "spent", spent, "budget", budget)
	return d.escalateCostBudget(ctx, t, matched, msg, "budget-exhausted")
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
func (d *Dispatcher) escalateCostBudget(ctx context.Context, t gen.Task, matched gen.AgentConfig, msg, reason string) (bool, error) {
	runID := uuid.NewString()
	if _, err := d.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:            runID,
		TaskID:        t.ID,
		AgentConfigID: &matched.ID,
	}); err != nil {
		return true, fmt.Errorf("create %s run: %w", reason, err)
	}
	if _, err := d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "waiting_human",
		Notes:  &msg,
		ID:     runID,
	}); err != nil {
		return true, fmt.Errorf("set %s run status: %w", reason, err)
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
	if err := d.q.SetTaskActiveRunOnly(ctx, gen.SetTaskActiveRunOnlyParams{
		ActiveAgentRunID: &runID,
		ID:               t.ID,
	}); err != nil {
		return true, fmt.Errorf("lock task on %s run: %w", reason, err)
	}

	if d.Publisher != nil {
		d.Publisher.Publish("task.needs_human", map[string]any{
			"task_id": t.ID,
			"run_id":  runID,
			"message": msg,
		})
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
	day := now.Format("2006-01-02")
	month := now.Format("2006-01")

	var dailySpent, monthlySpent float64
	if d.MaxDailyCostUSD > 0 {
		var err error
		dailySpent, err = d.q.SumCostForDay(ctx, day)
		if err != nil {
			slog.Error("dispatcher: sum cost for day", "component", "dispatcher", "err", err)
		}
	}
	if d.MaxMonthlyCostUSD > 0 {
		var err error
		monthlySpent, err = d.q.SumCostForMonth(ctx, month)
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
			d.Publisher.Publish("system.cost_budget_tripped", map[string]any{
				"reason":            status.TrippedReason,
				"message":           msg,
				"daily_spent_usd":   status.DailySpentUSD,
				"daily_limit_usd":   status.DailyLimitUSD,
				"monthly_spent_usd": status.MonthlySpentUSD,
				"monthly_limit_usd": status.MonthlyLimitUSD,
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

	return d.startRun(ctx, t, *matched, runOptions{humanReply: &message})
}

// runOptions carries the extras a non-sweep dispatch (currently only the
// human-reply flow) layers on top of a standard run.
type runOptions struct {
	humanReply *string
}

// startRun provisions the task's worktree if needed, creates the run row,
// marks it as the task's active run, and submits the job to the pool. Shared
// by the sweep dispatch path and DispatchReply. It reads as a sequence of
// phases, each delegated to a helper below.
func (d *Dispatcher) startRun(ctx context.Context, t gen.Task, matched gen.AgentConfig, opts runOptions) (string, error) {
	log := slog.With("component", "dispatcher", "task_id", t.ID)

	repo, err := d.q.GetRepo(ctx, t.RepoID)
	if err != nil {
		return "", fmt.Errorf("get repo: %w", err)
	}

	workDir, err := d.ensureWorktree(ctx, t, repo)
	if err != nil {
		return "", err
	}

	runID := uuid.NewString()
	log = log.With("run_id", runID)

	agentCfg, resumeSessionID, err := d.resolveAgentConfig(ctx, t, matched)
	if err != nil {
		return "", err
	}

	var feedback *string
	if t.CurrentAgentRunID != nil {
		prior, _ := d.q.GetAgentRun(ctx, *t.CurrentAgentRunID)
		feedback = prior.Feedback
	}

	if err := d.persistRunRow(ctx, t, matched, runID, feedback); err != nil {
		return "", err
	}

	runtimeSpec, err := d.prepareRuntime(ctx, repo, workDir)
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
	reviewComments := d.loadReviewComments(ctx, t.ID, log)
	sourceComments := d.loadSourceComments(ctx, t.ID, log)

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
		msg := fmt.Sprintf("agent config's provider is disabled or unknown: %q", agentCfg.Provider)
		log.Error("dispatcher: no runner for provider", "provider", agentCfg.Provider)
		if _, err := d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
			Status: "waiting_human",
			Notes:  &msg,
			ID:     runID,
		}); err != nil {
			log.Warn("dispatcher: mark nil-provider run waiting_human", "err", err)
		}
		// persistRunRow (above) already set BOTH current_agent_run_id and
		// active_agent_run_id to this phantom run before the provider was
		// resolved. This run has no logs and no feedback, so — same reasoning
		// as escalateCostBudget — restore current_agent_run_id to the prior
		// real run (t.CurrentAgentRunID, captured before persistRunRow ran) so
		// WS replay and the next dispatch's feedback lookup don't hit this
		// phantom row. active_agent_run_id is deliberately left pointing at
		// runID: it still needs to hold the re-dispatch lock until a human
		// fixes the config. See issue #344.
		if err := d.q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
			CurrentAgentRunID: t.CurrentAgentRunID, // may be nil if this was the task's first run - that's correct
			ActiveAgentRunID:  &runID,
			ID:                t.ID,
		}); err != nil {
			log.Warn("dispatcher: restore current_agent_run_id after nil-provider escalation", "err", err)
		}
		if d.Publisher != nil {
			d.Publisher.Publish("task.needs_human", map[string]any{
				"task_id": t.ID,
				"run_id":  runID,
				"message": msg,
			})
		}
		return "", fmt.Errorf("%w: %q", ErrProviderUnavailable, agentCfg.Provider)
	}

	// If this is a parent with subtasks that conflicted on merge-back, hand the
	// work agent the conflict context so it resolves the merges on this branch.
	var subtaskConflicts *string
	if d.Subtasks != nil {
		subtaskConflicts = d.Subtasks.BuildConflictContext(ctx, t.ID)
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
		RunID:    runID,
		Provider: provider,
		Input: RunInput{
			RunID:              runID,
			Task:               Task{ID: t.ID, Title: t.Title, Description: t.Description, Type: t.Type, Label: t.Label, WorkflowID: t.WorkflowID, AgentNotes: t.AgentNotes, Branch: t.Branch, ParentID: derefStr(t.ParentTaskID), RepoPath: repo.Path, Attachments: attachmentRels},
			AgentConfig:        agentCfg,
			RepoPath:           workDir,
			RepoRemoteURL:      derefStr(repo.RemoteUrl),
			Transitions:        transitions,
			Feedback:           feedback,
			PriorPlan:          agentNotes,
			OpenReviewComments: reviewComments,
			SourceComments:     sourceComments,
			AttachmentAbsPaths: attachmentAbsPaths,
			ResumeSessionID:    resumeSessionID,
			HumanReply:         opts.humanReply,
			SubtaskConflicts:   subtaskConflicts,
			CostBudgetUSD:      costBudgetUSD,
			CostSpentUSD:       costSpentUSD,
			CostWarnRatio:      d.resolveCostWarnRatio(ctx),
			Runtime:            runtimeSpec,
		},
	})
	if !enqueued {
		_, _ = d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
			Status: "failed",
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

// prepareRuntime resolves and prepares the repo's pinned toolchains (repos.
// runtime_languages) for this run, ahead of the provider being invoked. A
// repo with no runtime configured (the column empty) returns (nil, nil) —
// this is the §4.1 byte-identical guarantee: prepareRuntime must not run
// mise/uv or touch the worktree at all in that case, so every downstream
// caller (providers.applyRuntime) sees the same nil RuntimeSpec it always
// has and spawns exactly as before this feature existed.
//
// When pins are configured, runtime.Prep installs every pin via `mise
// install` and, for a python pin, creates worktreeDir/.venv from the
// mise-installed interpreter (skipped if .venv already exists — a re-run on
// the same worktree reuses it). Any failure here is returned as-is; the
// caller (startRun) is responsible for escalating to waiting_human rather
// than falling back to a plain spawn (see escalateRuntimePrepFailure).
func (d *Dispatcher) prepareRuntime(ctx context.Context, repo gen.Repo, worktreeDir string) (*RuntimeSpec, error) {
	pins, err := runtime.ParsePins(repo.RuntimeLanguages)
	if err != nil {
		// Persisted config should already be valid (the API validates on
		// write), but treat corrupt/invalid stored config the same as any
		// other prep failure rather than crashing the sweep.
		return nil, fmt.Errorf("parse repo runtime_languages: %w", err)
	}
	if len(pins) == 0 {
		return nil, nil
	}

	if err := runtime.Prep(ctx, pins, worktreeDir); err != nil {
		return nil, err
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
	if _, err := d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "waiting_human",
		Notes:  &msg,
		ID:     runID,
	}); err != nil {
		log.Warn("dispatcher: mark runtime-prep-failed run waiting_human", "err", err)
	}
	if err := d.q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: t.CurrentAgentRunID, // may be nil if this was the task's first run - that's correct
		ActiveAgentRunID:  &runID,
		ID:                t.ID,
	}); err != nil {
		log.Warn("dispatcher: restore current_agent_run_id after runtime-prep escalation", "err", err)
	}
	if d.Publisher != nil {
		d.Publisher.Publish("task.needs_human", map[string]any{
			"task_id": t.ID,
			"run_id":  runID,
			"message": msg,
		})
	}
	return fmt.Errorf("%w: %v", ErrRuntimePrepFailed, prepErr)
}

// ensureWorktree returns the task's working directory, provisioning a git
// worktree on first dispatch and reusing it across re-runs. Each task works in
// its own worktree on its own branch so concurrent agents on the same repo don't
// conflict. A subtask's branch is cut from its parent's branch (not the repo
// base) so its work merges back cleanly.
//
// t.WorktreePath can be stale — pointing at a directory that no longer exists
// — for a task that was archived (worktree reclaimed; see
// api/handlers.reclaimWorktreeOnArchive) and later unarchived, or one whose
// worktree the periodic sweeper reclaimed out from under it (see
// internal/worktreesweep, which never touches the DB row). Treating a
// missing directory the same as an empty WorktreePath reprovisions it here
// rather than handing the agent a nonexistent cwd.
func (d *Dispatcher) ensureWorktree(ctx context.Context, t gen.Task, repo gen.Repo) (string, error) {
	if workDir := t.WorktreePath; workDir != "" {
		if fi, statErr := os.Stat(workDir); statErr == nil && fi.IsDir() {
			return workDir, nil
		}
		slog.Warn("dispatcher: task's recorded worktree is missing; reprovisioning", "task_id", t.ID, "worktree_path", workDir)
	}

	// parentBranchBase does a DB read; resolve it before taking the repo git
	// lock so a slow DB doesn't hold the lock.
	base := d.parentBranchBase(ctx, t)

	var wtPath, branch, baseRef string
	var perr error
	// provisionWorktree(From) runs `git fetch --prune` and `git worktree add
	// -b`, both of which mutate the repo's shared ref store. Serialize against
	// sibling worktrees' commits/merges/branch-deletes/worktree-adds via the
	// per-repo lock (see worktree.go's repoGitLocks doc comment / issue #344).
	// The lock is a plain, non-reentrant mutex — never taken inside
	// provisionWorktree itself, since subtasks.go already holds it around
	// calls to provisionWorktree/RemoveWorktree.
	lock := RepoGitLock(repo.Path)
	lock.Lock()
	if base != "" {
		wtPath, branch, baseRef, perr = provisionWorktreeFrom(ctx, repo.Path, t.ID, t.Title, base)
	} else {
		wtPath, branch, baseRef, perr = provisionWorktree(ctx, repo.Path, t.ID, t.Title)
	}
	lock.Unlock()
	if perr != nil {
		return "", fmt.Errorf("provision worktree: %w", perr)
	}
	if err := d.q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{
		Branch:       branch,
		WorktreePath: wtPath,
		BaseRef:      baseRef,
		ID:           t.ID,
	}); err != nil {
		return "", fmt.Errorf("persist worktree: %w", err)
	}
	return wtPath, nil
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
func (d *Dispatcher) resolveAgentConfig(ctx context.Context, t gen.Task, matched gen.AgentConfig) (AgentConfig, string, error) {
	pc, err := d.q.GetProviderConfig(ctx, matched.ProviderConfigID)
	if err != nil {
		return AgentConfig{}, "", fmt.Errorf("get provider config: %w", err)
	}
	agentCfg := toAgentConfig(matched, pc)

	var resumeSessionID string
	if agentCfg.ResumeSessions && providerSupportsResume(agentCfg.Provider) {
		if sid, serr := d.q.GetLatestTaskSession(ctx, gen.GetLatestTaskSessionParams{
			TaskID:        t.ID,
			AgentConfigID: &matched.ID,
		}); serr == nil && sid != "" {
			resumeSessionID = sid
		}
	}
	return agentCfg, resumeSessionID, nil
}

// persistRunRow creates the run row and marks the task's active run in a single
// transaction. These two writes must be atomic: if the run were created but the
// task never pointed at it (a crash or error between the statements), an orphaned
// 'pending' run would linger with nothing gating re-dispatch. Committing them
// together means either both land or neither does.
func (d *Dispatcher) persistRunRow(ctx context.Context, t gen.Task, matched gen.AgentConfig, runID string, feedback *string) error {
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
	// Mark the task's active run so the next sweep skips it.
	if err := tq.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID,
		ActiveAgentRunID:  &runID,
		ID:                t.ID,
	}); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("set task active run: %w", err)
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
		Env:               env,
		EnabledPlugins:    enabledPlugins,
		EnabledMCPServers: enabledMCPServers,
		CommandAllowlist:  commandAllowlist,
		CommandDenylist:   commandDenylist,
	}
}
