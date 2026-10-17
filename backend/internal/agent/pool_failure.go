package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// handleTransientFailure marks the run failed and either schedules a bounded,
// backed-off auto-retry of the task or — once the configured retry budget is
// exhausted — escalates to waiting_human so a human isn't left guessing
// whether the task is quietly retrying or stuck (max_retries=0 escalates
// immediately). Clears the active run slot only when a retry was scheduled, so
// the dispatcher re-picks the task once next_retry_at elapses; an escalation
// keeps the lock until a human acts. result carries any usage the provider accumulated before the
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
	} else {
		// Budget exhausted — or auto-retry disabled (max_retries == 0), where the
		// budget is exhausted from the start. Either way escalate to waiting_human
		// rather than releasing the lock: a released lock is re-dispatched on the
		// very next sweep, i.e. an unbounded retry loop with no backoff.
		// Reset the counter so a human-triggered re-dispatch starts a fresh budget.
		finalStatus = "waiting_human"
		var msg string
		if maxRetries > 0 {
			msg = fmt.Sprintf("Agent failed after %d transient retries (%s). Manual re-dispatch or intervention required.", count, reason)
		} else {
			msg = fmt.Sprintf("Agent hit a transient failure (%s) and auto-retry is disabled (max_retries=0). Manual re-dispatch or intervention required.", reason)
		}
		message = &msg
		if _, err := p.q.ResetTaskTransientRetry(bg, job.Input.Task.ID); err != nil {
			slog.Warn("pool: reset transient retry (escalation)", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
		slog.Warn("pool: transient retry budget exhausted, escalating to waiting_human", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "count", count, "max_retries", maxRetries)
	}

	if finalStatus == "waiting_human" {
		// waiting_human intentionally stays locked (active run not cleared)
		// until a human acts, consistent with the login-error escalation path.
		_ = p.lifecycle().escalate(bg, escalation{
			TaskID: job.Input.Task.ID, RunID: job.RunID, Reason: ReasonTransientExhausted,
			Message: *message, Result: &result, PublishDone: true,
		})
	} else {
		p.failAndRelease(bg, job, result)
	}
	metrics.RunTerminalTotal.WithLabelValues(finalStatus).Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())
}

// handleRateLimited finishes a run that hit a provider rate/usage limit. The
// caller has already blocked the agent config in RateLimits, so the
// dispatcher will not hand this config another run until the block expires;
// it falls through to the next matching config (by priority) instead, or
// leaves the task waiting with a "rate limited" block reason when none is
// free. The limit belongs to the provider account, not to the task, so the
// task's transient retry budget is left untouched: spending it here would
// escalate a perfectly healthy task to waiting_human after max_retries
// limits, even while a fallback config sat idle. The registry's growing
// per-config backoff is what bounds re-attempts. Without a registry there is
// no config block to wait on, so this falls back to the task-level budget.
func (p *Pool) handleRateLimited(ctx context.Context, job Job, result Result, reason string, startedAt time.Time) {
	if p.RateLimits == nil {
		p.handleTransientFailure(ctx, job, result, reason, startedAt)
		return
	}
	slog.Info("pool: rate limited, releasing task for another config or the block to expire", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "agent_config_id", job.Input.AgentConfig.ID, "reason", reason)
	p.failAndRelease(context.Background(), job, result)
	metrics.RunTerminalTotal.WithLabelValues("failed").Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())
}

// failAndRelease marks the run failed (persisting any usage it accrued),
// releases the task's dispatch lock and publishes task.agent_done.
func (p *Pool) failAndRelease(bg context.Context, job Job, result Result) {
	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	if _, err := p.q.SetAgentRunCompleted(bg, gen.SetAgentRunCompletedParams{
		Status:       "failed",
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		CostUnknown:  costUnknown,
		TurnsUsed:    result.TurnsUsed,
		ID:           job.RunID,
	}); err != nil {
		slog.Warn("pool: set run completed (transient)", "component", "pool", "run_id", job.RunID, "err", err)
	}
	p.releaseLock(bg, job.Input.Task.ID, job.RunID)
	if p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskAgentDone, wsevent.TaskAgentDone{
			TaskID: job.Input.Task.ID,
			RunID:  job.RunID,
			Status: "failed",
		})
	}
}

// maxConsecutiveFailures is how many genuine (non-transient) agent failures in a
// row a task may accrue before the pool escalates it to waiting_human instead of
// releasing the dispatch lock again. Without a cap every failure frees the lock
// and the next 5s sweep re-runs the task, so a persistently failing task burns
// runs (and money) indefinitely. The counter (tasks.consecutive_failure_count)
// is reset by a completed run, any label transition, and a human reply/rerun.
const maxConsecutiveFailures = 3

// escalateNoOutcome handles a run that completed but reported no usable
// outcome. It rewrites the run as waiting_human (preserving usage, as
// escalateFailureLoop does), leaves the dispatch lock set so the task is not
// re-dispatched, and publishes task.needs_human exactly once.
func (p *Pool) escalateNoOutcome(ctx context.Context, job Job, result Result, log *slog.Logger) {
	msg := "Agent finished without reporting an outcome (no signal_complete call or OUTCOME: line), so the task could not be advanced. Review the work and move the task manually or re-dispatch."
	_ = p.lifecycle().escalate(ctx, escalation{
		TaskID: job.Input.Task.ID, RunID: job.RunID, Reason: ReasonNoOutcome, Message: msg, Result: &result,
	})
	log.Warn("pool: run completed without a resolvable outcome, escalating to waiting_human")
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

	_ = p.lifecycle().escalate(bg, escalation{
		TaskID: job.Input.Task.ID, RunID: job.RunID, Reason: ReasonMaxTurns, Message: msg, Result: &result, PublishDone: true,
	})

	metrics.RunClassificationTotal.WithLabelValues(string(ClassMaxTurns)).Inc()
	metrics.RunTerminalTotal.WithLabelValues("waiting_human").Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())

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

	_ = p.lifecycle().escalate(bg, escalation{
		TaskID: job.Input.Task.ID, RunID: job.RunID, Reason: ReasonCostBudget, Message: msg, Result: &result, PublishDone: true,
	})

	metrics.RunClassificationTotal.WithLabelValues(string(ClassCostBudget)).Inc()
	metrics.RunTerminalTotal.WithLabelValues("waiting_human").Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())

	log.Warn("pool: mid-run cost budget exceeded, escalating to waiting_human", "spent_usd", ce.SpentUSD, "budget_usd", ce.BudgetUSD)
}

// handleCancelled records a cancelled run (see CancelReason for why one might
// happen). It marks the run "cancelled" with a reason-specific note, resets
// the transient-retry budget (a cancel is not a failure), clears the
// active-run lock, and — for CancelReasonUser only — pauses the task.
// Pausing on a human stop is deliberate: an unpaused task on an
// agent-triggerable label would be re-dispatched on the very next sweep,
// restarting the run the human just killed; pausing leaves the task on its
// label for a human to resume when ready. A resource-constraint kill is not
// the task's fault, so it stays unpaused and is re-dispatched normally, the
// same as any other transient failure. Uses context.Background throughout
// since the run's own context is already cancelled. result carries any usage
// the provider accumulated before the cancellation took effect, so that
// spend is persisted on the run row rather than recorded as $0 — a cancelled
// run has still burned real tokens and must count toward cost aggregates the
// same as any other terminal run.
func (p *Pool) handleCancelled(job Job, result Result, startedAt time.Time, reason CancelReason) {
	bg := context.Background()
	log := slog.With("component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID)

	note := reason.note()
	status := "cancelled"
	if reason == CancelReasonShutdown {
		status = "interrupted"
	}
	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	if _, err := p.q.SetAgentRunCompleted(bg, gen.SetAgentRunCompletedParams{
		Status:       status,
		Notes:        &note,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		CostUnknown:  costUnknown,
		TurnsUsed:    result.TurnsUsed,
		ID:           job.RunID,
	}); err != nil {
		log.Error("pool: set run cancelled", "err", err)
	}
	metrics.RunTerminalTotal.WithLabelValues(status).Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())

	// An interrupted run leaves every retry/failure counter exactly as it was:
	// the process going away says nothing about the task or the infra.
	if reason != CancelReasonShutdown {
		if _, err := p.q.ResetTaskTransientRetry(bg, job.Input.Task.ID); err != nil {
			log.Warn("pool: reset transient retry (cancel)", "err", err)
		}
	}

	if reason == CancelReasonUser {
		if _, err := p.q.SetTaskPaused(bg, gen.SetTaskPausedParams{Paused: 1, ID: job.Input.Task.ID}); err != nil {
			log.Warn("pool: pause task after cancel", "err", err)
		}
	}
	p.releaseLock(bg, job.Input.Task.ID, job.RunID)

	if p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskAgentDone, wsevent.TaskAgentDone{
			TaskID: job.Input.Task.ID,
			RunID:  job.RunID,
			Status: status,
		})
		// Nudge boards (which may not be subscribed to this task) to refetch so
		// the newly-paused state is reflected without a reload.
		p.pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: job.Input.Task.ID})
	}

	// The agent config isn't why we stopped, so clear any rate-limit backoff
	// (except on shutdown, which says nothing about the config either way).
	// Conditional on no newer 429 having landed since this run started (see
	// issue #344) so a concurrently-finishing sibling run's fresh block isn't
	// clobbered.
	if p.RateLimits != nil && reason != CancelReasonShutdown {
		p.RateLimits.UnblockIfNotBlockedSince(job.Input.AgentConfig.ID, startedAt)
	}

	log.Info("pool: agent run cancelled", "reason", reason, "status", status)
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

// isPreStreamFailure reports whether a genuine provider error fired before the
// run produced any evidence of actually starting: no persisted logs, no
// session id, and zero token usage. Checked via ListAgentLogs the same way
// hasLoginError is — that's the definitive "nothing streamed" signal — combined
// with the zero-usage/empty-session checks to be safe.
func (p *Pool) isPreStreamFailure(ctx context.Context, job Job, result Result) bool {
	if result.SessionID != "" || result.InputTokens != 0 || result.OutputTokens != 0 {
		return false
	}
	logs, err := p.q.ListAgentLogs(ctx, job.RunID)
	if err != nil {
		return false
	}
	return len(logs) == 0
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
	_ = p.lifecycle().escalate(ctx, escalation{
		TaskID: job.Input.Task.ID, RunID: job.RunID, Reason: ReasonReworkLoop, Message: msg, Result: &result,
	})
	log.Warn("pool: failure loop exceeded threshold, escalating to waiting_human", "from", job.Input.Task.Label, "to", toLabel, "threshold", failureLoopThreshold)
}

// handleRejectedTransition handles a completed run whose agent-requested
// transition to toLabel was refused by the workflow engine (WIP limit, guard,
// missing transition, ...). The task stays on an agent-triggerable label, so
// simply releasing the lock would make the dispatcher re-run the agent on the
// next sweep, forever. A genuine rejection therefore counts as a failed attempt
// against the same consecutive-failure counter genuine run failures use; once
// maxConsecutiveFailures is reached the run is escalated to waiting_human
// (lock kept). Below the threshold the lock is released so the task can retry,
// e.g. after a WIP slot frees.
//
// A concurrent move is not a rejection: if the engine reported ErrStale or
// ErrTaskNotFound, or the task is no longer on the run's label (a human or
// another run moved it), the lock is released without counting or escalating.
func (p *Pool) handleRejectedTransition(ctx context.Context, job Job, result Result, toLabel string, transErr error, log *slog.Logger) {
	taskID := job.Input.Task.ID
	if errors.Is(transErr, workflow.ErrStale) || errors.Is(transErr, workflow.ErrTaskNotFound) {
		p.releaseLock(ctx, taskID, job.RunID)
		return
	}
	cur, err := p.q.GetTask(ctx, taskID)
	if err != nil || cur.Label != job.Input.Task.Label {
		// Task gone or moved by someone else: nothing to count.
		p.releaseLock(ctx, taskID, job.RunID)
		return
	}
	n, err := p.q.IncrementTaskFailureCount(ctx, taskID)
	if err != nil {
		log.Warn("pool: increment failure count (rejected transition)", "err", err)
		p.releaseLock(ctx, taskID, job.RunID)
		return
	}
	if n < maxConsecutiveFailures {
		p.releaseLock(ctx, taskID, job.RunID)
		return
	}
	msg := fmt.Sprintf("Agent asked to move to %q but the move was rejected: %v. This happened %d times in a row. Resolve the blocker (e.g. free a WIP slot) or move the task manually.", toLabel, transErr, n)
	_ = p.lifecycle().escalate(ctx, escalation{
		TaskID: taskID, RunID: job.RunID, Reason: ReasonRejectedTransition, Message: msg, Result: &result,
	})
	log.Warn("pool: rejected transition limit reached, escalating to waiting_human", "to", toLabel, "count", n, "limit", maxConsecutiveFailures)
}
