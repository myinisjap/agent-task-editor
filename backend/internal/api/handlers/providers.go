package handlers

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ProviderConfigsHandler owns CRUD for Provider Configs: the provider/model/
// env (API key) triple that Agent Configs and Chat Sessions reference by id.
// Splitting this out of Agent Config lets Chat Sessions reuse the same
// provider/API-key setup without duplicating env vars per feature.
type ProviderConfigsHandler struct {
	q *gen.Queries
}

func NewProviderConfigsHandler(q *gen.Queries) *ProviderConfigsHandler {
	return &ProviderConfigsHandler{q: q}
}

func (h *ProviderConfigsHandler) List(w http.ResponseWriter, r *http.Request) {
	configs, err := h.q.ListProviderConfigs(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if configs == nil {
		configs = []gen.ProviderConfig{}
	}
	JSON(w, http.StatusOK, configs)
}

func (h *ProviderConfigsHandler) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.q.GetProviderConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "provider config not found")
		return
	}
	JSON(w, http.StatusOK, cfg)
}

func (h *ProviderConfigsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Env      string `json:"env"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || body.Provider == "" {
		Err(w, http.StatusBadRequest, "name and provider are required")
		return
	}
	if !knownProviders[body.Provider] {
		Err(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q; valid: claude, anthropic, llm, opencode, qwen_code, gemini_cli, codex_cli", body.Provider))
		return
	}
	if body.Env == "" {
		body.Env = "{}"
	}
	cfg, err := h.q.CreateProviderConfig(r.Context(), gen.CreateProviderConfigParams{
		ID:       uuid.NewString(),
		Name:     body.Name,
		Provider: body.Provider,
		Model:    body.Model,
		Env:      body.Env,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, cfg)
}

func (h *ProviderConfigsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Env      string `json:"env"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Provider != "" && !knownProviders[body.Provider] {
		Err(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q; valid: claude, anthropic, llm, opencode, qwen_code, gemini_cli, codex_cli", body.Provider))
		return
	}

	existing, err := h.q.GetProviderConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "provider config not found")
		return
	}
	if body.Name == "" {
		body.Name = existing.Name
	}
	if body.Provider == "" {
		body.Provider = existing.Provider
	}
	if body.Env == "" {
		body.Env = existing.Env
	}

	cfg, err := h.q.UpdateProviderConfig(r.Context(), gen.UpdateProviderConfigParams{
		Name:     body.Name,
		Provider: body.Provider,
		Model:    body.Model,
		Env:      body.Env,
		ID:       chi.URLParam(r, "id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, cfg)
}

// Delete blocks deletion (409) if any agent config or chat session still
// references this provider config — removing it out from under them would
// silently break dispatch/chat.
func (h *ProviderConfigsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if n, err := h.q.CountAgentConfigsByProviderConfig(r.Context(), id); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	} else if n > 0 {
		Err(w, http.StatusConflict, fmt.Sprintf("provider config is used by %d agent config(s)", n))
		return
	}
	if n, err := h.q.CountChatSessionsByProviderConfig(r.Context(), id); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	} else if n > 0 {
		Err(w, http.StatusConflict, fmt.Sprintf("provider config is used by %d chat session(s)", n))
		return
	}
	if err := h.q.DeleteProviderConfig(r.Context(), id); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
