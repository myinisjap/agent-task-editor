package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type AgentsHandler struct {
	q *gen.Queries
}

func NewAgentsHandler(q *gen.Queries) *AgentsHandler {
	return &AgentsHandler{q: q}
}

func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	configs, err := h.q.ListAgentConfigs(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, configs)
}

func (h *AgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.q.GetAgentConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	JSON(w, http.StatusOK, cfg)
}

func (h *AgentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Provider     string `json:"provider"`
		Model        string `json:"model"`
		SystemPrompt string `json:"system_prompt"`
		Labels       string `json:"labels"`
		Env          string `json:"env"`
		MaxTokens    int64  `json:"max_tokens"`
		TimeoutSecs  int64  `json:"timeout_secs"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || body.Provider == "" || body.Model == "" {
		Err(w, http.StatusBadRequest, "name, provider, and model are required")
		return
	}
	if body.Labels == "" {
		body.Labels = "[]"
	}
	if body.Env == "" {
		body.Env = "{}"
	}
	if body.MaxTokens == 0 {
		body.MaxTokens = 8192
	}
	if body.TimeoutSecs == 0 {
		body.TimeoutSecs = 600
	}

	cfg, err := h.q.CreateAgentConfig(r.Context(), gen.CreateAgentConfigParams{
		ID:           uuid.NewString(),
		Name:         body.Name,
		Provider:     body.Provider,
		Model:        body.Model,
		SystemPrompt: body.SystemPrompt,
		Labels:       body.Labels,
		Env:          body.Env,
		MaxTokens:    body.MaxTokens,
		TimeoutSecs:  body.TimeoutSecs,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, cfg)
}

func (h *AgentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Provider     string `json:"provider"`
		Model        string `json:"model"`
		SystemPrompt string `json:"system_prompt"`
		Labels       string `json:"labels"`
		Env          string `json:"env"`
		MaxTokens    int64  `json:"max_tokens"`
		TimeoutSecs  int64  `json:"timeout_secs"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cfg, err := h.q.UpdateAgentConfig(r.Context(), gen.UpdateAgentConfigParams{
		Name:         body.Name,
		Provider:     body.Provider,
		Model:        body.Model,
		SystemPrompt: body.SystemPrompt,
		Labels:       body.Labels,
		Env:          body.Env,
		MaxTokens:    body.MaxTokens,
		TimeoutSecs:  body.TimeoutSecs,
		ID:           chi.URLParam(r, "id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, cfg)
}

func (h *AgentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteAgentConfig(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
