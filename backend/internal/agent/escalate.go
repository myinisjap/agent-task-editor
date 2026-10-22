package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// EscalationReason is the closed set of codes recorded on agent_runs
// (escalation_reason) whenever a run ends in waiting_human, so the UI can say
// why a human is needed and offer the right primary action instead of
// rendering every cause as "the agent is asking a question".
type EscalationReason string

const (
	// ReasonAgentQuestion: the agent called request_human to ask something.
	ReasonAgentQuestion EscalationReason = "agent_question"
	// ReasonAuthRequired: the provider CLI is not logged in.
	ReasonAuthRequired EscalationReason = "auth_required"
	// ReasonRepeatedFailure: consecutive genuine run failures hit the cap.
	ReasonRepeatedFailure EscalationReason = "repeated_failure"
	// ReasonReworkLoop: the same failure/rework transition fired repeatedly.
	ReasonReworkLoop EscalationReason = "rework_loop"
	// ReasonCostBudget: the task's cost budget is exhausted (pre- or mid-run).
	ReasonCostBudget EscalationReason = "cost_budget"
	// ReasonCostUnknown: a budget is set but a run had unpriced usage.
	ReasonCostUnknown EscalationReason = "cost_unknown"
	// ReasonProviderMissing: the agent config's provider is disabled/unknown.
	ReasonProviderMissing EscalationReason = "provider_missing"
	// ReasonRuntimePrep: installing the repo's pinned toolchains failed.
	ReasonRuntimePrep EscalationReason = "runtime_prep"
	// ReasonVerifyError: the repo/label verify command could not be run.
	ReasonVerifyError EscalationReason = "verify_error"
	// ReasonMaxTurns: the agent hit its max_turns cap.
	ReasonMaxTurns EscalationReason = "max_turns"
	// ReasonTransientExhausted: transient-failure retry budget exhausted.
	ReasonTransientExhausted EscalationReason = "transient_exhausted"
	// ReasonNoOutcome: the run finished without a usable outcome.
	ReasonNoOutcome EscalationReason = "no_outcome"
	// ReasonRejectedTransition: the agent's requested move was refused repeatedly.
	ReasonRejectedTransition EscalationReason = "rejected_transition"
)

// lockMode says what escalate does to the task's active-run lock after the
// run row is written. The lock itself is never cleared by an escalation: a
// waiting_human run intentionally keeps active_agent_run_id set (see AGENTS.md).
type lockMode int

const (
	// lockUntouched: the task is already locked on this run (a real run that
	// the dispatcher locked before submitting it).
	lockUntouched lockMode = iota
	// lockPhantomOnly: a phantom run with no logs/feedback; set only
	// active_agent_run_id so current_agent_run_id keeps pointing at the last
	// real run (issue #344).
	lockPhantomOnly
	// lockRestoreCurrent: persistRunRow already pointed both ids at this
	// phantom run; set active to it and restore current_agent_run_id to
	// escalation.CurrentRunID (the prior real run, possibly nil).
	lockRestoreCurrent
)

// escalation describes one transition of a run into waiting_human.
type escalation struct {
	TaskID  string
	RunID   string
	Reason  EscalationReason
	Message string
	// Result carries usage/stored info to preserve on the run row. Nil for
	// phantom runs that never invoked a provider.
	Result *Result
	Lock   lockMode
	// CurrentRunID is used by lockRestoreCurrent only.
	CurrentRunID *string
	// PublishDone also publishes task.agent_done after task.needs_human.
	PublishDone bool
	// FailFast stops at the first failed write and returns its error (used
	// where a half-applied phantom escalation must not lock the task).
	FailFast bool
}

// runLifecycle owns the run-status and dispatch-lock invariants shared by the
// pool and the dispatcher: how a run enters waiting_human and how its lock is
// released.
type runLifecycle struct {
	q   *gen.Queries
	pub Publisher
}

// announceNeedsHuman publishes task.needs_human with the reason code.
func (l runLifecycle) announceNeedsHuman(taskID, runID string, reason EscalationReason, msg string) {
	if l.pub == nil {
		return
	}
	l.pub.Publish(wsevent.TypeTaskNeedsHuman, wsevent.TaskNeedsHuman{
		TaskID:  taskID,
		RunID:   runID,
		Reason:  string(reason),
		Message: msg,
	})
}

// escalate marks the run waiting_human with a reason code and message,
// applies the requested lock handling, and publishes task.needs_human (and
// task.agent_done when PublishDone). Returns the first write error, if any.
func (l runLifecycle) escalate(ctx context.Context, e escalation) error {
	var firstErr error
	note := func(err error) bool {
		if err == nil {
			return false
		}
		slog.Warn("run lifecycle: escalate", "component", "lifecycle", "run_id", e.RunID, "task_id", e.TaskID, "reason", string(e.Reason), "err", err)
		if firstErr == nil {
			firstErr = err
		}
		return e.FailFast
	}

	reason := string(e.Reason)
	msg := e.Message
	params := gen.SetAgentRunCompletedParams{
		Status:           "waiting_human",
		Notes:            &msg,
		EscalationReason: &reason,
		ID:               e.RunID,
	}
	if r := e.Result; r != nil {
		params.StoredInfo = r.StoredInfo
		params.InputTokens = r.InputTokens
		params.OutputTokens = r.OutputTokens
		params.CostUsd = r.CostUSD
		params.TurnsUsed = r.TurnsUsed
		if r.CostUnknown {
			params.CostUnknown = 1
		}
	}
	if note(wrapErr("set run status", func() error { _, err := l.q.SetAgentRunCompleted(ctx, params); return err })) {
		return firstErr
	}

	// Best-effort: keep the question for the HUMAN DECISIONS prompt section.
	// Never affects escalation, locking or firstErr.
	if err := l.q.UpsertHumanQuestion(ctx, gen.UpsertHumanQuestionParams{
		RunID: e.RunID, TaskID: e.TaskID, EscalationReason: reason, Question: msg,
	}); err != nil {
		slog.Warn("run lifecycle: record human question", "component", "lifecycle", "run_id", e.RunID, "task_id", e.TaskID, "err", err)
	}

	switch e.Lock {
	case lockUntouched:
		// Already locked on this run by the dispatcher; nothing to write.
	case lockPhantomOnly:
		if note(wrapErr("lock task on run", func() error {
			return l.q.SetTaskActiveRunOnly(ctx, gen.SetTaskActiveRunOnlyParams{ActiveAgentRunID: &e.RunID, ID: e.TaskID})
		})) {
			return firstErr
		}
	case lockRestoreCurrent:
		if note(wrapErr("restore current run", func() error {
			return l.q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{CurrentAgentRunID: e.CurrentRunID, ActiveAgentRunID: &e.RunID, ID: e.TaskID})
		})) {
			return firstErr
		}
	}

	l.announceNeedsHuman(e.TaskID, e.RunID, e.Reason, e.Message)
	if e.PublishDone && l.pub != nil {
		l.pub.Publish(wsevent.TypeTaskAgentDone, wsevent.TaskAgentDone{
			TaskID: e.TaskID,
			RunID:  e.RunID,
			Status: "waiting_human",
		})
	}
	return firstErr
}

// releaseLock clears the task's active-run lock, but only if it still points
// at runID. A finished/aborted run must never wipe a lock a concurrent
// (re-)dispatch has since taken (see issue #244).
func (l runLifecycle) releaseLock(ctx context.Context, taskID, runID string) {
	n, err := l.q.ClearActiveAgentRunIfOwner(ctx, gen.ClearActiveAgentRunIfOwnerParams{
		ID:               taskID,
		ActiveAgentRunID: &runID,
	})
	if err != nil {
		slog.Warn("pool: release lock", "component", "pool", "task_id", taskID, "run_id", runID, "err", err)
		return
	}
	if n == 0 {
		slog.Warn("pool: skipped clearing dispatch lock owned by another run", "component", "pool", "task_id", taskID, "run_id", runID)
	}
}

func wrapErr(what string, f func() error) error {
	if err := f(); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}
