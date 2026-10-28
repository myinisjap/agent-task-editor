package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/escape"
	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// AutonomyHandler serves autonomy decisions.
type AutonomyHandler struct{ q *gen.Queries }

func NewAutonomyHandler(q *gen.Queries) *AutonomyHandler { return &AutonomyHandler{q: q} }

type autonomyDecisionResponse struct {
	ID         string            `json:"id"`
	TaskID     string            `json:"task_id"`
	WorkflowID string            `json:"workflow_id"`
	Label      string            `json:"label"`
	RunID      *string           `json:"run_id"`
	Mode       string            `json:"mode"`
	Decision   string            `json:"decision"`
	Reasons    []autonomy.Reason `json:"reasons"`
	CreatedAt  time.Time         `json:"created_at"`
	// Outcome is null for ask decisions; see autonomy.Outcome*.
	Outcome      *string    `json:"outcome"`
	HoldSeconds  *int64     `json:"hold_seconds"`
	DueAt        *time.Time `json:"due_at"`
	Note         *string    `json:"note"`
	OutcomeAt    *time.Time `json:"outcome_at"`
	OutcomeActor *string    `json:"outcome_actor"`
}

func toAutonomyDecisionResponse(d gen.AutonomyDecision) autonomyDecisionResponse {
	reasons := []autonomy.Reason{}
	_ = json.Unmarshal([]byte(d.Reasons), &reasons)
	if reasons == nil {
		reasons = []autonomy.Reason{}
	}
	return autonomyDecisionResponse{
		ID: d.ID, TaskID: d.TaskID, WorkflowID: d.WorkflowID, Label: d.Label, RunID: d.RunID,
		Mode: d.Mode, Decision: d.Decision, Reasons: reasons, CreatedAt: d.CreatedAt,
		Outcome: d.Outcome, HoldSeconds: d.HoldSeconds, DueAt: d.DueAt, Note: d.Note,
		OutcomeAt: d.OutcomeAt, OutcomeActor: d.OutcomeActor,
	}
}

// GetDecision returns the latest recorded decision for the task's current label.
func (h *AutonomyHandler) GetDecision(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "task not found")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	d, err := h.q.GetLatestAutonomyDecision(r.Context(), gen.GetLatestAutonomyDecisionParams{TaskID: task.ID, Label: task.Label})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "no autonomy decision for the task's current label")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toAutonomyDecisionResponse(d))
}

// SetAlwaysAsk sets the per-task always_ask override (a hard stop for autonomy).
func (h *TasksHandler) SetAlwaysAsk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AlwaysAsk bool `json:"always_ask"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	v := int64(0)
	if body.AlwaysAsk {
		v = 1
	}
	id := chi.URLParam(r, "id")
	n, err := h.q.SetTaskAlwaysAsk(r.Context(), gen.SetTaskAlwaysAskParams{AlwaysAsk: v, ID: id})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	task, err := h.q.GetTask(r.Context(), id)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toTaskResponse(task))
}

func (h *TasksHandler) publishAutonomy(eventType string, payload any) {
	p := h.apub
	if p == nil {
		p = h.pub
	}
	if p != nil {
		p.Publish(eventType, payload)
	}
}

// HoldAutonomy cancels the task's pending auto-approval so it becomes a normal
// review. The conditional UPDATE is the race-free arbiter against the policy
// sweeper's claim.
func (h *TasksHandler) HoldAutonomy(w http.ResponseWriter, r *http.Request) {
	task, err := h.q.GetTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	actor := middleware.ActorFromContext(r.Context())
	now := time.Now().UTC()
	n, err := h.q.HoldPendingAutonomy(r.Context(), gen.HoldPendingAutonomyParams{OutcomeAt: &now, OutcomeActor: &actor, TaskID: task.ID, Label: task.Label})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		Err(w, http.StatusConflict, "no pending auto-approval for this task (if it was already approved, use undo)")
		return
	}
	d, err := h.q.GetLatestAutonomyDecision(r.Context(), gen.GetLatestAutonomyDecisionParams{TaskID: task.ID, Label: task.Label})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.publishAutonomy(wsevent.TypeTaskAutonomyHeld, wsevent.TaskAutonomyHeld{TaskID: task.ID, Label: task.Label, DecisionID: d.ID, Actor: actor})
	h.publishAutonomy(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: task.ID})
	JSON(w, http.StatusOK, toAutonomyDecisionResponse(d))
}

type autonomyUndoResponse struct {
	Task       taskResponse `json:"task"`
	PRLeftOpen bool         `json:"pr_left_open"`
	Message    string       `json:"message"`
}

// UndoAutonomy moves a policy-approved task back to the gate it left.
func (h *TasksHandler) UndoAutonomy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, err := h.q.GetTask(ctx, chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	if task.Archived != 0 {
		Err(w, http.StatusConflict, "task is archived")
		return
	}
	labels, err := h.q.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	transitions, err := h.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	history, err := h.q.ListTaskLabelHistory(ctx, task.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	gate, ok, reason := needsme.CanUndo(task, history, needsme.WorkflowDef{Labels: labels, Transitions: transitions})
	if !ok {
		Err(w, http.StatusConflict, reason)
		return
	}
	if h.engine.RunIsLive(ctx, task) {
		Err(w, http.StatusConflict, errRunLive.Error())
		return
	}
	previous := task.Label
	actor := middleware.ActorFromContext(ctx)
	if err := h.engine.MoveBack(ctx, task.ID, gate, actor, autonomy.UndoNote, previous); err != nil {
		handleTransitionError(w, err)
		return
	}
	// A create_pr side effect that is still pending becomes a no-op: the outbox
	// re-checks that the task's current label has create_pr before running it.
	// Record the escape against the policy approval (if any) so the step-down
	// counter counts one bad approval once however it is reported.
	esc := escape.Input{TaskID: task.ID, WorkflowID: task.WorkflowID, Label: gate, Source: autonomy.EscapeSourceUndo, Actor: &actor}
	d, derr := h.q.GetLatestApprovedAutonomyDecision(ctx, task.ID)
	if derr == nil {
		esc.DecisionID = &d.ID
	}
	if _, _, err := h.recorder().Record(ctx, esc); err != nil {
		slog.Warn("autonomy undo: record escape failed", "task_id", task.ID, "err", err)
	}
	if derr == nil {
		now := time.Now().UTC()
		out := autonomy.OutcomeUndone
		if err := h.q.SetAutonomyOutcome(ctx, gen.SetAutonomyOutcomeParams{Outcome: &out, OutcomeAt: &now, OutcomeActor: &actor, ID: d.ID}); err != nil {
			slog.Warn("autonomy undo: mark decision undone failed", "task_id", task.ID, "err", err)
		}
	}
	updated, err := h.q.GetTask(ctx, task.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.publishAutonomy(wsevent.TypeTaskAutonomyUndone, wsevent.TaskAutonomyUndone{TaskID: task.ID, Label: gate, From: previous, PRURL: updated.PrUrl})
	msg := fmt.Sprintf("Moved back to %s.", gate)
	if updated.PrUrl != "" {
		msg = fmt.Sprintf("Moved back to %s. The pull request %s was already opened and was left as is.", gate, updated.PrUrl)
	}
	JSON(w, http.StatusOK, autonomyUndoResponse{Task: toTaskResponse(updated), PRLeftOpen: updated.PrUrl != "", Message: msg})
}

// recorder returns the wired escape recorder, or a fallback built from the
// handler's own publisher (tests construct handlers without the router).
func (h *TasksHandler) recorder() *escape.Recorder {
	if h.rec != nil {
		return h.rec
	}
	p := h.apub
	if p == nil {
		p = h.pub
	}
	var ep escape.Publisher
	if p != nil {
		ep = p
	}
	return escape.New(h.q, ep)
}
