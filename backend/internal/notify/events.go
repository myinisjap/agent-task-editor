package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

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
		if n.Label != "" {
			// Gate-scoped (autonomy_stepped_down): different gates must not
			// debounce each other.
			return n.Reason + ":" + n.Label
		}
		return n.Reason
	}
	return n.Reason + ":" + n.TaskID
}

// classify inspects a raw WS event and, if it is one of the trigger
// events this package cares about, resolves it into a notification. ok is
// false for any other event type, or when classification determines the
// event should not notify (e.g. a task.label_changed into a non-gate
// label) -- callers should silently skip in that case, not treat it as an
// error.
func (n *Notifier) classify(ctx context.Context, ev rawEvent) (notification, bool) {
	switch ev.eventType {
	case wsevent.TypeTaskNeedsHuman:
		return n.classifyNeedsHuman(ctx, wsevent.ToMap(ev.payload))
	case wsevent.TypeTaskLabelChanged:
		return n.classifyLabelChanged(ctx, wsevent.ToMap(ev.payload))
	case wsevent.TypeTaskCostWarning:
		return n.classifyCostWarning(ctx, wsevent.ToMap(ev.payload))
	case wsevent.TypeTaskAutonomyAsked, wsevent.TypeTaskAutonomyHeld:
		return n.classifyAutonomy(ctx, ev.eventType, wsevent.ToMap(ev.payload))
	case wsevent.TypeSystemCostBudgetTripped:
		return n.classifyCostBudgetTripped(wsevent.ToMap(ev.payload))
	case wsevent.TypeAutonomySteppedDown:
		return n.classifySteppedDown(wsevent.ToMap(ev.payload))
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
	// A gate whose autonomy mode is not "ask" notifies only when the policy
	// hands the task to a person (task.autonomy_asked) or a person holds an
	// auto-approval (task.autonomy_held); tasks that will auto-approve stay quiet.
	for _, l := range labels {
		if l.Name == to && l.AutonomyMode != "ask" {
			return notification{}, false
		}
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

// classifyAutonomy turns task.autonomy_asked (the policy handed the task to a
// person) and task.autonomy_held into a human_gate notification. They share the
// human_gate debounce key with each other.
func (n *Notifier) classifyAutonomy(ctx context.Context, eventType string, payload map[string]any) (notification, bool) {
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		return notification{}, false
	}
	task, err := n.q.GetTask(ctx, taskID)
	if err != nil {
		return notification{}, false
	}
	label := stringField(payload, "label")
	if label == "" {
		label = task.Label
	}
	msg := "Auto-approval held by " + stringField(payload, "actor")
	if eventType == wsevent.TypeTaskAutonomyAsked {
		msg = "Needs your decision (" + stringField(payload, "reason")
		if rs, ok := payload["reasons"].([]any); ok && len(rs) > 0 {
			codes := make([]string, 0, len(rs))
			for _, r := range rs {
				if s, ok := r.(string); ok {
					codes = append(codes, s)
				}
			}
			msg += ": " + strings.Join(codes, ", ")
		}
		msg += ")"
	}
	note := notification{Event: eventType, Reason: "human_gate", TaskID: taskID, TaskTitle: task.Title, Label: label, Message: msg}
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

func (n *Notifier) classifySteppedDown(payload map[string]any) (notification, bool) {
	label := stringField(payload, "label")
	if label == "" {
		return notification{}, false
	}
	note := notification{
		Event:  "autonomy.stepped_down",
		Reason: "autonomy_stepped_down",
		Label:  label,
		Message: fmt.Sprintf("Gate %s stepped down from %s to %s: %s", label,
			stringField(payload, "from_mode"), stringField(payload, "to_mode"), stringField(payload, "reason")),
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
