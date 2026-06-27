package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// labelConflict returns the name of any enabled config (excluding excludeID) that shares a label.
func (h *AgentsHandler) labelConflict(r *http.Request, labelsJSON string, excludeID string) (string, error) {
	var newLabels []string
	if err := json.Unmarshal([]byte(labelsJSON), &newLabels); err != nil || len(newLabels) == 0 {
		return "", nil
	}
	active, err := h.q.ListAgentConfigs(r.Context())
	if err != nil {
		return "", err
	}
	for _, cfg := range active {
		if cfg.ID == excludeID {
			continue
		}
		var existing []string
		if err := json.Unmarshal([]byte(cfg.Labels), &existing); err != nil {
			continue
		}
		for _, el := range existing {
			for _, nl := range newLabels {
				if el == nl {
					return cfg.Name, nil
				}
			}
		}
	}
	return "", nil
}

// redactEnv replaces env values with "***" so API keys are never returned to clients.
func redactEnv(envJSON string) string {
	var env map[string]string
	if err := json.Unmarshal([]byte(envJSON), &env); err != nil || len(env) == 0 {
		return envJSON
	}
	redacted := make(map[string]string, len(env))
	for k := range env {
		redacted[k] = "***"
	}
	out, _ := json.Marshal(redacted)
	return string(out)
}

func safeConfig(cfg gen.AgentConfig) gen.AgentConfig {
	cfg.Env = redactEnv(cfg.Env)
	return cfg
}

type AgentsHandler struct {
	q *gen.Queries
}

func NewAgentsHandler(q *gen.Queries) *AgentsHandler {
	return &AgentsHandler{q: q}
}

func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	configs, err := h.q.ListAllAgentConfigs(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	safe := make([]gen.AgentConfig, len(configs))
	for i, c := range configs {
		safe[i] = safeConfig(c)
	}
	JSON(w, http.StatusOK, safe)
}

func (h *AgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.q.GetAgentConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	JSON(w, http.StatusOK, safeConfig(cfg))
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

	conflict, err := h.labelConflict(r, body.Labels, "")
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	// ponytail: start disabled when labels conflict; client surfaces this
	startEnabled := int64(1)
	if conflict != "" {
		startEnabled = 0
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

	if startEnabled == 0 {
		// disable the freshly created config
		cfg, err = h.q.UpdateAgentConfig(r.Context(), gen.UpdateAgentConfigParams{
			Name: cfg.Name, Provider: cfg.Provider, Model: cfg.Model,
			SystemPrompt: cfg.SystemPrompt, Labels: cfg.Labels, Env: cfg.Env,
			MaxTokens: cfg.MaxTokens, TimeoutSecs: cfg.TimeoutSecs,
			Enabled: 0, ID: cfg.ID,
		})
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("X-Label-Conflict", conflict)
		JSON(w, http.StatusCreated, cfg)
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
		Enabled      *bool  `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Read current to preserve enabled state if not sent
	existing, err := h.q.GetAgentConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	enabled := existing.Enabled
	if body.Enabled != nil {
		if *body.Enabled {
			// check for label conflicts before enabling
			labelsToCheck := body.Labels
			if labelsToCheck == "" {
				labelsToCheck = existing.Labels
			}
			conflict, cerr := h.labelConflict(r, labelsToCheck, chi.URLParam(r, "id"))
			if cerr != nil {
				Err(w, http.StatusInternalServerError, cerr.Error())
				return
			}
			if conflict != "" {
				Err(w, http.StatusConflict, fmt.Sprintf("label conflict with active config %q — disable it first", conflict))
				return
			}
			enabled = 1
		} else {
			enabled = 0
		}
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
		Enabled:      enabled,
		ID:           chi.URLParam(r, "id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, safeConfig(cfg))
}

func (h *AgentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteAgentConfig(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AgentsHandler) GetModels(w http.ResponseWriter, r *http.Request) {
	providerModels := map[string][2]string{
		"claude": {"claude-sonnet-4-6", "claude-opus-4"},
	}
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		Err(w, http.StatusBadRequest, "provider query param is required")
		return
	}

	var models []string
	defaultModel := ""

	if p, ok := providerModels[provider]; ok {
		models = []string{p[0], p[1]}
		defaultModel = p[0]
	}

	if provider == "opencode" {
		out, err := exec.Command("opencode", "models").Output()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			var filtered []string
			for _, line := range lines {
				if v := strings.TrimSpace(line); v != "" {
					filtered = append(filtered, v)
				}
			}
			models = filtered
			if len(models) > 0 {
				defaultModel = models[0]
			}
		} else {
			slog.Warn("opencode models: failed to fetch model list", "err", err)
		}
	}

	if models == nil {
		Err(w, http.StatusNotFound, fmt.Sprintf("unknown provider: %s", provider))
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"provider":     provider,
		"default_model": defaultModel,
		"models":       models,
	})
}
