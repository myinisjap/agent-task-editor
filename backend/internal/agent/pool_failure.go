package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// handleTransientFailure marks the run failed and either schedules a bounded,
// backed-off auto-retry of the task or — once the configured retry budget is
// exhausted — escalates to waiting_human so a human isn't left guessing
// whether the task is quietly retrying or stuck. Always clears the active
// run slot so the dispatcher can re-pick the task once eligible (either on
// the next sweep, if no retry budget was consumed, or once next_retry_at
// elapses). result carries any usage the provider accumulated before the
// transient/rate-limit error was raised, so that spend (tokens, cost_usd,
// cost_unknown) is persisted on the run row rather than recorded as $0 — a
// transient/rate-limited run has still burned real tokens and must count
// toward cost aggregates (per-task budgets, and the global daily/monthly
// spend ceiling) just like a completed or max-turns/cost-budget run does.
func (p *Pool) handleTransientFailure(ctx context.Context, job Job, result Result, reason string, startedAt time.Time) {
	bg := context.Background()

	maxRetries := job.Input.AgentConfig.MaxRetries
	task, terr := p.q.GetTask(bg, job.Input.Task.ID)
	var count int64
	if terr == nil {
		count = task.TransientRetryCount
	} else {
		slog.Warn("pool: handleTransientFailure: get task", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", terr)
	}

	finalStatus := "failed"
	var message *string

	if maxRetries > 0 && count < maxRetries {
		// Still within budget — bump the counter and schedule a backed-off retry.
		newCount := count + 1
		backoffBase := time.Duration(job.Input.AgentConfig.RetryBackoffSecs) * time.Second
		delay := BackoffDurationWithBase(int(newCount-1), backoffBase)
		nextRetryAt := time.Now().Add(delay)
		if _, err := p.q.SetTaskTransientRetry(bg, gen.SetTaskTransientRetryParams{
			TransientRetryCount: newCount,
			NextRetryAt:         &nextRetryAt,
			ID:                  job.Input.Task.ID,
		}); err != nil {
			slog.Warn("pool: set transient retry", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
		slog.Info("pool: transient failure, scheduling auto-retry", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "attempt", newCount, "max_retries", maxRetries, "next_retry_at", nextRetryAt, "reason", reason)
	} else if maxRetries > 0 {
		// Budget exhausted — escalate to waiting_human rather than retrying forever.
		// Reset the counter so a human-triggered re-dispatch starts a fresh budget.
		finalStatus = "waiting_human"
		msg := fmt.Sprintf("Agent failed after %d transient retries (%s). Manual re-dispatch or intervention required.", count, reason)
		message = &msg
		if _, err := p.q.ResetTaskTransientRetry(bg, job.Input.Task.ID); err != nil {
			slog.Warn("pool: reset transient retry (escalation)", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
		slog.Warn("pool: transient retry budget exhausted, escalating to waiting_human", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "count", count, "max_retries", maxRetries)
	} else {
		// max_retries == 0 — auto-retry disabled for this config; behaves like
		// today's plain failed-and-immediately-re-dispatchable behavior.
		if _, err := p.q.ResetTaskTransientRetry(bg, job.Input.Task.ID); err != nil {
			slog.Warn("pool: reset transient retry (disabled)", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
		slog.Info("pool: transient failure, auto-retry disabled (max_retries=0)", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "reason", reason)
	}

	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	runParams := gen.SetAgentRunCompletedParams{
		Status:       finalStatus,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		CostUnknown:  costUnknown,
		ID:           job.RunID,
	}
	if message != nil {
		runParams.Notes = message
	}
	if _, err := p.q.SetAgentRunCompleted(bg, runParams); err != nil {
		slog.Warn("pool: set run completed (transient)", "component", "pool", "run_id", job.RunID, "err", err)
	}
	metrics.RunTerminalTotal.WithLabelValues(finalStatus).Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())

	if finalStatus == "waiting_human" {
		// waiting_human intentionally stays locked (active run not cleared)
		// until a human acts, consistent with the login-error escalation path.
		if p.pub != nil {
			msg := ""
			if message != nil {
				msg = *message
			}
			p.pub.Publish("task.needs_human", map[string]any{
				"task_id": job.Input.Task.ID,
				"run_id":  job.RunID,
				"message": msg,
			})
		}
	} else {
		p.releaseLock(bg, job.Input.Task.ID, job.RunID)
	}

	if p.pub != nil {
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  finalStatus,
		})
	}
}

// handleMaxTurnsExhausted marks the run waiting_human when the agent hit its
// configured max_turns cap without finishing. Unlike handleTransientFailure,
// this never auto-retries and never touches the transient-retry budget —
// turn exhaustion is neither an infra blip nor a genuine task failure, it's
// the operator's own cap doing its job. Re-dispatching (as a plain "failed"/
// ClassGenuine run would) would silently hand the next run a fresh turn
// budget, voiding the cap. The task's active-run lock is deliberately left
// set (no ClearActiveAgentRun) so it stays locked until a human raises
// max_turns, replies, or otherwise re-dispatches — consistent with every
// other waiting_human escalation (login, exhausted retry budget, exhausted
// cost budget, rework loop).
func (p *Pool) handleMaxTurnsExhausted(ctx context.Context, job Job, result Result, mt *ErrMaxTurns, startedAt time.Time) {
	bg := context.Background()
	log := slog.With("component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID)

	msg := fmt.Sprintf("Agent hit its turn limit (%d turns) without completing. Review the work so far and re-dispatch or raise max_turns.", mt.MaxTurns)

	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	if _, err := p.q.SetAgentRunCompleted(bg, gen.SetAgentRunCompletedParams{
		Status:       "waiting_human",
		StoredInfo:   result.StoredInfo,
		Notes:        &msg,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		CostUnknown:  costUnknown,
		ID:           job.RunID,
	}); err != nil {
		log.Warn("pool: max-turns escalation: set run status", "err", err)
	}

	metrics.RunClassificationTotal.WithLabelValues(string(ClassMaxTurns)).Inc()
	metrics.RunTerminalTotal.WithLabelValues("waiting_human").Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())

	if p.pub != nil {
		p.pub.Publish("task.needs_human", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"message": msg,
		})
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  "waiting_human",
		})
	}

	log.Warn("pool: agent hit max turns, escalating to waiting_human", "max_turns", mt.MaxTurns)
}

// handleCostBudgetExceeded marks the run waiting_human when a provider's
// mid-run cost watchdog cancelled the run because projected cost crossed the
// task's effective cost budget. Modeled directly on handleMaxTurnsExhausted:
// this never auto-retries and never touches the transient-retry budget — a
// budget stop is a policy decision, not an infra blip or a genuine task
// failure. The task's active-run lock is deliberately left set (no
// ClearActiveAgentRun) so it stays locked until a human raises the budget,
// replies, or otherwise re-dispatches — consistent with every other
// waiting_human escalation (login, exhausted retry budget, exhausted turn
// budget, exhausted pre-dispatch cost budget, rework loop).
func (p *Pool) handleCostBudgetExceeded(ctx context.Context, job Job, result Result, ce *ErrCostBudgetExceeded, startedAt time.Time) {
	bg := context.Background()
	log := slog.With("component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID)

	msg := fmt.Sprintf("mid-run cost budget exceeded: $%.2f of $%.2f", ce.SpentUSD, ce.BudgetUSD)

	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	if _, err := p.q.SetAgentRunCompleted(bg, gen.SetAgentRunCompletedParams{
		Status:       "waiting_human",
		StoredInfo:   result.StoredInfo,
		Notes:        &msg,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		CostUnknown:  costUnknown,
		ID:           job.RunID,
	}); err != nil {
		log.Warn("pool: cost-budget escalation: set run status", "err", err)
	}

	metrics.RunClassificationTotal.WithLabelValues(string(ClassCostBudget)).Inc()
	metrics.RunTerminalTotal.WithLabelValues("waiting_human").Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())

	if p.pub != nil {
		p.pub.Publish("task.needs_human", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"message": msg,
		})
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  "waiting_human",
		})
	}

	log.Warn("pool: mid-run cost budget exceeded, escalating to waiting_human", "spent_usd", ce.SpentUSD, "budget_usd", ce.BudgetUSD)
}

// handleCancelled records a human-requested stop. It marks the run "cancelled"
// with a note, resets the transient-retry budget (a cancel is not a failure),
// pauses the task, and clears the active-run lock. Pausing — rather than only
// clearing the lock — is deliberate: an unpaused task on an agent-triggerable
// label would be re-dispatched on the very next sweep, restarting the run the
// human just killed. Pausing leaves the task on its label for a human to resume
// when ready. Uses context.Background throughout since the run's own context is
// already cancelled. result carries any usage the provider accumulated before
// the cancellation took effect, so that spend is persisted on the run row
// rather than recorded as $0 — a cancelled run has still burned real tokens
// and must count toward cost aggregates the same as any other terminal run.
func (p *Pool) handleCancelled(job Job, result Result, startedAt time.Time) {
	bg := context.Background()
	log := slog.With("component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID)

	note := "Run cancelled by user."
	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	if _, err := p.q.SetAgentRunCompleted(bg, gen.SetAgentRunCompletedParams{
		Status:       "cancelled",
		Notes:        &note,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		CostUnknown:  costUnknown,
		ID:           job.RunID,
	}); err != nil {
		log.Error("pool: set run cancelled", "err", err)
	}
	metrics.RunTerminalTotal.WithLabelValues("cancelled").Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())

	if _, err := p.q.ResetTaskTransientRetry(bg, job.Input.Task.ID); err != nil {
		log.Warn("pool: reset transient retry (cancel)", "err", err)
	}

	if _, err := p.q.SetTaskPaused(bg, gen.SetTaskPausedParams{Paused: 1, ID: job.Input.Task.ID}); err != nil {
		log.Warn("pool: pause task after cancel", "err", err)
	}
	p.releaseLock(bg, job.Input.Task.ID, job.RunID)

	if p.pub != nil {
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  "cancelled",
		})
		// Nudge boards (which may not be subscribed to this task) to refetch so
		// the newly-paused state is reflected without a reload.
		p.pub.Publish("task.updated", map[string]any{"id": job.Input.Task.ID})
	}

	// The agent config isn't why we stopped, so clear any rate-limit backoff.
	if p.RateLimits != nil {
		p.RateLimits.Unblock(job.Input.AgentConfig.ID)
	}

	log.Info("pool: agent run cancelled by user")
}

// hasLoginError scans the last 20 log entries for an auth/login error signal.
// The patterns live in the central classification table (errclass.go), so a
// CLI wording change is a one-line edit there rather than here.
func (p *Pool) hasLoginError(ctx context.Context, runID string) bool {
	logs, err := p.q.ListAgentLogs(ctx, runID)
	if err != nil {
		return false
	}
	// Check the last 20 entries only — login errors appear near the end.
	start := len(logs) - 20
	if start < 0 {
		start = 0
	}
	for _, l := range logs[start:] {
		if ClassifyLine(l.Content) == ClassAuth {
			return true
		}
	}
	return false
}

// resolveOutcome finds the to_label for a given outcome ("success"|"failure") from the task's current label.
// Returns empty string if no unambiguous match is found.
func (p *Pool) resolveOutcome(ctx context.Context, task Task, outcome string) string {
	log := slog.With("component", "pool", "task_id", task.ID)
	all, err := p.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		log.Error("pool: resolve outcome: list transitions", "err", err)
		return ""
	}
	var match string
	for _, t := range all {
		if t.FromLabel != task.Label {
			continue
		}
		// Only consider transitions that an agent can trigger.
		if t.TriggerType == "human" {
			continue
		}
		// nil Path means the transition fires on any outcome.
		if t.Path != nil && *t.Path != outcome && *t.Path != "either" {
			continue
		}
		if match != "" {
			log.Warn("pool: resolve outcome: ambiguous transitions", "outcome", outcome)
			return ""
		}
		match = t.ToLabel
	}
	return match
}

// failureLoopThreshold is how many times the same agent failure-path transition
// (e.g. agent-review → work) may fire in a row — with no human action and no
// success exit from the origin label in between — before the pool stops looping
// and escalates the task to a human. The failure path is otherwise unbounded:
// every transition clears the dispatch lock, so a reviewer that keeps reporting
// the same issue would re-trigger a Worker forever (see the observed 1.5h loop).
const failureLoopThreshold = 3

// failureLoopExceeded reports whether the (fromLabel → toLabel) agent transition
// about to fire has already fired failureLoopThreshold times in the recent tail
// of the task's label history — i.e. the task is stuck bouncing along the same
// rework edge. The window resets on any human-triggered transition or on any
// exit from fromLabel to a label other than toLabel (a success/progress move),
// so a task that genuinely advances and later revisits the edge starts fresh.
func (p *Pool) failureLoopExceeded(ctx context.Context, taskID, fromLabel, toLabel string, log *slog.Logger) bool {
	hist, err := p.q.ListTaskLabelHistory(ctx, taskID)
	if err != nil {
		log.Warn("pool: failure-loop check: list history", "err", err)
		return false
	}
	count := 0
	// ListTaskLabelHistory is ordered oldest-first; walk newest → oldest.
	for i := len(hist) - 1; i >= 0; i-- {
		h := hist[i]
		if h.Trigger == string(workflow.TriggerHuman) {
			break // a human touched this task — reset the loop budget
		}
		if h.FromLabel == nil || *h.FromLabel != fromLabel {
			continue // not an exit from the origin label (e.g. the work → review leg)
		}
		if h.ToLabel != toLabel {
			break // exited the origin label a different way — this loop was broken
		}
		count++
	}
	return count >= failureLoopThreshold
}

// escalateFailureLoop diverts a task stuck in a rework loop to waiting_human
// instead of firing the failure transition again. The run has already been
// persisted as `completed` with its usage; this re-writes it as waiting_human
// (preserving usage from result, including the cost_unknown flag set by
// persistRunResult — SetAgentRunCompletedParams overwrites every column
// unconditionally, so omitting a field here would silently clobber it back
// to false/0) with an explanatory note and publishes task.needs_human. The
// task's active-run lock is left as-is (this run) — the caller never cleared
// it — so the task stays locked until a human acts, mirroring the
// transient-retry and cost-budget escalations.
func (p *Pool) escalateFailureLoop(ctx context.Context, job Job, result Result, toLabel string, log *slog.Logger) {
	msg := fmt.Sprintf("Stuck in a rework loop: the %q → %q failure path fired %d times without the task clearing review. Human intervention required.", job.Input.Task.Label, toLabel, failureLoopThreshold)
	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	if _, err := p.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status:       "waiting_human",
		StoredInfo:   result.StoredInfo,
		Notes:        &msg,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		CostUnknown:  costUnknown,
		ID:           job.RunID,
	}); err != nil {
		log.Warn("pool: failure-loop escalation: set run status", "err", err)
	}
	if p.pub != nil {
		p.pub.Publish("task.needs_human", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"message": msg,
		})
	}
	log.Warn("pool: failure loop exceeded threshold, escalating to waiting_human", "from", job.Input.Task.Label, "to", toLabel, "threshold", failureLoopThreshold)
}
