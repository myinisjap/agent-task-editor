// Package policy evaluates the autonomy policy when a task lands on a gate and
// acts on approve decisions. It subscribes to task.label_changed through
// notify.FanOut and does all work on one goroutine (Run): recording decisions,
// the hold=0 immediate approval and the periodic sweep of due pending
// approvals are therefore serialized. Approvals go through the engine's shared
// Approve path as a human-equivalent transition with actor "policy".
package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

const queueCap = 256

// DefaultSweepInterval is how often due pending approvals are looked for.
const DefaultSweepInterval = 10 * time.Second

type event struct {
	taskID string
	to     string
}

// Approver is the part of *workflow.Engine the policy needs.
type Approver interface {
	Approve(ctx context.Context, taskID string, o workflow.ApproveOptions) (string, error)
	RunIsLive(ctx context.Context, task gen.Task) bool
}

type publisher interface {
	Publish(eventType string, payload any)
}

// Service is an event subscriber that evaluates and acts on autonomy decisions.
type Service struct {
	q     *gen.Queries
	cache *reviewbrief.Cache
	queue chan event

	approver Approver
	pub      publisher
	// SweepInterval is the period of the due-approval sweep (default 10s).
	SweepInterval time.Duration
	now           func() time.Time
}

// New creates a Service.
func New(q *gen.Queries) *Service {
	return &Service{q: q, cache: reviewbrief.NewCache(), queue: make(chan event, queueCap),
		SweepInterval: DefaultSweepInterval, now: func() time.Time { return time.Now().UTC() }}
}

// SetApprover wires the engine (built after the Service in main).
func (r *Service) SetApprover(a Approver) { r.approver = a }

// SetPublisher wires the event publisher for the policy's own events. Publish
// only reacts to task.label_changed, so publishing through the fan-out that
// contains this Service cannot loop.
func (r *Service) SetPublisher(p publisher) { r.pub = p }

func (r *Service) publish(eventType string, payload any) {
	if r.pub != nil {
		r.pub.Publish(eventType, payload)
	}
}

// Publish enqueues label-change events. It never blocks; a full queue drops
// the event.
func (r *Service) Publish(eventType string, payload any) {
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
		slog.Warn("autonomy policy: queue full, dropping event", "task_id", taskID)
	}
}

// Run drains the queue and sweeps due approvals until ctx is cancelled. It
// sweeps once immediately, which makes pending approvals restart-safe.
func (r *Service) Run(ctx context.Context) {
	if r == nil {
		return
	}
	iv := r.SweepInterval
	if iv <= 0 {
		iv = DefaultSweepInterval
	}
	tick := time.NewTicker(iv)
	defer tick.Stop()
	r.Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.Sweep(ctx)
		case ev := <-r.queue:
			r.Handle(ctx, ev.taskID, ev.to)
		}
	}
}

// Handle evaluates and records a decision for a task that landed on label
// toLabel, and acts on it. It logs and returns on every error and recovers
// from panics.
func (r *Service) Handle(ctx context.Context, taskID, toLabel string) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("autonomy policy: panic", "task_id", taskID, "panic", rec)
		}
	}()
	if err := r.handle(ctx, taskID, toLabel); err != nil {
		slog.Warn("autonomy policy: skipped", "task_id", taskID, "err", err)
	}
}

func (r *Service) handle(ctx context.Context, taskID, toLabel string) error {
	task, err := r.q.GetTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	// A pending approval for any other label is stale (a person moved the task).
	now := r.now()
	if err := r.q.SupersedePendingAutonomy(ctx, gen.SupersedePendingAutonomyParams{OutcomeAt: &now, TaskID: task.ID, Label: toLabel}); err != nil {
		slog.Warn("autonomy policy: supersede failed", "task_id", task.ID, "err", err)
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
	row, err := r.evaluate(ctx, task, *label)
	if err != nil {
		// The gate is non-ask, so a person must still hear about the task.
		r.publishAsked(task.ID, toLabel, "", wsevent.AskedEvaluationError, nil)
		return err
	}
	if err := r.q.CreateAutonomyDecision(ctx, row); err != nil {
		r.publishAsked(task.ID, toLabel, "", wsevent.AskedEvaluationError, nil)
		return err
	}
	if row.Decision == autonomy.DecisionAsk {
		r.publishAsked(task.ID, toLabel, row.ID, wsevent.AskedPolicy, reasonCodes(row.Reasons))
		return nil
	}
	if row.HoldSeconds != nil && *row.HoldSeconds > 0 {
		r.publish(wsevent.TypeTaskAutonomyPending, wsevent.TaskAutonomyPending{
			TaskID: task.ID, Label: toLabel, DecisionID: row.ID,
			DueAt: row.DueAt.UTC().Format(time.RFC3339), HoldSeconds: int(*row.HoldSeconds),
		})
		r.publish(wsevent.TypeTaskUpdated, map[string]any{"id": task.ID})
		return nil
	}
	stored, err := r.q.GetAutonomyDecision(ctx, row.ID)
	if err != nil {
		return err
	}
	r.process(ctx, stored)
	return nil
}

// evaluate builds the input, evaluates, and returns the row to insert (outcome
// pending with hold/due/note for approve decisions, nil outcome for ask).
func (r *Service) evaluate(ctx context.Context, task gen.Task, label gen.WorkflowLabel) (gen.CreateAutonomyDecisionParams, error) {
	var zero gen.CreateAutonomyDecisionParams
	mode, err := autonomy.ParseMode(label.AutonomyMode)
	if err != nil {
		return zero, err
	}
	var askOn []string
	if err := json.Unmarshal([]byte(label.AutonomyAskOn), &askOn); err != nil {
		return zero, err
	}
	cost, err := r.q.SumTaskCost(ctx, task.ID)
	if err != nil {
		return zero, err
	}
	in := autonomy.Input{
		Mode:       mode,
		AskOn:      askOn,
		AlwaysAsk:  task.AlwaysAsk != 0,
		CostUSD:    cost,
		MaxCostUSD: label.AutonomyMaxCostUsd,
	}
	// An undo writes its label-history row before the label_changed event, so
	// this read cannot race it.
	h, err := r.q.GetLatestTaskLabelHistory(ctx, task.ID)
	switch {
	case err == nil:
		if h.ToLabel == task.Label && h.Note != nil && *h.Note == autonomy.UndoNote {
			in.Undone = true
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return zero, err
	}
	if task.CurrentAgentRunID != nil {
		v, err := r.q.GetRunVerification(ctx, *task.CurrentAgentRunID)
		switch {
		case err == nil:
			in.VerifyStatus = v.Status
		case errors.Is(err, sql.ErrNoRows):
		default:
			return zero, err
		}
	}
	if mode == autonomy.ModeOnFlags {
		res, err := r.cache.Diff(ctx, r.q, task)
		if err != nil {
			slog.Warn("autonomy policy: diff failed", "task_id", task.ID, "err", err)
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
		return zero, err
	}
	row := gen.CreateAutonomyDecisionParams{
		ID:         uuid.NewString(),
		TaskID:     task.ID,
		WorkflowID: task.WorkflowID,
		Label:      task.Label,
		RunID:      task.CurrentAgentRunID,
		Mode:       string(mode),
		Decision:   d.Decision,
		Reasons:    string(reasons),
	}
	if d.Decision == autonomy.DecisionApprove {
		hold := label.AutonomyHoldSeconds
		due := r.now().Add(time.Duration(hold) * time.Second)
		note := autonomy.ApprovalNote(mode, in.Flags, in.VerifyStatus, int(hold))
		pending := autonomy.OutcomePending
		row.Outcome = &pending
		row.HoldSeconds = &hold
		row.DueAt = &due
		row.Note = &note
	}
	return row, nil
}

func reasonCodes(reasonsJSON string) []string {
	var rs []autonomy.Reason
	_ = json.Unmarshal([]byte(reasonsJSON), &rs)
	out := make([]string, 0, len(rs))
	for _, x := range rs {
		out = append(out, x.Code)
	}
	return out
}

func (r *Service) publishAsked(taskID, label, decisionID, reason string, codes []string) {
	if codes == nil {
		codes = []string{}
	}
	r.publish(wsevent.TypeTaskAutonomyAsked, wsevent.TaskAutonomyAsked{
		TaskID: taskID, Label: label, DecisionID: decisionID, Reason: reason, Reasons: codes,
	})
}

// Sweep processes every pending approval whose hold window has ended.
func (r *Service) Sweep(ctx context.Context) {
	if r == nil || r.q == nil {
		return
	}
	now := r.now()
	rows, err := r.q.ListDueAutonomyApprovals(ctx, &now)
	if err != nil {
		slog.Warn("autonomy policy: list due approvals failed", "err", err)
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		r.safeProcess(ctx, row)
	}
}

func (r *Service) safeProcess(ctx context.Context, row gen.AutonomyDecision) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("autonomy policy: panic", "task_id", row.TaskID, "panic", rec)
		}
	}()
	r.process(ctx, row)
}

func (r *Service) setOutcome(ctx context.Context, id, outcome, note string) {
	now := r.now()
	actor := autonomy.PolicyActor
	var np *string
	if note != "" {
		np = &note
	}
	if err := r.q.SetAutonomyOutcome(ctx, gen.SetAutonomyOutcomeParams{Outcome: &outcome, Note: np, OutcomeAt: &now, OutcomeActor: &actor, ID: id}); err != nil {
		slog.Warn("autonomy policy: set outcome failed", "decision_id", id, "err", err)
	}
}

// process acts on one pending approve decision: it approves only if the task
// is still on the gate, not paused, has no live or waiting run, and the row was
// not held in the meantime (the claim is the race-free arbiter with Hold).
func (r *Service) process(ctx context.Context, row gen.AutonomyDecision) {
	if r.approver == nil {
		return
	}
	task, err := r.q.GetTask(ctx, row.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			r.setOutcome(ctx, row.ID, autonomy.OutcomeSuperseded, "")
		} else {
			slog.Warn("autonomy policy: load task failed", "task_id", row.TaskID, "err", err)
		}
		return
	}
	if task.Archived != 0 || task.Label != row.Label {
		r.setOutcome(ctx, row.ID, autonomy.OutcomeSuperseded, "")
		return
	}
	cause := ""
	switch {
	case task.Paused != 0:
		cause = "task is paused"
	case r.approver.RunIsLive(ctx, task):
		cause = "a run is active on the task"
	case task.ActiveAgentRunID != nil:
		if run, err := r.q.GetAgentRun(ctx, *task.ActiveAgentRunID); err == nil && run.Status == "waiting_human" {
			cause = "the active run is waiting for a person"
		}
	}
	if cause != "" {
		r.setOutcome(ctx, row.ID, autonomy.OutcomeSkipped, "Auto-approval skipped: "+cause)
		r.publishAsked(task.ID, row.Label, row.ID, wsevent.AskedSkipped, nil)
		return
	}
	now := r.now()
	n, err := r.q.ClaimAutonomyApproval(ctx, gen.ClaimAutonomyApprovalParams{OutcomeAt: &now, ID: row.ID})
	if err != nil {
		slog.Warn("autonomy policy: claim failed", "decision_id", row.ID, "err", err)
		return
	}
	if n == 0 {
		return // held (or already processed)
	}
	note := ""
	if row.Note != nil {
		note = *row.Note
	}
	target, err := r.approver.Approve(ctx, task.ID, workflow.ApproveOptions{Actor: autonomy.PolicyActor, Note: note, ExpectedFrom: row.Label})
	if err != nil {
		if errors.Is(err, workflow.ErrStale) || errors.Is(err, workflow.ErrTaskNotFound) {
			r.setOutcome(ctx, row.ID, autonomy.OutcomeSuperseded, "")
			return
		}
		r.setOutcome(ctx, row.ID, autonomy.OutcomeFailed, fmt.Sprintf("Auto-approval failed: %v", err))
		r.publishAsked(task.ID, row.Label, row.ID, wsevent.AskedFailed, nil)
		return
	}
	r.publish(wsevent.TypeTaskAutonomyApproved, wsevent.TaskAutonomyApproved{
		TaskID: task.ID, DecisionID: row.ID, From: row.Label, To: target, Note: note,
	})
}
