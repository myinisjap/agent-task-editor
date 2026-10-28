package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// AutonomyRulesHandler serves ask-rule CRUD and a gate's autonomy history.
type AutonomyRulesHandler struct{ q *gen.Queries }

func NewAutonomyRulesHandler(q *gen.Queries) *AutonomyRulesHandler {
	return &AutonomyRulesHandler{q: q}
}

type askRuleResponse struct {
	ID              string     `json:"id"`
	RepoID          *string    `json:"repo_id"`
	WorkflowID      string     `json:"workflow_id"`
	Label           *string    `json:"label"`
	PathGlobs       []string   `json:"path_globs"`
	TaskType        *string    `json:"task_type"`
	MinChangedLines *int64     `json:"min_changed_lines"`
	CreatedBy       string     `json:"created_by"`
	SourceTaskID    *string    `json:"source_task_id"`
	CreatedAt       time.Time  `json:"created_at"`
	HitCount        int64      `json:"hit_count"`
	LastHitAt       *time.Time `json:"last_hit_at"`
	Summary         string     `json:"summary"`
}

func toAskRuleResponse(r gen.AutonomyAskRule) askRuleResponse {
	globs := []string{}
	_ = json.Unmarshal([]byte(r.PathGlobs), &globs)
	if globs == nil {
		globs = []string{}
	}
	ar := autonomy.AskRule{ID: r.ID, RepoID: r.RepoID, Label: r.Label, PathGlobs: globs, TaskType: r.TaskType}
	if r.MinChangedLines != nil {
		n := int(*r.MinChangedLines)
		ar.MinChangedLines = &n
	}
	return askRuleResponse{
		ID: r.ID, RepoID: r.RepoID, WorkflowID: r.WorkflowID, Label: r.Label, PathGlobs: globs, TaskType: r.TaskType,
		MinChangedLines: r.MinChangedLines, CreatedBy: r.CreatedBy, SourceTaskID: r.SourceTaskID, CreatedAt: r.CreatedAt,
		HitCount: r.HitCount, LastHitAt: r.LastHitAt, Summary: ar.Summary(),
	}
}

// askRuleInput is the rule condition body shared by POST, should-have-asked
// and backtest extra_rules. workflow_id/repo_id/label/source_task_id are only
// used by the CRUD endpoints (and repo_id by backtest extra_rules).
type askRuleInput struct {
	WorkflowID      string   `json:"workflow_id"`
	RepoID          *string  `json:"repo_id"`
	Label           *string  `json:"label"`
	PathGlobs       []string `json:"path_globs"`
	TaskType        *string  `json:"task_type"`
	MinChangedLines *int     `json:"min_changed_lines"`
	SourceTaskID    *string  `json:"source_task_id"`
}

func (in askRuleInput) toRule() autonomy.AskRule {
	return autonomy.AskRule{RepoID: in.RepoID, Label: in.Label, PathGlobs: in.PathGlobs, TaskType: in.TaskType, MinChangedLines: in.MinChangedLines}
}

func ptrInt64(n *int) *int64 {
	if n == nil {
		return nil
	}
	v := int64(*n)
	return &v
}

func emptyToNil(s *string) *string {
	if s != nil && *s == "" {
		return nil
	}
	return s
}

func globsJSON(g []string) string {
	if g == nil {
		g = []string{}
	}
	b, _ := json.Marshal(g)
	return string(b)
}

// validateRuleScope checks that the workflow, label and repo a rule is scoped
// to exist. It returns an error message ("" when fine).
func validateRuleScope(ctx context.Context, q *gen.Queries, workflowID string, label, repoID *string) string {
	if workflowID == "" {
		return "workflow_id is required"
	}
	if _, err := q.GetWorkflow(ctx, workflowID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "workflow not found"
		}
		return err.Error()
	}
	if label != nil {
		labels, err := q.ListWorkflowLabels(ctx, workflowID)
		if err != nil {
			return err.Error()
		}
		found := false
		for _, l := range labels {
			found = found || l.Name == *label
		}
		if !found {
			return "label is not a label of that workflow"
		}
	}
	if repoID != nil {
		if _, err := q.GetRepo(ctx, *repoID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "repo not found"
			}
			return err.Error()
		}
	}
	return ""
}

// List returns ask rules. The label and repo filters also return the
// "all gates" / "all repos" rules, because those apply too.
//
// GET /api/v1/autonomy/ask-rules?workflow_id=&label=&repo_id=
func (h *AutonomyRulesHandler) List(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	rows, err := h.q.ListAskRules(r.Context(), gen.ListAskRulesParams{
		Column1: qs.Get("workflow_id"), Column2: qs.Get("label"), Column3: qs.Get("repo_id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]askRuleResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toAskRuleResponse(row))
	}
	JSON(w, http.StatusOK, out)
}

// Create adds an ask rule.
//
// POST /api/v1/autonomy/ask-rules
func (h *AutonomyRulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in askRuleInput
	if err := decode(r, &in); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.Label, in.RepoID = emptyToNil(in.Label), emptyToNil(in.RepoID)
	if msg := validateRuleScope(ctx, h.q, in.WorkflowID, in.Label, in.RepoID); msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	rule := in.toRule()
	if err := rule.Validate(); err != nil {
		Err(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.SourceTaskID != nil {
		if _, err := h.q.GetTask(ctx, *in.SourceTaskID); err != nil {
			Err(w, http.StatusBadRequest, "source_task_id: task not found")
			return
		}
	}
	row, err := h.q.CreateAskRule(ctx, gen.CreateAskRuleParams{
		ID: uuid.NewString(), RepoID: in.RepoID, WorkflowID: in.WorkflowID, Label: in.Label,
		PathGlobs: globsJSON(rule.PathGlobs), TaskType: rule.TaskType, MinChangedLines: ptrInt64(rule.MinChangedLines),
		CreatedBy: middleware.ActorFromContext(ctx), SourceTaskID: in.SourceTaskID, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, toAskRuleResponse(row))
}

// Update changes a rule. Only the fields present in the body change; an
// explicit null clears repo_id, label, task_type or min_changed_lines.
// workflow_id cannot change.
//
// PATCH /api/v1/autonomy/ask-rules/{id}
func (h *AutonomyRulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur, err := h.q.GetAskRule(ctx, chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "ask rule not found")
		} else {
			Err(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	var raw map[string]json.RawMessage
	if err := decode(r, &raw); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Start from the stored values.
	var globs []string
	_ = json.Unmarshal([]byte(cur.PathGlobs), &globs)
	p := gen.UpdateAskRuleParams{RepoID: cur.RepoID, Label: cur.Label, TaskType: cur.TaskType, MinChangedLines: cur.MinChangedLines, ID: cur.ID}
	bad := func(field string) {
		Err(w, http.StatusBadRequest, "invalid value for "+field)
	}
	for key, val := range raw {
		var err error
		switch key {
		case "workflow_id":
			var wf string
			if err = json.Unmarshal(val, &wf); err != nil || wf != cur.WorkflowID {
				Err(w, http.StatusBadRequest, "workflow_id cannot be changed")
				return
			}
		case "repo_id":
			err = json.Unmarshal(val, &p.RepoID)
		case "label":
			err = json.Unmarshal(val, &p.Label)
		case "task_type":
			err = json.Unmarshal(val, &p.TaskType)
		case "min_changed_lines":
			err = json.Unmarshal(val, &p.MinChangedLines)
		case "path_globs":
			globs = nil
			err = json.Unmarshal(val, &globs)
		}
		if err != nil {
			bad(key)
			return
		}
	}
	p.RepoID, p.Label = emptyToNil(p.RepoID), emptyToNil(p.Label)
	if msg := validateRuleScope(ctx, h.q, cur.WorkflowID, p.Label, p.RepoID); msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	rule := autonomy.AskRule{PathGlobs: globs, TaskType: p.TaskType}
	if p.MinChangedLines != nil {
		n := int(*p.MinChangedLines)
		rule.MinChangedLines = &n
	}
	if err := rule.Validate(); err != nil {
		Err(w, http.StatusBadRequest, err.Error())
		return
	}
	p.PathGlobs, p.TaskType, p.MinChangedLines = globsJSON(rule.PathGlobs), rule.TaskType, ptrInt64(rule.MinChangedLines)
	row, err := h.q.UpdateAskRule(ctx, p)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toAskRuleResponse(row))
}

// Delete removes a rule.
//
// DELETE /api/v1/autonomy/ask-rules/{id}
func (h *AutonomyRulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	n, err := h.q.DeleteAskRule(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		Err(w, http.StatusNotFound, "ask rule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type autonomyChangeResponse struct {
	ID         string    `json:"id"`
	WorkflowID string    `json:"workflow_id"`
	Label      string    `json:"label"`
	FromMode   string    `json:"from_mode"`
	ToMode     string    `json:"to_mode"`
	Actor      string    `json:"actor"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

func toAutonomyChangeResponse(c gen.AutonomyChange) autonomyChangeResponse {
	return autonomyChangeResponse{ID: c.ID, WorkflowID: c.WorkflowID, Label: c.Label, FromMode: c.FromMode, ToMode: c.ToMode,
		Actor: c.Actor, Reason: c.Reason, CreatedAt: c.CreatedAt}
}

type autonomyEscapeResponse struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	TaskTitle  string    `json:"task_title"`
	Source     string    `json:"source"`
	DecisionID *string   `json:"decision_id"`
	Actor      *string   `json:"actor"`
	Note       *string   `json:"note"`
	RuleID     *string   `json:"rule_id"`
	CreatedAt  time.Time `json:"created_at"`
	// Counted: still counts toward the step-down threshold (inside the window
	// and not consumed by a mode change).
	Counted bool `json:"counted"`
}

func toAutonomyEscapeResponse(e gen.AutonomyEscape, title string, counted bool) autonomyEscapeResponse {
	return autonomyEscapeResponse{ID: e.ID, TaskID: e.TaskID, TaskTitle: title, Source: e.Source, DecisionID: e.DecisionID,
		Actor: e.Actor, Note: e.Note, RuleID: e.RuleID, CreatedAt: e.CreatedAt, Counted: counted}
}

const historyLimit = 200

// History returns a gate's mode changes and escapes, newest first.
//
// GET /api/v1/workflows/{id}/labels/{label}/autonomy/history
func (h *AutonomyRulesHandler) History(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	wfID, label := chi.URLParam(r, "id"), chi.URLParam(r, "label")
	if _, err := h.q.GetWorkflow(ctx, wfID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "workflow not found")
		} else {
			Err(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	settings, err := h.q.GetAutonomySettings(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	var currentMode *string
	if labels, err := h.q.ListWorkflowLabels(ctx, wfID); err == nil {
		for _, l := range labels {
			if l.Name == label {
				m := l.AutonomyMode
				currentMode = &m
			}
		}
	}
	changes, err := h.q.ListAutonomyChanges(ctx, gen.ListAutonomyChangesParams{WorkflowID: wfID, Label: label, Limit: historyLimit})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	escapes, err := h.q.ListGateEscapes(ctx, gen.ListGateEscapesParams{WorkflowID: wfID, Label: label, Limit: historyLimit})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	since := time.Now().UTC().Add(-time.Duration(settings.StepDownWindowDays) * 24 * time.Hour)
	counted, err := h.q.CountGateEscapesForStepDown(ctx, gen.CountGateEscapesForStepDownParams{WorkflowID: wfID, Label: label, CreatedAt: since})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	chOut := make([]autonomyChangeResponse, 0, len(changes))
	for _, c := range changes {
		chOut = append(chOut, toAutonomyChangeResponse(c))
	}
	esOut := make([]autonomyEscapeResponse, 0, len(escapes))
	for _, e := range escapes {
		isCounted := e.ResetByChangeID == nil && !e.CreatedAt.Before(since)
		esOut = append(esOut, toAutonomyEscapeResponse(gen.AutonomyEscape{
			ID: e.ID, TaskID: e.TaskID, WorkflowID: e.WorkflowID, Label: e.Label, Source: e.Source, CreatedAt: e.CreatedAt,
			DecisionID: e.DecisionID, Actor: e.Actor, Note: e.Note, RuleID: e.RuleID, ResetByChangeID: e.ResetByChangeID,
		}, e.TaskTitle, isCounted))
	}
	JSON(w, http.StatusOK, map[string]any{
		"workflow_id": wfID, "label": label, "current_mode": currentMode,
		"changes": chOut, "escapes": esOut,
		"step_down": map[string]any{"escapes_counted": counted, "threshold": settings.StepDownEscapes, "window_days": settings.StepDownWindowDays},
	})
}

// AutonomySettingsHandler manages the DB-backed escape step-down setting
// (ADR 0031 pattern, like cost-warning settings).
type AutonomySettingsHandler struct{ q *gen.Queries }

func NewAutonomySettingsHandler(q *gen.Queries) *AutonomySettingsHandler {
	return &AutonomySettingsHandler{q: q}
}

type autonomySettingsResponse struct {
	StepDownEscapes    int64     `json:"step_down_escapes"`
	StepDownWindowDays int64     `json:"step_down_window_days"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Get returns the step-down settings.
//
// GET /api/v1/settings/autonomy
func (h *AutonomySettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	row, err := h.q.GetAutonomySettings(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, autonomySettingsResponse{row.StepDownEscapes, row.StepDownWindowDays, row.UpdatedAt})
}

// Update validates and stores the step-down settings. 0 escapes disables the
// automatic step-down.
//
// PUT /api/v1/settings/autonomy
func (h *AutonomySettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StepDownEscapes    *int64 `json:"step_down_escapes"`
		StepDownWindowDays *int64 `json:"step_down_window_days"`
	}
	if err := decode(r, &body); err != nil || body.StepDownEscapes == nil || body.StepDownWindowDays == nil {
		Err(w, http.StatusBadRequest, "step_down_escapes and step_down_window_days are required")
		return
	}
	if *body.StepDownEscapes < 0 || *body.StepDownEscapes > 100 {
		Err(w, http.StatusBadRequest, "step_down_escapes must be between 0 and 100 (0 = never step down)")
		return
	}
	if *body.StepDownWindowDays < 1 || *body.StepDownWindowDays > 365 {
		Err(w, http.StatusBadRequest, "step_down_window_days must be between 1 and 365")
		return
	}
	row, err := h.q.UpsertAutonomySettings(r.Context(), gen.UpsertAutonomySettingsParams{
		StepDownEscapes: *body.StepDownEscapes, StepDownWindowDays: *body.StepDownWindowDays,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, autonomySettingsResponse{row.StepDownEscapes, row.StepDownWindowDays, row.UpdatedAt})
}
