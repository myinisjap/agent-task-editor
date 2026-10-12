// Package gen holds the sqlc-generated query code for the storage layer plus
// the hand-written tasks_compat.go companion.
package gen

// Hand-written companion to tasks.sql.go (sqlc never touches this file).
//
// The board-state UPDATE/INSERT statements in queries/tasks.sql used to end in
// RETURNING <every tasks column>. The git/worktree/PR and write-back columns
// now live in the task_git and task_writeback side tables, which RETURNING
// cannot join, so those statements are :exec queries under unexported names
// and the exported methods below keep the original signatures: run the write,
// then read the flat row back from the tasks_full view with GetTask. Callers
// are therefore unchanged, and an unknown task id still yields sql.ErrNoRows.
//
// The side-table writers (SetTaskWorktree, ClearTaskWorktreePath,
// UpdateTaskGitState, SetTaskPR, SetTaskPRMergeable) upsert their task_git row
// lazily; all but SetTaskPRMergeable also bump tasks.updated_at as before.

import "context"

// CreateTaskParams are the arguments of CreateTask.
type CreateTaskParams = createTaskParams

// CreateTask runs the write and returns the resulting task row.
func (q *Queries) CreateTask(ctx context.Context, arg CreateTaskParams) (Task, error) {
	if err := q.createTask(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// CreateSourcedTaskParams are the arguments of CreateSourcedTask.
type CreateSourcedTaskParams = createSourcedTaskParams

// CreateSourcedTask runs the write and returns the resulting task row.
func (q *Queries) CreateSourcedTask(ctx context.Context, arg CreateSourcedTaskParams) (Task, error) {
	if err := q.createSourcedTask(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// CreateSubtaskParams are the arguments of CreateSubtask.
type CreateSubtaskParams = createSubtaskParams

// CreateSubtask runs the write and returns the resulting task row.
func (q *Queries) CreateSubtask(ctx context.Context, arg CreateSubtaskParams) (Task, error) {
	if err := q.createSubtask(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// UpdateTaskParams are the arguments of UpdateTask.
type UpdateTaskParams = updateTaskParams

// UpdateTask runs the write and returns the resulting task row.
func (q *Queries) UpdateTask(ctx context.Context, arg UpdateTaskParams) (Task, error) {
	if err := q.updateTask(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// UpdateTaskFromSourceParams are the arguments of UpdateTaskFromSource.
type UpdateTaskFromSourceParams = updateTaskFromSourceParams

// UpdateTaskFromSource runs the write and returns the resulting task row.
func (q *Queries) UpdateTaskFromSource(ctx context.Context, arg UpdateTaskFromSourceParams) (Task, error) {
	if err := q.updateTaskFromSource(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// UpdateTaskLabelParams are the arguments of UpdateTaskLabel.
type UpdateTaskLabelParams = updateTaskLabelParams

// UpdateTaskLabel runs the write and returns the resulting task row.
func (q *Queries) UpdateTaskLabel(ctx context.Context, arg UpdateTaskLabelParams) (Task, error) {
	if err := q.updateTaskLabel(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// SetTaskTransientRetryParams are the arguments of SetTaskTransientRetry.
type SetTaskTransientRetryParams = setTaskTransientRetryParams

// SetTaskTransientRetry runs the write and returns the resulting task row.
func (q *Queries) SetTaskTransientRetry(ctx context.Context, arg SetTaskTransientRetryParams) (Task, error) {
	if err := q.setTaskTransientRetry(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// UpdateTaskNotesParams are the arguments of UpdateTaskNotes.
type UpdateTaskNotesParams = updateTaskNotesParams

// UpdateTaskNotes runs the write and returns the resulting task row.
func (q *Queries) UpdateTaskNotes(ctx context.Context, arg UpdateTaskNotesParams) (Task, error) {
	if err := q.updateTaskNotes(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// UpdateTaskAttachmentsParams are the arguments of UpdateTaskAttachments.
type UpdateTaskAttachmentsParams = updateTaskAttachmentsParams

// UpdateTaskAttachments runs the write and returns the resulting task row.
func (q *Queries) UpdateTaskAttachments(ctx context.Context, arg UpdateTaskAttachmentsParams) (Task, error) {
	if err := q.updateTaskAttachments(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// SetTaskPausedParams are the arguments of SetTaskPaused.
type SetTaskPausedParams = setTaskPausedParams

// SetTaskPaused runs the write and returns the resulting task row.
func (q *Queries) SetTaskPaused(ctx context.Context, arg SetTaskPausedParams) (Task, error) {
	if err := q.setTaskPaused(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// SetTaskArchivedParams are the arguments of SetTaskArchived.
type SetTaskArchivedParams = setTaskArchivedParams

// SetTaskArchived runs the write and returns the resulting task row.
func (q *Queries) SetTaskArchived(ctx context.Context, arg SetTaskArchivedParams) (Task, error) {
	if err := q.setTaskArchived(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// SetTaskMergeStatusParams are the arguments of SetTaskMergeStatus.
type SetTaskMergeStatusParams = setTaskMergeStatusParams

// SetTaskMergeStatus runs the write and returns the resulting task row.
func (q *Queries) SetTaskMergeStatus(ctx context.Context, arg SetTaskMergeStatusParams) (Task, error) {
	if err := q.setTaskMergeStatus(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// ResetTaskTransientRetry clears the transient retry state and returns the task.
func (q *Queries) ResetTaskTransientRetry(ctx context.Context, id string) (Task, error) {
	if err := q.resetTaskTransientRetry(ctx, id); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, id)
}

// SetTaskWorktreeParams are the arguments of SetTaskWorktree.
type SetTaskWorktreeParams = upsertTaskWorktreeParams

// SetTaskWorktree records the task's branch, worktree path and base ref.
func (q *Queries) SetTaskWorktree(ctx context.Context, arg SetTaskWorktreeParams) error {
	if err := q.touchTask(ctx, arg.ID); err != nil {
		return err
	}
	return q.upsertTaskWorktree(ctx, arg)
}

// ClearTaskWorktreePath clears only worktree_path (branch/base_ref are kept: the
// branch survives in the repo's main clone after the worktree dir is removed).
func (q *Queries) ClearTaskWorktreePath(ctx context.Context, id string) error {
	if err := q.touchTask(ctx, id); err != nil {
		return err
	}
	return q.clearTaskWorktreePath(ctx, id)
}

// UpdateTaskGitStateParams are the arguments of UpdateTaskGitState.
type UpdateTaskGitStateParams = upsertTaskGitStateParams

// UpdateTaskGitState sets git_state and returns the task.
func (q *Queries) UpdateTaskGitState(ctx context.Context, arg UpdateTaskGitStateParams) (Task, error) {
	if err := q.touchTask(ctx, arg.ID); err != nil {
		return Task{}, err
	}
	if err := q.upsertTaskGitState(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// SetTaskPRParams are the arguments of SetTaskPR.
type SetTaskPRParams = upsertTaskPRParams

// SetTaskPR sets git_state and pr_url together and returns the task.
func (q *Queries) SetTaskPR(ctx context.Context, arg SetTaskPRParams) (Task, error) {
	if err := q.touchTask(ctx, arg.ID); err != nil {
		return Task{}, err
	}
	if err := q.upsertTaskPR(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}

// SetTaskPRMergeableParams are the arguments of SetTaskPRMergeable.
type SetTaskPRMergeableParams = upsertTaskPRMergeableParams

// SetTaskPRMergeable records the PR mergeability verdict and returns the task.
// Deliberately does not touch updated_at: ghsync refreshes this on every sweep
// and a background poll should not make a task look freshly edited.
func (q *Queries) SetTaskPRMergeable(ctx context.Context, arg SetTaskPRMergeableParams) (Task, error) {
	if err := q.upsertTaskPRMergeable(ctx, arg); err != nil {
		return Task{}, err
	}
	return q.GetTask(ctx, arg.ID)
}
