package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ErrRunLive is returned (and rendered as a 409 by the API) when a
// human-equivalent transition is attempted while the task's active agent run is
// still live (see RunLiveStatuses).
var ErrRunLive = errors.New("a run is currently active on this task; cancel the run before moving its label")

// ErrNoHumanPath is returned when the current label has no human transition
// with the requested path.
var ErrNoHumanPath = errors.New("no human transition")

// RunLiveStatuses are the agent-run statuses during which the task's
// active-run lock is held by a run that is either executing or about to be
// (i.e. still eligible to execute) and must not be cleared out from under it
// by a human transition. This is deliberately broader than just "running":
// a run claims the lock as soon as it is dispatched, while its status is still
// "pending"; a human transition during that window is just as unsafe as one
// during "running" (issue #244). "waiting_human" is intentionally excluded: it
// is the parked state a run sits in awaiting exactly this kind of human action.
var RunLiveStatuses = map[string]bool{
	"pending": true,
	"running": true,
}

// RunIsLive reports whether task's active agent run (if any) is in a status
// (see RunLiveStatuses) during which clearing active_agent_run_id would race a
// live run. A missing active run, or one that can't be read, counts as not
// live. Without this guard a human transition clears active_agent_run_id while
// the run is still live; the dispatcher then starts a second run and two
// agents end up sharing one worktree (issue #244, ADR 0003).
func (e *Engine) RunIsLive(ctx context.Context, task gen.Task) bool {
	if task.ActiveAgentRunID == nil {
		return false
	}
	run, err := e.sq.GetAgentRun(ctx, *task.ActiveAgentRunID)
	if err != nil {
		return false
	}
	return RunLiveStatuses[run.Status]
}

// HumanPathTarget returns the destination label of the human transition with
// the given path (e.g. "success" or "failure") from fromLabel. The error wraps
// ErrNoHumanPath.
func HumanPathTarget(transitions []gen.WorkflowTransition, fromLabel, path string) (string, error) {
	for _, t := range transitions {
		if t.FromLabel == fromLabel && t.TriggerType == "human" && t.Path != nil && *t.Path == path {
			return t.ToLabel, nil
		}
	}
	return "", &noHumanPathError{path: path, from: fromLabel}
}

type noHumanPathError struct{ path, from string }

func (e *noHumanPathError) Error() string {
	return fmt.Sprintf("no %q human transition defined from %q", e.path, e.from)
}

func (e *noHumanPathError) Is(target error) bool { return target == ErrNoHumanPath }

// ApproveOptions parameterises Engine.Approve.
type ApproveOptions struct {
	// Actor is recorded as the history row's actor_id.
	Actor string
	// Note is recorded as the history row's note.
	Note string
	// ExpectedFrom, when non-empty, makes Approve fail with ErrStale if the
	// task is no longer on that label.
	ExpectedFrom string
}

// Approve follows the "success" human transition from the task's current
// label. It is the single approve code path shared by POST /tasks/{id}/approve
// and the autonomy policy, so create_pr targets, side effects, events and
// label history behave identically. Order of refusals: unknown task
// (ErrTaskNotFound), stale label (ErrStale), no path (ErrNoHumanPath), live run
// (ErrRunLive). It returns the destination label.
func (e *Engine) Approve(ctx context.Context, taskID string, o ApproveOptions) (string, error) {
	task, err := e.q.GetTask(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if o.ExpectedFrom != "" && task.Label != o.ExpectedFrom {
		return "", fmt.Errorf("%w: task is on %q, expected %q", ErrStale, task.Label, o.ExpectedFrom)
	}
	transitions, err := e.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		return "", fmt.Errorf("failed to load workflow transitions")
	}
	target, err := HumanPathTarget(transitions, task.Label, "success")
	if err != nil {
		return "", err
	}
	if e.RunIsLive(ctx, task) {
		return "", ErrRunLive
	}
	if err := e.TransitionExpecting(ctx, taskID, target, TriggerHuman, o.Actor, o.Note, o.ExpectedFrom); err != nil {
		return "", err
	}
	return target, nil
}

// MoveBack moves a task to toLabel as a human move, bypassing the transition
// table, and records note verbatim (no ForcedNotePrefix). Used by autonomy
// undo. Callers must refuse it while a run is live (RunIsLive).
func (e *Engine) MoveBack(ctx context.Context, taskID, toLabel, actorID, note, expectedFrom string) error {
	return e.transition(ctx, taskID, toLabel, TriggerHuman, actorID, note, expectedFrom, true)
}
