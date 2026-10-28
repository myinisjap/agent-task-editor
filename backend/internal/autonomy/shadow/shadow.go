// Package shadow records what the autonomy policy WOULD decide when a task
// lands on a gate. It subscribes to task.label_changed through notify.FanOut,
// does all work on its own goroutine, and never touches the engine: a failure
// here can never block or alter a transition.
package shadow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

const queueCap = 256

type event struct {
	taskID string
	to     string
}

// Recorder is an event subscriber that records shadow autonomy decisions.
type Recorder struct {
	q     *gen.Queries
	cache *reviewbrief.Cache
	queue chan event
}

// New creates a Recorder.
func New(q *gen.Queries) *Recorder {
	return &Recorder{q: q, cache: reviewbrief.NewCache(), queue: make(chan event, queueCap)}
}

// Publish enqueues label-change events. It never blocks; a full queue drops
// the event.
func (r *Recorder) Publish(eventType string, payload any) {
	if r == nil || eventType != wsevent.TypeTaskLabelChanged {
		return
	}
	m := wsevent.ToMap(payload)
	taskID, _ := m["task_id"].(string)
	to, _ := m["to"].(string)
	if taskID == "" || to == "" {
		return
	}
	select {
	case r.queue <- event{taskID: taskID, to: to}:
	default:
		slog.Warn("autonomy shadow: queue full, dropping event", "task_id", taskID)
	}
}

// Run drains the queue until ctx is cancelled.
func (r *Recorder) Run(ctx context.Context) {
	if r == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-r.queue:
			r.Handle(ctx, ev.taskID, ev.to)
		}
	}
}

// Handle evaluates and records a decision for a task that landed on label
// toLabel. It logs and returns on every error and recovers from panics.
func (r *Recorder) Handle(ctx context.Context, taskID, toLabel string) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("autonomy shadow: panic", "task_id", taskID, "panic", rec)
		}
	}()
	if err := r.handle(ctx, taskID, toLabel); err != nil {
		slog.Warn("autonomy shadow: skipped", "task_id", taskID, "err", err)
	}
}

func (r *Recorder) handle(ctx context.Context, taskID, toLabel string) error {
	task, err := r.q.GetTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if task.Archived != 0 || task.Label != toLabel {
		return nil
	}
	labels, err := r.q.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		return err
	}
	transitions, err := r.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		return err
	}
	if !needsme.IsHumanGate(needsme.WorkflowDef{Labels: labels, Transitions: transitions}, toLabel) {
		return nil
	}
	var label *gen.WorkflowLabel
	for i := range labels {
		if labels[i].Name == toLabel {
			label = &labels[i]
			break
		}
	}
	if label == nil || label.AutonomyMode == string(autonomy.ModeAsk) {
		return nil
	}
	mode, err := autonomy.ParseMode(label.AutonomyMode)
	if err != nil {
		return err
	}
	var askOn []string
	if err := json.Unmarshal([]byte(label.AutonomyAskOn), &askOn); err != nil {
		return err
	}
	cost, err := r.q.SumTaskCost(ctx, task.ID)
	if err != nil {
		return err
	}
	in := autonomy.Input{
		Mode:       mode,
		AskOn:      askOn,
		AlwaysAsk:  task.AlwaysAsk != 0,
		CostUSD:    cost,
		MaxCostUSD: label.AutonomyMaxCostUsd,
	}
	if task.CurrentAgentRunID != nil {
		v, err := r.q.GetRunVerification(ctx, *task.CurrentAgentRunID)
		switch {
		case err == nil:
			in.VerifyStatus = v.Status
		case errors.Is(err, sql.ErrNoRows):
		default:
			return err
		}
	}
	if mode == autonomy.ModeOnFlags {
		res, err := r.cache.Diff(ctx, r.q, task)
		if err != nil {
			slog.Warn("autonomy shadow: diff failed", "task_id", task.ID, "err", err)
		} else if res.Available && res.Changed.Files > 0 {
			in.HasDiff = true
			for _, f := range res.Flags {
				in.Flags = append(in.Flags, f.Code)
			}
		}
	}
	d := autonomy.Evaluate(in)
	reasons, err := json.Marshal(d.Reasons)
	if err != nil {
		return err
	}
	return r.q.CreateAutonomyDecision(ctx, gen.CreateAutonomyDecisionParams{
		ID:         uuid.NewString(),
		TaskID:     task.ID,
		WorkflowID: task.WorkflowID,
		Label:      toLabel,
		RunID:      task.CurrentAgentRunID,
		Mode:       string(mode),
		Decision:   d.Decision,
		Reasons:    string(reasons),
	})
}
