// Package wsevent defines the typed payloads of every event the server pushes
// over the /ws WebSocket, plus their event-type names.
//
// The contract is specified in openapi.yaml (components.schemas.WsEvent and the
// Ws*Payload schemas) and the frontend's WSEvent type is generated from it.
// The JSON tags here must match those schemas exactly; TestPayloadsMatchOpenAPI
// fails when they drift. It is a leaf package (stdlib only) so any publisher can
// import it.
package wsevent

import (
	"encoding/json"
	"time"
)

// Event type names, the `type` discriminator of the WsEvent envelope.
const (
	TypeTaskLabelChanged         = "task.label_changed"
	TypeTaskUpdated              = "task.updated"
	TypeTaskSideEffect           = "task.side_effect"
	TypeTaskAgentStarted         = "task.agent_started"
	TypeTaskAgentDone            = "task.agent_done"
	TypeTaskNeedsHuman           = "task.needs_human"
	TypeTaskCostWarning          = "task.cost_warning"
	TypeSystemCostBudgetTripped  = "system.cost_budget_tripped"
	TypeTaskReviewCommentsChange = "task.review_comments_changed"
	TypeTaskVerification         = "task.verification"
	TypeTaskPreview              = "task.preview"
	TypeTaskRateLimited          = "task.rate_limited"
	TypeProviderPauseChanged     = "provider.pause_changed"
	TypeTaskGitStateChanged      = "task.git_state_changed"
	TypeTaskPRMergeableChanged   = "task.pr_mergeable_changed"
	TypeTaskReviewCommentAdded   = "task.review_comment_added"
	TypeTaskSubtaskConflict      = "task.subtask_conflict"
	TypeTaskSourceCommentAdded   = "task.source_comment_added"
	TypeTaskCreated              = "task.created"
	TypeTaskCreatedBulk          = "task.created_bulk"
	TypeRepoCloneDone            = "repo.clone_done"
	TypeRepoCloneFailed          = "repo.clone_failed"
	TypeAgentLog                 = "agent.log"
	TypeAgentLogReplay           = "agent.log_replay"
	TypeTaskAutonomyPending      = "task.autonomy_pending"
	TypeTaskAutonomyHeld         = "task.autonomy_held"
	TypeTaskAutonomyApproved     = "task.autonomy_approved"
	TypeTaskAutonomyUndone       = "task.autonomy_undone"
	TypeTaskAutonomyAsked        = "task.autonomy_asked"
	TypeAutonomySteppedDown      = "autonomy.stepped_down"
	TypeResync                   = "resync"
)

// TaskLabelChanged is the payload of task.label_changed.
type TaskLabelChanged struct {
	TaskID string `json:"task_id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Note   string `json:"note"`
}

// TaskUpdated is the payload of task.updated: a "refetch this task" signal.
type TaskUpdated struct {
	ID string `json:"id"`
}

// TaskSideEffect is the payload of task.side_effect.
type TaskSideEffect struct {
	TaskID    string `json:"task_id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}

// TaskAgentStarted is the payload of task.agent_started.
type TaskAgentStarted struct {
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
	AgentName string `json:"agent_name"`
}

// TaskAgentDone is the payload of task.agent_done.
type TaskAgentDone struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// TaskNeedsHuman is the payload of task.needs_human.
type TaskNeedsHuman struct {
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// TaskCostWarning is the payload of task.cost_warning. RunID is empty (and
// omitted) for the dispatcher's pre-dispatch variant.
type TaskCostWarning struct {
	TaskID    string  `json:"task_id"`
	RunID     string  `json:"run_id,omitempty"`
	SpentUSD  float64 `json:"spent_usd"`
	BudgetUSD float64 `json:"budget_usd"`
}

// SystemCostBudgetTripped is the payload of system.cost_budget_tripped.
type SystemCostBudgetTripped struct {
	Reason          string  `json:"reason"`
	Message         string  `json:"message"`
	DailySpentUSD   float64 `json:"daily_spent_usd"`
	DailyLimitUSD   float64 `json:"daily_limit_usd"`
	MonthlySpentUSD float64 `json:"monthly_spent_usd"`
	MonthlyLimitUSD float64 `json:"monthly_limit_usd"`
}

// TaskReviewCommentsChanged is the payload of task.review_comments_changed.
type TaskReviewCommentsChanged struct {
	TaskID   string `json:"task_id"`
	RunID    string `json:"run_id"`
	Resolved int    `json:"resolved"`
}

// TaskVerification is the payload of task.verification: a run's repo verify
// command finished. Status is pass | fail | timeout | error.
type TaskVerification struct {
	TaskID string `json:"task_id"`
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// TaskRateLimited is the payload of task.rate_limited.
type TaskRateLimited struct {
	TaskID        string `json:"task_id"`
	RunID         string `json:"run_id"`
	AgentConfigID string `json:"agent_config_id"`
	UnblockedAt   string `json:"unblocked_at"`
}

// ProviderPauseChanged is the payload of provider.pause_changed.
type ProviderPauseChanged struct {
	Provider string `json:"provider"`
	Paused   bool   `json:"paused"`
}

// TaskGitStateChanged is the payload of task.git_state_changed.
type TaskGitStateChanged struct {
	TaskID   string `json:"task_id"`
	GitState string `json:"git_state"`
	PRURL    string `json:"pr_url"`
}

// TaskPRMergeableChanged is the payload of task.pr_mergeable_changed.
type TaskPRMergeableChanged struct {
	TaskID      string `json:"task_id"`
	PRMergeable string `json:"pr_mergeable"`
}

// TaskReviewCommentAdded is the payload of task.review_comment_added.
type TaskReviewCommentAdded struct {
	TaskID    string `json:"task_id"`
	CommentID string `json:"comment_id"`
	Source    string `json:"source"`
}

// TaskSubtaskConflict is the payload of task.subtask_conflict.
type TaskSubtaskConflict struct {
	TaskID   string   `json:"task_id"`
	ParentID string   `json:"parent_id"`
	Files    []string `json:"files"`
}

// TaskSourceCommentAdded is the payload of task.source_comment_added.
type TaskSourceCommentAdded struct {
	TaskID    string `json:"task_id"`
	CommentID string `json:"comment_id"`
}

// TaskCreated is the payload of task.created. Source and SourceRef are set
// for schedule-created tasks, ParentID for subtasks; the rest omit them. The
// source fields are pointers so an explicitly empty value is still sent.
type TaskCreated struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Label     string  `json:"label"`
	RepoID    string  `json:"repo_id"`
	Source    *string `json:"source,omitempty"`
	SourceRef *string `json:"source_ref,omitempty"`
	ParentID  string  `json:"parent_id,omitempty"`
}

// TaskCreatedBulk is the payload of task.created_bulk.
type TaskCreatedBulk struct {
	RepoID string   `json:"repo_id"`
	Source string   `json:"source"`
	Count  int      `json:"count"`
	IDs    []string `json:"ids"`
}

// RepoClone is the payload of repo.clone_done and repo.clone_failed.
type RepoClone struct {
	RepoID      string `json:"repo_id"`
	CloneStatus string `json:"clone_status"`
	CloneError  string `json:"clone_error"`
}

// AgentLogEntry is the log line carried by agent.log and agent.log_replay.
// Note the timestamp key is "at", unlike the REST AgentLog.
type AgentLogEntry struct {
	ID      string    `json:"id"`
	Seq     int64     `json:"seq"`
	Type    string    `json:"type"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

// AgentLog is the payload of agent.log.
type AgentLog struct {
	RunID  string        `json:"run_id"`
	TaskID string        `json:"task_id"`
	Entry  AgentLogEntry `json:"entry"`
}

// AgentLogReplay is the payload of agent.log_replay.
type AgentLogReplay struct {
	RunID   string          `json:"run_id"`
	TaskID  string          `json:"task_id"`
	HasMore bool            `json:"has_more"`
	Entries []AgentLogEntry `json:"entries"`
}

// Resync is the (always empty) payload of resync.
type Resync struct{}

// ToMap converts a payload to a generic map via its JSON form. It is for
// consumers that classify events by field name (the outbound webhook notifier)
// and for tests. Non-object payloads yield nil.
func ToMap(payload any) map[string]any {
	if m, ok := payload.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// TaskPreview is the payload of task.preview: a preview screenshot capture
// started or finished for a task. Status is running | ok | failed.
type TaskPreview struct {
	TaskID    string `json:"task_id"`
	CaptureID string `json:"capture_id"`
	Status    string `json:"status"`
}

// TaskAutonomyPending is the payload of task.autonomy_pending: the policy
// scheduled an auto-approval that fires at DueAt unless a person holds it.
type TaskAutonomyPending struct {
	TaskID      string `json:"task_id"`
	Label       string `json:"label"`
	DecisionID  string `json:"decision_id"`
	DueAt       string `json:"due_at"` // RFC3339, UTC
	HoldSeconds int    `json:"hold_seconds"`
}

// TaskAutonomyHeld is the payload of task.autonomy_held: a person cancelled a
// pending auto-approval; the task is now a normal review.
type TaskAutonomyHeld struct {
	TaskID     string `json:"task_id"`
	Label      string `json:"label"`
	DecisionID string `json:"decision_id"`
	Actor      string `json:"actor"`
}

// TaskAutonomyApproved is the payload of task.autonomy_approved: the policy
// approved the task (the usual task.label_changed is published as well).
type TaskAutonomyApproved struct {
	TaskID     string `json:"task_id"`
	DecisionID string `json:"decision_id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Note       string `json:"note"`
}

// TaskAutonomyUndone is the payload of task.autonomy_undone: a person undid an
// auto-approval and the task is back on its gate.
type TaskAutonomyUndone struct {
	TaskID string `json:"task_id"`
	Label  string `json:"label"`
	From   string `json:"from"`
	PRURL  string `json:"pr_url,omitempty"`
}

// TaskAutonomyAsked is the payload of task.autonomy_asked: the policy handed a
// task to a person (policy decision, skipped, failed or evaluation_error).
type TaskAutonomyAsked struct {
	TaskID     string   `json:"task_id"`
	Label      string   `json:"label"`
	DecisionID string   `json:"decision_id,omitempty"`
	Reason     string   `json:"reason"`
	Reasons    []string `json:"reasons"`
}

// Reason values of TaskAutonomyAsked.
const (
	AskedPolicy          = "policy"
	AskedSkipped         = "skipped"
	AskedFailed          = "failed"
	AskedEvaluationError = "evaluation_error"
	AskedSteppedDown     = "stepped_down"
)

// AutonomySteppedDown is the payload of autonomy.stepped_down: a gate's mode
// was lowered after too many escapes.
type AutonomySteppedDown struct {
	WorkflowID string `json:"workflow_id"`
	Label      string `json:"label"`
	FromMode   string `json:"from_mode"`
	ToMode     string `json:"to_mode"`
	Actor      string `json:"actor"`
	Reason     string `json:"reason"`
	Escapes    int    `json:"escapes"`
	ChangeID   string `json:"change_id"`
}
