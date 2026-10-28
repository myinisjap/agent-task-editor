// Package escape records autonomy escapes (a policy-approved task that should
// have stopped for a person) and steps a gate's mode down when enough of them
// accumulate. It never steps a gate up.
package escape

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// Publisher publishes WS events.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Recorder records escapes and applies the step-down.
type Recorder struct {
	q   *gen.Queries
	pub Publisher
	now func() time.Time
}

// New creates a Recorder. pub may be nil.
func New(q *gen.Queries, pub Publisher) *Recorder {
	return &Recorder{q: q, pub: pub, now: func() time.Time { return time.Now().UTC() }}
}

// SetNow overrides the clock (tests).
func (r *Recorder) SetNow(f func() time.Time) { r.now = f }

func (r *Recorder) publish(t string, p any) {
	if r != nil && r.pub != nil {
		r.pub.Publish(t, p)
	}
}

// Input describes one escape.
type Input struct {
	TaskID, WorkflowID, Label, Source string
	DecisionID, Actor, Note, RuleID   *string
}

// Insert writes the escape row using tq (which may be a transaction) without
// checking the step-down.
func (r *Recorder) Insert(ctx context.Context, tq *gen.Queries, in Input) (gen.AutonomyEscape, error) {
	if tq == nil {
		tq = r.q
	}
	return tq.CreateAutonomyEscape(ctx, gen.CreateAutonomyEscapeParams{
		ID: uuid.NewString(), TaskID: in.TaskID, WorkflowID: in.WorkflowID, Label: in.Label, Source: in.Source,
		DecisionID: in.DecisionID, Actor: in.Actor, Note: in.Note, RuleID: in.RuleID, CreatedAt: r.now(),
	})
}

// Record inserts an escape and then checks the step-down.
func (r *Recorder) Record(ctx context.Context, in Input) (gen.AutonomyEscape, *gen.AutonomyChange, error) {
	e, err := r.Insert(ctx, nil, in)
	if err != nil {
		return e, nil, err
	}
	ch, err := r.CheckStepDown(ctx, in.WorkflowID, in.Label)
	if err != nil {
		slog.Warn("autonomy step-down check failed", "workflow_id", in.WorkflowID, "label", in.Label, "err", err)
	}
	return e, ch, nil
}

// ResetCounter marks every uncounted escape of a gate as consumed by the change,
// so the step-down counter restarts. (created_at has one-second precision, so
// "since the last change" cannot be a timestamp comparison.)
func ResetCounter(ctx context.Context, q *gen.Queries, workflowID, label, changeID string) error {
	return q.ResetGateEscapes(ctx, gen.ResetGateEscapesParams{ResetByChangeID: &changeID, WorkflowID: workflowID, Label: label})
}

// CheckStepDown lowers the gate one notch when it has reached the configured
// number of distinct escapes within the window. It returns the change row, or
// nil when nothing changed.
func (r *Recorder) CheckStepDown(ctx context.Context, workflowID, label string) (*gen.AutonomyChange, error) {
	settings, err := r.q.GetAutonomySettings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.StepDownEscapes <= 0 {
		return nil, nil
	}
	labels, err := r.q.ListWorkflowLabels(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	var lbl *gen.WorkflowLabel
	for i := range labels {
		if labels[i].Name == label {
			lbl = &labels[i]
		}
	}
	if lbl == nil {
		return nil, nil
	}
	from, err := autonomy.ParseMode(lbl.AutonomyMode)
	if err != nil {
		return nil, nil
	}
	to, ok := autonomy.StepDownMode(from)
	if !ok {
		return nil, nil
	}
	now := r.now()
	since := now.Add(-time.Duration(settings.StepDownWindowDays) * 24 * time.Hour)
	arg := gen.CountGateEscapesForStepDownParams{WorkflowID: workflowID, Label: label, CreatedAt: since}
	n, err := r.q.CountGateEscapesForStepDown(ctx, arg)
	if err != nil {
		return nil, err
	}
	if n < settings.StepDownEscapes {
		return nil, nil
	}
	sources, _ := r.q.ListGateEscapeSourcesForStepDown(ctx, gen.ListGateEscapeSourcesForStepDownParams(arg))
	askOn := lbl.AutonomyAskOn
	filled := false
	if from == autonomy.ModeAuto && to == autonomy.ModeOnFlags && (strings.TrimSpace(askOn) == "" || askOn == "[]" || askOn == "null") {
		// on_flags with no ask_on codes behaves like auto; make the step real.
		askOn = `["` + strings.Join(reviewbrief.FlagCodes, `","`) + `"]`
		filled = true
	}
	// The next three statements are not in a transaction on purpose: the
	// conditional UPDATE (only if the mode is still `from`) is the arbiter, so
	// two concurrent escapes cannot step the same gate twice.
	rows, err := r.q.StepDownLabelAutonomy(ctx, gen.StepDownLabelAutonomyParams{
		AutonomyMode: string(to), AutonomyAskOn: askOn, WorkflowID: workflowID, Name: label, AutonomyMode_2: string(from),
	})
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, nil
	}
	reason := fmt.Sprintf("%d escapes in %d days", n, settings.StepDownWindowDays)
	if len(sources) > 0 {
		reason += " (" + strings.Join(sources, ", ") + ")"
	}
	if filled {
		reason += "; ask_on set to all flags"
	}
	ch, err := r.q.CreateAutonomyChange(ctx, gen.CreateAutonomyChangeParams{
		ID: uuid.NewString(), WorkflowID: workflowID, Label: label, FromMode: string(from), ToMode: string(to),
		Actor: autonomy.PolicyActor, Reason: reason, CreatedAt: now,
	})
	if err != nil {
		return nil, err
	}
	if err := ResetCounter(ctx, r.q, workflowID, label, ch.ID); err != nil {
		slog.Warn("autonomy step-down: reset counter failed", "err", err)
	}
	// The policy does not re-check the gate's mode when a hold ends, so cancel
	// pending auto-approvals here and tell a person.
	note := fmt.Sprintf("Auto-approval cancelled: gate stepped down to %s", to)
	pend, err := r.q.SupersedePendingAtGate(ctx, gen.SupersedePendingAtGateParams{Note: &note, OutcomeAt: &now, WorkflowID: workflowID, Label: label})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("autonomy step-down: supersede pending failed", "err", err)
	}
	for _, p := range pend {
		r.publish(wsevent.TypeTaskAutonomyAsked, wsevent.TaskAutonomyAsked{
			TaskID: p.TaskID, Label: label, DecisionID: p.ID, Reason: wsevent.AskedSteppedDown, Reasons: []string{},
		})
		r.publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: p.TaskID})
	}
	r.publish(wsevent.TypeAutonomySteppedDown, wsevent.AutonomySteppedDown{
		WorkflowID: workflowID, Label: label, FromMode: string(from), ToMode: string(to),
		Actor: autonomy.PolicyActor, Reason: reason, Escapes: int(n), ChangeID: ch.ID,
	})
	return &ch, nil
}
