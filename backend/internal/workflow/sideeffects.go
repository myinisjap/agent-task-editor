package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Side effect kinds recorded in task_side_effects.kind.
const (
	// SideEffectTerminal pushes the task's branch and tears down its worktree
	// (or merges a subtask back into its parent) after reaching a terminal label.
	SideEffectTerminal = "terminal"
	// SideEffectCreatePR pushes the branch and opens a pull request after the
	// task reaches a create_pr label.
	SideEffectCreatePR = "create_pr"
	// SideEffectLeaveNotReady applies the in-progress signal to the source issue
	// after the task leaves the human-gate label.
	SideEffectLeaveNotReady = "leave_not_ready"
)

// Side effect statuses. A finished effect has no row, so "done" is absent.
const (
	// SideEffectPending is waiting for its next_at to come due.
	SideEffectPending = "pending"
	// SideEffectRunning is claimed by the worker right now.
	SideEffectRunning = "running"
	// SideEffectFailed exhausted its attempts; it is retried only manually.
	SideEffectFailed = "failed"
)

const (
	// MaxSideEffectAttempts is how many times an effect is tried before it is
	// parked as failed.
	MaxSideEffectAttempts = 8
	// DefaultSideEffectTimeout bounds one execution of a hook.
	DefaultSideEffectTimeout = 10 * time.Minute

	sideEffectPollInterval = 5 * time.Second
	sideEffectBatch        = 20
)

// DefaultSideEffectBackoff is the exponential retry delay after the given
// number of failed attempts: 15s, 30s, 1m, 2m, ... capped at 10 minutes.
func DefaultSideEffectBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := 15 * time.Second
	for i := 1; i < attempts && d < 10*time.Minute; i++ {
		d *= 2
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

func (e *Engine) nudgeWorker() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// RunSideEffects drains the side-effect outbox until ctx is cancelled: it
// recovers rows left running by a previous process, then runs due rows each
// time it is nudged by a transition or every few seconds. It uses ctx (never a
// request context) as the parent of every hook call, so cancelling ctx on
// shutdown stops it cleanly; an interrupted effect is put back as pending
// without counting as an attempt.
func (e *Engine) RunSideEffects(ctx context.Context) {
	if n, err := e.sq.ResetRunningTaskSideEffects(ctx); err != nil {
		slog.Warn("side effects: reset running rows", "err", err)
	} else if n > 0 {
		slog.Info("side effects: recovered interrupted effects", "count", n)
	}
	t := time.NewTicker(sideEffectPollInterval)
	defer t.Stop()
	for {
		e.DrainSideEffects(ctx)
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		case <-t.C:
		}
	}
}

// DrainSideEffects runs every currently due side effect once, sequentially, and
// returns how many it executed. Effects that fail are rescheduled with backoff
// and are not run again by this call. RunSideEffects calls it in a loop; tests
// call it directly to run the outbox synchronously.
func (e *Engine) DrainSideEffects(ctx context.Context) int {
	ran := 0
	seen := map[string]bool{}
	for ctx.Err() == nil {
		due, err := e.sq.ListDueTaskSideEffects(ctx, gen.ListDueTaskSideEffectsParams{NextAt: time.Now(), Limit: sideEffectBatch})
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("side effects: list due", "err", err)
			}
			return ran
		}
		progressed := false
		for _, se := range due {
			if ctx.Err() != nil {
				return ran
			}
			if seen[se.ID] {
				continue
			}
			seen[se.ID] = true
			if n, err := e.sq.ClaimTaskSideEffect(ctx, se.ID); err != nil || n == 0 {
				continue
			}
			progressed = true
			ran++
			e.runSideEffect(ctx, se)
		}
		if !progressed {
			return ran
		}
	}
	return ran
}

func (e *Engine) runSideEffect(ctx context.Context, se gen.TaskSideEffect) {
	timeout := e.SideEffectTimeout
	if timeout <= 0 {
		timeout = DefaultSideEffectTimeout
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	err := e.execSideEffect(rctx, se)
	cancel()

	if err == nil {
		if cerr := e.sq.CompleteTaskSideEffect(context.WithoutCancel(ctx), se.ID); cerr != nil {
			slog.Warn("side effects: clear row", "id", se.ID, "err", cerr)
		}
		e.publishSideEffect(se, "done", "")
		return
	}
	// Shutdown interrupted the attempt: leave it pending, uncounted, for the
	// next process.
	if ctx.Err() != nil {
		_ = e.sq.RetryTaskSideEffectLater(context.WithoutCancel(ctx), gen.RetryTaskSideEffectLaterParams{
			ID: se.ID, Attempts: se.Attempts, NextAt: time.Now(), LastError: se.LastError,
		})
		return
	}

	attempts := se.Attempts + 1
	msg := err.Error()
	slog.Warn("side effect failed", "kind", se.Kind, "task_id", se.TaskID, "attempt", attempts, "err", err)
	bg := context.WithoutCancel(ctx)
	if attempts >= MaxSideEffectAttempts {
		if ferr := e.sq.FailTaskSideEffect(bg, gen.FailTaskSideEffectParams{ID: se.ID, Attempts: attempts, LastError: &msg}); ferr != nil {
			slog.Warn("side effects: mark failed", "id", se.ID, "err", ferr)
		}
		e.publishSideEffect(se, SideEffectFailed, msg)
		return
	}
	backoff := e.SideEffectBackoff
	if backoff == nil {
		backoff = DefaultSideEffectBackoff
	}
	if rerr := e.sq.RetryTaskSideEffectLater(bg, gen.RetryTaskSideEffectLaterParams{
		ID: se.ID, Attempts: attempts, NextAt: time.Now().Add(backoff(int(attempts))), LastError: &msg,
	}); rerr != nil {
		slog.Warn("side effects: reschedule", "id", se.ID, "err", rerr)
	}
	e.publishSideEffect(se, SideEffectPending, msg)
}

// execSideEffect re-reads the task (it may have moved since the effect was
// enqueued) and runs the matching hook. An effect whose destination label the
// task has since left is dropped as stale: pushing and removing the worktree of
// a task a human moved back to work would destroy live state.
func (e *Engine) execSideEffect(ctx context.Context, se gen.TaskSideEffect) error {
	task, err := e.sq.GetTask(ctx, se.TaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}
	// A merged/closed PR means the branch's work is upstream; push and PR
	// effects can never be needed (and would fail: the local branch is gone).
	if (se.Kind == SideEffectTerminal || se.Kind == SideEffectCreatePR) &&
		(task.GitState == "pr_merged" || task.GitState == "pr_closed") {
		return nil
	}
	labels, err := e.sq.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		return fmt.Errorf("list labels: %w", err)
	}
	on := func(match func(gen.WorkflowLabel) bool) bool {
		for _, l := range labels {
			if l.Name == task.Label {
				return match(l)
			}
		}
		return false
	}
	switch se.Kind {
	case SideEffectTerminal:
		if e.OnTerminal == nil || !on(func(l gen.WorkflowLabel) bool { return l.IsTerminal != 0 }) {
			return nil
		}
		return e.OnTerminal(ctx, task)
	case SideEffectCreatePR:
		if e.OnCreatePR == nil || !on(func(l gen.WorkflowLabel) bool { return l.CreatePr != 0 }) {
			return nil
		}
		return e.OnCreatePR(ctx, task)
	case SideEffectLeaveNotReady:
		if e.OnLeaveNotReady == nil {
			return nil
		}
		return e.OnLeaveNotReady(ctx, task)
	default:
		return fmt.Errorf("unknown side effect kind %q", se.Kind)
	}
}

func (e *Engine) publishSideEffect(se gen.TaskSideEffect, status, lastError string) {
	if e.pub == nil {
		return
	}
	e.pub.Publish(wsevent.TypeTaskSideEffect, wsevent.TaskSideEffect{
		TaskID:    se.TaskID,
		Kind:      se.Kind,
		Status:    status,
		LastError: lastError,
	})
	e.pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: se.TaskID})
}

// RetrySideEffects makes a task's failed side effects pending again with a
// fresh attempt budget and wakes the worker. It returns how many were requeued.
func (e *Engine) RetrySideEffects(ctx context.Context, taskID string) (int64, error) {
	n, err := e.sq.RequeueFailedTaskSideEffects(ctx, gen.RequeueFailedTaskSideEffectsParams{NextAt: time.Now(), TaskID: taskID})
	if err != nil {
		return 0, err
	}
	if n > 0 {
		e.nudgeWorker()
		if e.pub != nil {
			e.pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: taskID})
		}
	}
	return n, nil
}

// DismissSideEffects deletes a task's failed side effects without running
// them. It returns how many were removed.
func (e *Engine) DismissSideEffects(ctx context.Context, taskID string) (int64, error) {
	n, err := e.sq.DismissFailedTaskSideEffects(ctx, taskID)
	if err != nil {
		return 0, err
	}
	if n > 0 && e.pub != nil {
		e.pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: taskID})
	}
	return n, nil
}
