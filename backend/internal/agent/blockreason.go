package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// BlockReason explains why a pickup-eligible task is not currently being
// dispatched. It is computed at read time (see BlockReasonResolver), never
// stored — the same pattern queuePositionMap uses for QueuePosition, chosen
// for the same reason: a stored column would go stale the moment a rate
// limit expires, a dependency lands, or a WIP column drains.
//
// Only the FIRST reason the dispatcher would hit is reported (see
// BlockReasonResolver.resolveOne), not the full set: reporting more would
// incorrectly imply that clearing every listed reason unblocks the task,
// when in fact clearing the first one might just expose the next.
type BlockReason struct {
	Code     string     `json:"code"`
	Message  string     `json:"message"`
	ClearsAt *time.Time `json:"clears_at"`
	Detail   any        `json:"detail"`
}

// Block reason codes. These mirror the non-dispatch paths in
// Dispatcher.dispatch/sweep and the SQL gates in ListAgentPickupTasks.
const (
	BlockPaused           = "paused"
	BlockAgentIgnore      = "agent_ignore"
	BlockDependency       = "dependency"
	BlockRetryBackoff     = "retry_backoff"
	BlockNoConfig         = "no_config"
	BlockRepoConcurrency  = "repo_concurrency"
	BlockRateLimited      = "rate_limited"
	BlockCostBudget       = "cost_budget"
	BlockCostBudgetGlobal = "cost_budget_global"
	BlockWIPLimit         = "wip_limit"
)

// BlockReasonResolver computes, at read time, the first reason each of a set
// of tasks is not currently dispatch-eligible. It shares its collaborators
// (queries, pool, rate-limit registry) with the Dispatcher and calls the
// same package-level predicate helpers the dispatcher does directly —
// matchConfigs and effectiveBudget — so those two can never drift. Its
// repo-concurrency and WIP-limit checks mirror Dispatcher.repoAtLimit and
// Dispatcher.checkWIPLimit's logic exactly (same fallback/threshold rules,
// documented inline on each) since those are methods on *Dispatcher with a
// different receiver; a change to either dispatcher-side rule should be
// mirrored here too, and the resolver's own unit tests (blockreason_test.go)
// plus the dispatcher's E2E tests pin the same behavior in both places. See
// issue #353.
//
// It never writes anything: unlike Dispatcher.checkCostBudget, which
// escalates an exhausted budget to waiting_human by creating a phantom run,
// this resolver's own checkCostBudget method only ever previews that
// comparison — see its doc comment.
type BlockReasonResolver struct {
	q          *gen.Queries
	pool       *Pool
	rateLimits *RateLimitRegistry
	dispatcher *Dispatcher
}

// NewBlockReasonResolver constructs a resolver sharing the dispatcher's
// collaborators. pool, rateLimits, and dispatcher may be nil (e.g. in
// tests); a nil pool falls back to treating repo_concurrency as never
// blocking (there is no global worker cap to compare against), a nil
// rateLimits registry is treated as "nothing is rate-limited", matching
// Dispatcher.dispatch's own nil-guard on d.RateLimits, and a nil dispatcher
// is treated as "no global cost ceiling configured" (see checkGlobalCost).
func NewBlockReasonResolver(q *gen.Queries, pool *Pool, rateLimits *RateLimitRegistry, dispatcher *Dispatcher) *BlockReasonResolver {
	return &BlockReasonResolver{q: q, pool: pool, rateLimits: rateLimits, dispatcher: dispatcher}
}

// blockSharedState is the once-per-request set of shared lookups needed to
// resolve block reasons for a whole page of tasks without N+1 queries.
type blockSharedState struct {
	configs       []gen.AgentConfig
	inUse         map[string]int64    // repo_id -> in-flight run count
	repos         map[string]gen.Repo // repo_id -> repo
	labelsByWF    map[string][]gen.WorkflowLabel
	transByWF     map[string][]gen.WorkflowTransition
	unsatBlockers map[string][]gen.ListUnsatisfiedBlockersForTasksRow // task_id -> blockers
}

// ResolveMany computes the first BlockReason for each task in tasks that
// would otherwise be a dispatch candidate, keyed by task id. Tasks that
// aren't currently dispatch-eligible in the first place (non-agent-
// triggerable label, archived, or already running) are absent from the
// map — a "not applicable" task doesn't sprout a badge. Tasks present but
// with no active block reason (i.e. genuinely next-in-line, waiting only on
// a free worker slot — see QueuePosition) are also absent.
//
// This does ONE shared-state gathering pass (configs, repo usage, repos,
// workflow labels/transitions batched per distinct workflow id, dependency
// blockers batched over the given tasks) and then resolves each task against
// it — mirroring queuePositionMap's single-query-per-request discipline. It
// deliberately does NOT call GetRepo or SumTaskCost per task; SumTaskCost is
// only ever called for tasks that survive every earlier gate and have a
// nonzero effective budget.
func (r *BlockReasonResolver) ResolveMany(ctx context.Context, tasks []gen.Task) (map[string]*BlockReason, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	state, err := r.gatherState(ctx, tasks)
	if err != nil {
		return nil, err
	}

	out := make(map[string]*BlockReason, len(tasks))
	for _, t := range tasks {
		if !r.isDispatchCandidate(t, state) {
			continue
		}
		if reason := r.resolveOne(ctx, t, state); reason != nil {
			out[t.ID] = reason
		}
	}
	return out, nil
}

// gatherState performs the shared-state pass described on ResolveMany.
func (r *BlockReasonResolver) gatherState(ctx context.Context, tasks []gen.Task) (*blockSharedState, error) {
	state := &blockSharedState{
		repos:      make(map[string]gen.Repo),
		labelsByWF: make(map[string][]gen.WorkflowLabel),
		transByWF:  make(map[string][]gen.WorkflowTransition),
	}

	configs, err := r.q.ListAgentConfigs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agent configs: %w", err)
	}
	state.configs = configs

	inUse, err := r.q.CountActiveRunsByRepo(ctx)
	if err != nil {
		return nil, fmt.Errorf("count active runs by repo: %w", err)
	}
	state.inUse = make(map[string]int64, len(inUse))
	for _, row := range inUse {
		state.inUse[row.RepoID] = row.InUse
	}

	repos, err := r.q.ListRepos(ctx)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	for _, repo := range repos {
		state.repos[repo.ID] = repo
	}

	// Batch workflow labels/transitions by distinct workflow id — the board
	// can show tasks spanning multiple workflows on one page.
	seenWF := make(map[string]bool)
	for _, t := range tasks {
		if seenWF[t.WorkflowID] {
			continue
		}
		seenWF[t.WorkflowID] = true
		labels, lerr := r.q.ListWorkflowLabels(ctx, t.WorkflowID)
		if lerr != nil {
			return nil, fmt.Errorf("list workflow labels for %q: %w", t.WorkflowID, lerr)
		}
		state.labelsByWF[t.WorkflowID] = labels
		trans, terr := r.q.ListWorkflowTransitions(ctx, t.WorkflowID)
		if terr != nil {
			return nil, fmt.Errorf("list workflow transitions for %q: %w", t.WorkflowID, terr)
		}
		state.transByWF[t.WorkflowID] = trans
	}

	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	blockers, berr := r.q.ListUnsatisfiedBlockersForTasks(ctx, ids)
	if berr != nil {
		return nil, fmt.Errorf("list unsatisfied blockers: %w", berr)
	}
	state.unsatBlockers = make(map[string][]gen.ListUnsatisfiedBlockersForTasksRow, len(blockers))
	for _, b := range blockers {
		state.unsatBlockers[b.TaskID] = append(state.unsatBlockers[b.TaskID], b)
	}

	return state, nil
}

// isDispatchCandidate reports whether t is the kind of task a block reason
// is even meaningful for: on a label with an agent/both-triggered transition
// out, not archived, and not already running. Paused is itself a reportable
// reason and is intentionally NOT excluded here.
func (r *BlockReasonResolver) isDispatchCandidate(t gen.Task, state *blockSharedState) bool {
	if t.Archived != 0 {
		return false
	}
	if t.ActiveAgentRunID != nil {
		return false
	}
	for _, tr := range state.transByWF[t.WorkflowID] {
		if tr.FromLabel == t.Label && (tr.TriggerType == "agent" || tr.TriggerType == "both") {
			return true
		}
	}
	return false
}

// resolveOne evaluates the dispatch-gating predicates for a single task, in
// order, and returns the first one that blocks it (nil if none do).
//
// The order deliberately differs from Dispatcher.dispatch's raw source-line
// order: the SQL gates in ListAgentPickupTasks (agent_ignore, dependency,
// retry_backoff — plus paused) filter a task out BEFORE the dispatch loop
// ever sees it, so in practice they are evaluated "first" from the system's
// point of view. This resolver receives tasks that may already be filtered
// out of that query, so it re-derives those gates directly. The chosen order
// — paused, cost_budget_global, agent_ignore, dependency, retry_backoff,
// no_config, repo_concurrency, rate_limited, cost_budget, wip_limit —
// surfaces the harder-to-miss / more-permanent-looking reasons first and is
// pinned by TestBlockReasonResolver_Ordering (blockreason_test.go) and the
// individual per-code tests alongside it, one per row in the table above.
// cost_budget_global is checked second (right after paused, itself the most
// fundamental/task-local gate) because — unlike every other reason here,
// which is specific to this task's label/config/repo — it applies
// identically to every dispatch-eligible task in the system at once: once
// the global daily/monthly spend ceiling trips, NO task is being dispatched,
// so it's more informative to report that immediately than to first walk
// through per-task gates that are, at that moment, moot.
func (r *BlockReasonResolver) resolveOne(ctx context.Context, t gen.Task, state *blockSharedState) *BlockReason {
	if reason := r.checkPaused(t); reason != nil {
		return reason
	}
	if reason := r.checkGlobalCostBudget(); reason != nil {
		return reason
	}
	if reason := r.checkAgentIgnore(t, state); reason != nil {
		return reason
	}
	if reason := r.checkDependency(t, state); reason != nil {
		return reason
	}
	if reason := r.checkRetryBackoff(t); reason != nil {
		return reason
	}

	matches := matchConfigs(state.configs, t.Label)
	if len(matches) == 0 {
		return &BlockReason{
			Code:    BlockNoConfig,
			Message: fmt.Sprintf("no enabled agent config matches label %q", t.Label),
			Detail:  map[string]string{"label": t.Label},
		}
	}

	if reason := r.checkRepoConcurrency(t, state); reason != nil {
		return reason
	}

	matched, reason := r.checkRateLimited(matches, t.Label, state)
	if reason != nil {
		return reason
	}

	if reason := r.checkCostBudget(ctx, t, *matched); reason != nil {
		return reason
	}

	if reason := r.checkWIPLimit(ctx, t, state); reason != nil {
		return reason
	}

	return nil
}

func (r *BlockReasonResolver) checkPaused(t gen.Task) *BlockReason {
	if t.Paused == 0 {
		return nil
	}
	return &BlockReason{
		Code:    BlockPaused,
		Message: "task is paused",
	}
}

func (r *BlockReasonResolver) checkAgentIgnore(t gen.Task, state *blockSharedState) *BlockReason {
	for _, l := range state.labelsByWF[t.WorkflowID] {
		if l.Name == t.Label && l.AgentIgnore != 0 {
			return &BlockReason{
				Code:    BlockAgentIgnore,
				Message: fmt.Sprintf("label %q is excluded from agent pickup", t.Label),
				Detail:  map[string]string{"label": t.Label},
			}
		}
	}
	return nil
}

// blockerDetail describes one still-unsatisfied blocking task, surfaced in a
// dependency BlockReason's Detail so the UI can link straight to it (see
// DependenciesPanel.tsx).
type blockerDetail struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Label  string `json:"label"`
}

func (r *BlockReasonResolver) checkDependency(t gen.Task, state *blockSharedState) *BlockReason {
	blockers := state.unsatBlockers[t.ID]
	if len(blockers) == 0 {
		return nil
	}
	details := make([]blockerDetail, len(blockers))
	for i, b := range blockers {
		details[i] = blockerDetail{TaskID: b.BlockerTaskID, Title: b.BlockerTitle, Label: b.BlockerLabel}
	}
	msg := fmt.Sprintf("blocked on %d unresolved dependenc", len(details))
	if len(details) == 1 {
		msg += "y"
	} else {
		msg += "ies"
	}
	return &BlockReason{
		Code:    BlockDependency,
		Message: msg,
		Detail:  details,
	}
}

func (r *BlockReasonResolver) checkRetryBackoff(t gen.Task) *BlockReason {
	if t.NextRetryAt == nil || !t.NextRetryAt.After(time.Now()) {
		return nil
	}
	clearsAt := *t.NextRetryAt
	return &BlockReason{
		Code:     BlockRetryBackoff,
		Message:  fmt.Sprintf("waiting to retry after a transient failure (attempt %d)", t.TransientRetryCount),
		ClearsAt: &clearsAt,
		Detail:   map[string]int64{"retry_count": t.TransientRetryCount},
	}
}

func (r *BlockReasonResolver) checkRepoConcurrency(t gen.Task, state *blockSharedState) *BlockReason {
	repo, ok := state.repos[t.RepoID]
	if !ok {
		return nil
	}
	if !r.repoAtLimit(repo, state.inUse) {
		return nil
	}
	limit := r.repoLimit(repo)
	return &BlockReason{
		Code:    BlockRepoConcurrency,
		Message: fmt.Sprintf("repo is at its concurrency limit (%d of %d slots in use)", state.inUse[repo.ID], limit),
		Detail:  map[string]int64{"limit": int64(limit), "in_use": state.inUse[repo.ID]},
	}
}

// repoAtLimit mirrors Dispatcher.repoAtLimit exactly (same fallback to the
// pool's global MaxWorkers when the repo has no override), so the two can't
// drift. A nil pool (no worker pool wired, e.g. in some tests) falls back to
// treating the limit as unbounded when the repo also has no override.
func (r *BlockReasonResolver) repoAtLimit(repo gen.Repo, inUse map[string]int64) bool {
	limit := r.repoLimit(repo)
	if limit <= 0 {
		return false
	}
	return inUse[repo.ID] >= int64(limit)
}

func (r *BlockReasonResolver) repoLimit(repo gen.Repo) int {
	if repo.MaxConcurrentRuns != nil && *repo.MaxConcurrentRuns > 0 {
		return int(*repo.MaxConcurrentRuns)
	}
	if r.pool != nil {
		return r.pool.MaxWorkers()
	}
	return 0
}

func (r *BlockReasonResolver) checkRateLimited(matches []*gen.AgentConfig, label string, state *blockSharedState) (*gen.AgentConfig, *BlockReason) {
	if r.rateLimits == nil {
		return matches[0], nil
	}
	var blockedIDs []string
	var earliest time.Time
	for _, cand := range matches {
		blocked, until := r.rateLimits.IsBlocked(cand.ID)
		if !blocked {
			return cand, nil
		}
		blockedIDs = append(blockedIDs, cand.ID)
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	reason := &BlockReason{
		Code:    BlockRateLimited,
		Message: fmt.Sprintf("all %d matching agent config(s) for label %q are rate-limited", len(blockedIDs), label),
		Detail:  map[string]any{"agent_config_ids": blockedIDs},
	}
	if !earliest.IsZero() {
		reason.ClearsAt = &earliest
	}
	return nil, reason
}

// checkGlobalCostBudget reports the shared cost_budget_global reason when
// the dispatcher's global daily/monthly spend ceiling has tripped (see
// Dispatcher.refreshGlobalCostStatus) — identical for every task, computed
// once from the dispatcher's already-cached GlobalCostStatus rather than
// re-querying SumCostForDay/SumCostForMonth per task or per request. A nil
// dispatcher (e.g. in tests that don't wire one) is treated as "no global
// ceiling configured", matching the nil-guards on pool/rateLimits above.
func (r *BlockReasonResolver) checkGlobalCostBudget() *BlockReason {
	if r.dispatcher == nil {
		return nil
	}
	status := r.dispatcher.GlobalCostStatus()
	if !status.Tripped {
		return nil
	}
	var spent, limit float64
	if status.TrippedReason == "monthly" {
		spent, limit = status.MonthlySpentUSD, status.MonthlyLimitUSD
	} else {
		spent, limit = status.DailySpentUSD, status.DailyLimitUSD
	}
	return &BlockReason{
		Code:    BlockCostBudgetGlobal,
		Message: fmt.Sprintf("global %s cost budget exhausted: $%.2f of $%.2f — all dispatch halted", status.TrippedReason, spent, limit),
		Detail: map[string]any{
			"reason":            status.TrippedReason,
			"daily_spent_usd":   status.DailySpentUSD,
			"daily_limit_usd":   status.DailyLimitUSD,
			"monthly_spent_usd": status.MonthlySpentUSD,
			"monthly_limit_usd": status.MonthlyLimitUSD,
		},
	}
}

// checkCostBudget is a READ-ONLY preview of Dispatcher.checkCostBudget's
// exhaustion/unenforceable comparison. It must never write anything (no
// phantom runs, no locking, no cost_warned flag, no publish) — see the
// BlockReasonResolver doc comment. It intentionally does not surface the
// early-warning (warnRatio) case as a block reason, since a warned task is
// still dispatched normally; only the hard-exhausted or unenforceable
// (cost-unknown) cases actually block dispatch.
func (r *BlockReasonResolver) checkCostBudget(ctx context.Context, t gen.Task, matched gen.AgentConfig) *BlockReason {
	budget := effectiveBudget(t.MaxCostUsd, matched.MaxCostUsd)
	if budget <= 0 {
		return nil
	}
	spent, err := r.q.SumTaskCost(ctx, t.ID)
	if err != nil {
		return nil
	}
	if spent < budget {
		unknownRuns, uerr := r.q.CountTaskCostUnknownRuns(ctx, t.ID)
		if uerr != nil || unknownRuns == 0 {
			return nil
		}
		return &BlockReason{
			Code:    BlockCostBudget,
			Message: fmt.Sprintf("cost budget cannot be enforced: %d run(s) have unknown cost", unknownRuns),
			Detail:  map[string]any{"spent_usd": spent, "budget_usd": budget, "unknown_runs": unknownRuns},
		}
	}
	return &BlockReason{
		Code:    BlockCostBudget,
		Message: fmt.Sprintf("budget exhausted: $%.2f of $%.2f", spent, budget),
		Detail:  map[string]any{"spent_usd": spent, "budget_usd": budget},
	}
}

// checkWIPLimit is a read-time mirror of Dispatcher.checkWIPLimit, reusing
// the same workflow.SuccessTarget helper and the already-batched labels for
// t.WorkflowID (state.labelsByWF) instead of re-querying. Only the count
// query (CountTasksByLabel) is per-task, and only for tasks that reach this
// gate with a hard-limited target label — the same scoping the dispatcher
// itself uses.
func (r *BlockReasonResolver) checkWIPLimit(ctx context.Context, t gen.Task, state *blockSharedState) *BlockReason {
	target, ok := workflow.SuccessTarget(state.transByWF[t.WorkflowID], t.Label)
	if !ok || target == "" {
		return nil
	}
	var targetLabel *gen.WorkflowLabel
	for _, l := range state.labelsByWF[t.WorkflowID] {
		if l.Name == target {
			ll := l
			targetLabel = &ll
			break
		}
	}
	if targetLabel == nil || targetLabel.WipLimitHard == 0 || targetLabel.WipLimit == nil {
		return nil
	}
	limit := *targetLabel.WipLimit
	if limit <= 0 {
		return nil
	}
	count, err := r.q.CountTasksByLabel(ctx, gen.CountTasksByLabelParams{
		WorkflowID: t.WorkflowID,
		Label:      target,
	})
	if err != nil || count < limit {
		return nil
	}
	return &BlockReason{
		Code:    BlockWIPLimit,
		Message: fmt.Sprintf("WIP limit reached for %q (%d of %d)", target, count, limit),
		Detail:  map[string]any{"target_label": target, "count": count, "limit": limit},
	}
}
