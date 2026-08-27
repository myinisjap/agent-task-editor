package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// notification is the classified, ready-to-deliver form of a raw WS event.
type notification struct {
	Event     string `json:"event"`
	Reason    string `json:"reason"`
	TaskID    string `json:"task_id,omitempty"`
	TaskTitle string `json:"task_title,omitempty"`
	Label     string `json:"label,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Message   string `json:"message,omitempty"`
	URL       string `json:"url,omitempty"`
	Timestamp string `json:"timestamp"`
}

// debounceKey returns the dedupe key used by debounce.go: reason+task_id for
// task-scoped notifications, or just reason for the system-wide
// cost_budget_tripped event (there is no task to key on).
//
// task.needs_human and task.label_changed (human_gate) are deliberately kept
// as separate reasons here (not collapsed into one key) even though they can
// fire close together for the same task/run -- e.g. a pool.go escalation
// both moves the task onto a human-gate label and publishes
// task.needs_human. The two carry genuinely different messages (why the
// agent stopped vs. which label the task is now sitting on), so both are
// allowed to notify. See docs/websocket.md's "Outbound webhook" section for
// the operator-facing version of this note.
func (n notification) debounceKey() string {
	if n.TaskID == "" {
		return n.Reason
	}
	return n.Reason + ":" + n.TaskID
}

// classify inspects a raw WS event and, if it is one of the four trigger
// events this package cares about, resolves it into a notification. ok is
// false for any other event type, or when classification determines the
// event should not notify (e.g. a task.label_changed into a non-gate
// label) -- callers should silently skip in that case, not treat it as an
// error.
func (n *Notifier) classify(ctx context.Context, ev rawEvent) (notification, bool) {
	switch ev.eventType {
	case "task.needs_human":
		return n.classifyNeedsHuman(ctx, ev.payload)
	case "task.label_changed":
		return n.classifyLabelChanged(ctx, ev.payload)
	case "task.cost_warning":
		return n.classifyCostWarning(ctx, ev.payload)
	case "system.cost_budget_tripped":
		return n.classifyCostBudgetTripped(ev.payload)
	default:
		return notification{}, false
	}
}

func (n *Notifier) classifyNeedsHuman(ctx context.Context, payload map[string]any) (notification, bool) {
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		return notification{}, false
	}
	task, err := n.q.GetTask(ctx, taskID)
	if err != nil {
		return notification{}, false
	}
	note := notification{
		Event:     "task.needs_human",
		Reason:    "needs_human",
		TaskID:    taskID,
		TaskTitle: task.Title,
		Label:     task.Label,
		Message:   stringField(payload, "message"),
		RunID:     stringField(payload, "run_id"),
	}
	n.fillTimestampAndURL(&note)
	return note, true
}

func (n *Notifier) classifyLabelChanged(ctx context.Context, payload map[string]any) (notification, bool) {
	taskID, _ := payload["task_id"].(string)
	to := stringField(payload, "to")
	if taskID == "" || to == "" {
		return notification{}, false
	}
	task, err := n.q.GetTask(ctx, taskID)
	if err != nil {
		return notification{}, false
	}
	labels, err := n.q.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		return notification{}, false
	}
	transitions, err := n.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		return notification{}, false
	}
	if !workflow.IsHumanGateLabel(labels, transitions, to) {
		return notification{}, false
	}
	note := notification{
		Event:     "task.label_changed",
		Reason:    "human_gate",
		TaskID:    taskID,
		TaskTitle: task.Title,
		Label:     to,
		Message:   stringField(payload, "note"),
	}
	n.fillTimestampAndURL(&note)
	return note, true
}

func (n *Notifier) classifyCostWarning(ctx context.Context, payload map[string]any) (notification, bool) {
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		return notification{}, false
	}
	task, err := n.q.GetTask(ctx, taskID)
	if err != nil {
		return notification{}, false
	}
	spent, _ := floatField(payload, "spent_usd")
	budget, _ := floatField(payload, "budget_usd")
	note := notification{
		Event:     "task.cost_warning",
		Reason:    "cost_warning",
		TaskID:    taskID,
		TaskTitle: task.Title,
		Label:     task.Label,
		RunID:     stringField(payload, "run_id"),
		Message:   fmt.Sprintf("$%.2f of $%.2f budget spent", spent, budget),
	}
	n.fillTimestampAndURL(&note)
	return note, true
}

func (n *Notifier) classifyCostBudgetTripped(payload map[string]any) (notification, bool) {
	note := notification{
		Event:   "system.cost_budget_tripped",
		Reason:  "cost_budget_tripped",
		Message: stringField(payload, "message"),
	}
	n.fillTimestampAndURL(&note)
	return note, true
}

// fillTimestampAndURL sets Timestamp (always) and URL (only when baseURL is
// configured and the notification has a task).
//
// URL is deliberately omitted rather than guessed when baseURL is unset -- a
// notification whose link only works on the server box (e.g. localhost)
// would be worse than no link at all.
func (n *Notifier) fillTimestampAndURL(note *notification) {
	note.Timestamp = n.now().UTC().Format(time.RFC3339)
	if n.baseURL != "" && note.TaskID != "" {
		note.URL = strings.TrimRight(n.baseURL, "/") + "/tasks/" + note.TaskID
	}
}

func stringField(payload map[string]any, key string) string {
	s, _ := payload[key].(string)
	return s
}

func floatField(payload map[string]any, key string) (float64, bool) {
	switch v := payload[key].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}
