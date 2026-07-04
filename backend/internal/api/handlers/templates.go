package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TemplatesHandler manages task templates: reusable pre-filled
// title/description/type snippets for recurring shapes of work.
type TemplatesHandler struct {
	q *gen.Queries
}

func NewTemplatesHandler(q *gen.Queries) *TemplatesHandler {
	return &TemplatesHandler{q: q}
}

func (h *TemplatesHandler) List(w http.ResponseWriter, r *http.Request) {
	templates, err := h.q.ListTaskTemplates(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if templates == nil {
		templates = []gen.TaskTemplate{}
	}
	JSON(w, http.StatusOK, templates)
}

func (h *TemplatesHandler) Get(w http.ResponseWriter, r *http.Request) {
	tpl, err := h.q.GetTaskTemplate(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "template not found")
		return
	}
	JSON(w, http.StatusOK, tpl)
}

// templateBody is the create/update request payload.
type templateBody struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

func (h *TemplatesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body templateBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		Err(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.Type == "" {
		body.Type = "feature"
	}
	tpl, err := h.q.CreateTaskTemplate(r.Context(), gen.CreateTaskTemplateParams{
		ID:          uuid.NewString(),
		Name:        body.Name,
		Title:       body.Title,
		Description: body.Description,
		Type:        body.Type,
	})
	if err != nil {
		// The UNIQUE constraint on name is the only expected insert failure.
		Err(w, http.StatusConflict, "a template with that name already exists")
		return
	}
	JSON(w, http.StatusCreated, tpl)
}

func (h *TemplatesHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body templateBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" {
		Err(w, http.StatusBadRequest, "name is required")
		return
	}
	if body.Type == "" {
		body.Type = "feature"
	}
	tpl, err := h.q.UpdateTaskTemplate(r.Context(), gen.UpdateTaskTemplateParams{
		Name:        body.Name,
		Title:       body.Title,
		Description: body.Description,
		Type:        body.Type,
		ID:          chi.URLParam(r, "id"),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "template not found")
			return
		}
		Err(w, http.StatusConflict, "a template with that name already exists")
		return
	}
	JSON(w, http.StatusOK, tpl)
}

func (h *TemplatesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteTaskTemplate(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
