package handlers

import (
	"database/sql"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type WorkflowsHandler struct {
	q  *gen.Queries
	db *sql.DB
}

func NewWorkflowsHandler(q *gen.Queries, db *sql.DB) *WorkflowsHandler {
	return &WorkflowsHandler{q: q, db: db}
}

type workflowResponse struct {
	gen.Workflow
	Labels      []gen.WorkflowLabel      `json:"labels"`
	Transitions []gen.WorkflowTransition `json:"transitions"`
}

func (h *WorkflowsHandler) buildResponse(r *http.Request, wf gen.Workflow) (workflowResponse, error) {
	labels, err := h.q.ListWorkflowLabels(r.Context(), wf.ID)
	if err != nil {
		return workflowResponse{}, err
	}
	transitions, err := h.q.ListWorkflowTransitions(r.Context(), wf.ID)
	if err != nil {
		return workflowResponse{}, err
	}
	return workflowResponse{Workflow: wf, Labels: labels, Transitions: transitions}, nil
}

func (h *WorkflowsHandler) List(w http.ResponseWriter, r *http.Request) {
	wfs, err := h.q.ListWorkflows(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	var out []workflowResponse
	for _, wf := range wfs {
		resp, err := h.buildResponse(r, wf)
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, resp)
	}
	JSON(w, http.StatusOK, out)
}

func (h *WorkflowsHandler) Get(w http.ResponseWriter, r *http.Request) {
	wf, err := h.q.GetWorkflow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "workflow not found")
		return
	}
	resp, err := h.buildResponse(r, wf)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, resp)
}

func (h *WorkflowsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(r, &body); err != nil || body.Name == "" {
		Err(w, http.StatusBadRequest, "name is required")
		return
	}

	wf, err := h.q.CreateWorkflow(r.Context(), gen.CreateWorkflowParams{
		ID:          uuid.NewString(),
		Name:        body.Name,
		Description: body.Description,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, wf)
}

func (h *WorkflowsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Labels      []struct {
			Name        string `json:"name"`
			Color       string `json:"color"`
			SortOrder   int64  `json:"sort_order"`
			AgentIgnore bool   `json:"agent_ignore"`
			IsTerminal  bool   `json:"is_terminal"`
		} `json:"labels"`
		Transitions []struct {
			FromLabel     string  `json:"from_label"`
			ToLabel       string  `json:"to_label"`
			TriggerType   string  `json:"trigger_type"`
			AgentConfigID *string `json:"agent_config_id"`
			Path          *string `json:"path"`
		} `json:"transitions"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wfID := chi.URLParam(r, "id")
	wf, err := h.q.UpdateWorkflow(r.Context(), gen.UpdateWorkflowParams{
		Name:        body.Name,
		Description: body.Description,
		ID:          wfID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Replace labels and transitions atomically inside a transaction.
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := gen.New(tx)

	if err := tq.DeleteWorkflowLabels(r.Context(), wfID); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, l := range body.Labels {
		agentIgnore := int64(0)
		if l.AgentIgnore {
			agentIgnore = 1
		}
		isTerminal := int64(0)
		if l.IsTerminal {
			isTerminal = 1
		}
		if _, err := tq.CreateWorkflowLabel(r.Context(), gen.CreateWorkflowLabelParams{
			ID:          uuid.NewString(),
			WorkflowID:  wfID,
			Name:        l.Name,
			Color:       l.Color,
			SortOrder:   l.SortOrder,
			AgentIgnore: agentIgnore,
			IsTerminal:  isTerminal,
		}); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := tq.DeleteWorkflowTransitions(r.Context(), wfID); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, t := range body.Transitions {
		if _, err := tq.CreateWorkflowTransition(r.Context(), gen.CreateWorkflowTransitionParams{
			ID:            uuid.NewString(),
			WorkflowID:    wfID,
			FromLabel:     t.FromLabel,
			ToLabel:       t.ToLabel,
			TriggerType:   t.TriggerType,
			AgentConfigID: t.AgentConfigID,
			Path:          t.Path,
		}); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp, err := h.buildResponse(r, wf)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, resp)
}

func (h *WorkflowsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteWorkflow(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
