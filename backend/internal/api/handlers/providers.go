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

// List returns a page of provider configs, newest first. Query parameters:
//   - limit: page size (default 200, capped at 500)
//   - after: cursor (the id of the last config from the previous page)
//
// The body is a plain JSON array. When more configs remain, the id to pass
// as the next ?after= cursor is returned in the X-Next-Cursor header.
func (h *ProviderConfigsHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := parsePageLimit(r.URL.Query().Get("limit"), defaultConfigPageLimit, maxConfigPageLimit)
	configs, err := h.q.ListProviderConfigsPage(r.Context(), gen.ListProviderConfigsPageParams{
		Column1: r.URL.Query().Get("after"),
		Limit:   int64(limit) + 1,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(configs) > limit {
		configs = configs[:limit]
		w.Header().Set("X-Next-Cursor", configs[len(configs)-1].ID)
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
		Err(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q; valid: claude, opencode, qwen_code, codex_cli", body.Provider))
		return
	}
	if deprecatedProviders[body.Provider] {
		Err(w, http.StatusBadRequest, fmt.Sprintf("provider %q is deprecated and disabled for new configs; existing configs continue to run", body.Provider))
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
		Err(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q; valid: claude, opencode, qwen_code, codex_cli", body.Provider))
		return
	}

	existing, err := h.q.GetProviderConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "provider config not found")
		return
	}

	// Only reject the deprecated provider when it's actually changing to (or
	// being set to) a deprecated value. An existing deprecated config that
	// PATCHes with its own provider unchanged (or omits provider entirely,
	// meaning "unchanged") must keep working — that's the whole point of
	// deprecating rather than removing these providers.
	if body.Provider != "" && body.Provider != existing.Provider && deprecatedProviders[body.Provider] {
		Err(w, http.StatusBadRequest, fmt.Sprintf("provider %q is deprecated and disabled for new configs; existing configs continue to run", body.Provider))
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
	if body.Model == "" {
		body.Model = existing.Model
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
