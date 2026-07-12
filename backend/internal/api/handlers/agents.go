package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
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

// agentConfigView mirrors gen.AgentConfig but serializes the SQLite
// 0/1 flag columns as real JSON booleans, matching the OpenAPI schema
// (clients that echo a GET response back into a PUT would otherwise send
// bare 1/0, which fails strict bool decoding).
type agentConfigView struct {
	gen.AgentConfig
	Enabled         bool `json:"enabled"`
	ResumeSessions  bool `json:"resume_sessions"`
	SubtasksEnabled bool `json:"subtasks_enabled"`
}

func safeConfig(cfg gen.AgentConfig) agentConfigView {
	return agentConfigView{
		AgentConfig:     cfg,
		Enabled:         cfg.Enabled != 0,
		ResumeSessions:  cfg.ResumeSessions != 0,
		SubtasksEnabled: cfg.SubtasksEnabled != 0,
	}
}

var knownProviders = map[string]bool{
	"claude": true, "anthropic": true, "llm": true, "opencode": true, "qwen_code": true,
	"gemini_cli": true, "codex_cli": true,
}

// flexBool unmarshals JSON true/false as well as numeric 0/1, since some
// clients (e.g. hand-built requests) send booleans as numbers.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*b = n.String() != "0"
		return nil
	}
	var v bool
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*b = flexBool(v)
	return nil
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
	safe := make([]agentConfigView, len(configs))
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
		Name              string    `json:"name"`
		Provider          string    `json:"provider"`
		Model             string    `json:"model"`
		SystemPrompt      string    `json:"system_prompt"`
		Labels            string    `json:"labels"`
		Env               string    `json:"env"`
		MaxTokens         int64     `json:"max_tokens"`
		TimeoutSecs       int64     `json:"timeout_secs"`
		MaxTurns          int64     `json:"max_turns"`
		EnabledPlugins    string    `json:"enabled_plugins"`
		EnabledMCPServers string    `json:"enabled_mcp_servers"`
		CommandAllowlist  string    `json:"command_allowlist"`
		CommandDenylist   string    `json:"command_denylist"`
		MaxRetries        *int64    `json:"max_retries"`
		RetryBackoffSecs  *int64    `json:"retry_backoff_secs"`
		ResumeSessions    *flexBool `json:"resume_sessions"`
		SubtasksEnabled   *flexBool `json:"subtasks_enabled"`
		MaxSubtasks       *int64    `json:"max_subtasks"`
		MaxCostUsd        *float64  `json:"max_cost_usd"`
		Priority          *int64    `json:"priority"`
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
	if body.MaxRetries != nil && *body.MaxRetries < 0 {
		Err(w, http.StatusBadRequest, "max_retries must be >= 0")
		return
	}
	if body.RetryBackoffSecs != nil && *body.RetryBackoffSecs < 0 {
		Err(w, http.StatusBadRequest, "retry_backoff_secs must be >= 0")
		return
	}
	if body.MaxCostUsd != nil && *body.MaxCostUsd < 0 {
		Err(w, http.StatusBadRequest, "max_cost_usd must be >= 0")
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
	if body.MaxTurns == 0 {
		body.MaxTurns = 50
	}
	if body.EnabledPlugins == "" {
		body.EnabledPlugins = "[]"
	}
	if body.EnabledMCPServers == "" {
		body.EnabledMCPServers = "[]"
	}
	if body.CommandAllowlist == "" {
		body.CommandAllowlist = "[]"
	}
	if body.CommandDenylist == "" {
		body.CommandDenylist = "[]"
	}
	maxRetries := int64(3)
	if body.MaxRetries != nil {
		maxRetries = *body.MaxRetries
	}
	retryBackoffSecs := int64(30)
	if body.RetryBackoffSecs != nil {
		retryBackoffSecs = *body.RetryBackoffSecs
	}
	resumeSessions := int64(1)
	if body.ResumeSessions != nil && !*body.ResumeSessions {
		resumeSessions = 0
	}
	subtasksEnabled := int64(0)
	if body.SubtasksEnabled != nil && *body.SubtasksEnabled {
		subtasksEnabled = 1
	}
	maxSubtasks := int64(10)
	if body.MaxSubtasks != nil && *body.MaxSubtasks > 0 {
		maxSubtasks = *body.MaxSubtasks
	}
	maxCostUsd := float64(0)
	if body.MaxCostUsd != nil {
		maxCostUsd = *body.MaxCostUsd
	}
	priority := int64(0)
	if body.Priority != nil {
		priority = *body.Priority
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
		ID:                uuid.NewString(),
		Name:              body.Name,
		Provider:          body.Provider,
		Model:             body.Model,
		SystemPrompt:      body.SystemPrompt,
		Labels:            body.Labels,
		Env:               body.Env,
		MaxTokens:         body.MaxTokens,
		TimeoutSecs:       body.TimeoutSecs,
		MaxTurns:          body.MaxTurns,
		EnabledPlugins:    body.EnabledPlugins,
		EnabledMcpServers: body.EnabledMCPServers,
		CommandAllowlist:  body.CommandAllowlist,
		CommandDenylist:   body.CommandDenylist,
		MaxRetries:        maxRetries,
		RetryBackoffSecs:  retryBackoffSecs,
		ResumeSessions:    resumeSessions,
		SubtasksEnabled:   subtasksEnabled,
		MaxSubtasks:       maxSubtasks,
		MaxCostUsd:        maxCostUsd,
		Priority:          priority,
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
			MaxTokens: cfg.MaxTokens, TimeoutSecs: cfg.TimeoutSecs, MaxTurns: cfg.MaxTurns,
			EnabledPlugins: cfg.EnabledPlugins, EnabledMcpServers: cfg.EnabledMcpServers,
			CommandAllowlist: cfg.CommandAllowlist, CommandDenylist: cfg.CommandDenylist,
			MaxRetries: cfg.MaxRetries, RetryBackoffSecs: cfg.RetryBackoffSecs,
			ResumeSessions:  cfg.ResumeSessions,
			SubtasksEnabled: cfg.SubtasksEnabled, MaxSubtasks: cfg.MaxSubtasks,
			MaxCostUsd: cfg.MaxCostUsd, Priority: cfg.Priority,
			Enabled: 0, ID: cfg.ID,
		})
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("X-Label-Conflict", conflict)
		JSON(w, http.StatusCreated, safeConfig(cfg))
		return
	}

	JSON(w, http.StatusCreated, safeConfig(cfg))
}

func (h *AgentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name              string    `json:"name"`
		Provider          string    `json:"provider"`
		Model             string    `json:"model"`
		SystemPrompt      string    `json:"system_prompt"`
		Labels            string    `json:"labels"`
		Env               string    `json:"env"`
		MaxTokens         int64     `json:"max_tokens"`
		TimeoutSecs       int64     `json:"timeout_secs"`
		MaxTurns          int64     `json:"max_turns"`
		Enabled           *flexBool `json:"enabled"`
		EnabledPlugins    string    `json:"enabled_plugins"`
		EnabledMCPServers string    `json:"enabled_mcp_servers"`
		CommandAllowlist  string    `json:"command_allowlist"`
		CommandDenylist   string    `json:"command_denylist"`
		MaxRetries        *int64    `json:"max_retries"`
		RetryBackoffSecs  *int64    `json:"retry_backoff_secs"`
		ResumeSessions    *flexBool `json:"resume_sessions"`
		SubtasksEnabled   *flexBool `json:"subtasks_enabled"`
		MaxSubtasks       *int64    `json:"max_subtasks"`
		MaxCostUsd        *float64  `json:"max_cost_usd"`
		Priority          *int64    `json:"priority"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Provider != "" && !knownProviders[body.Provider] {
		Err(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q; valid: claude, anthropic, llm, opencode, qwen_code, gemini_cli, codex_cli", body.Provider))
		return
	}
	if body.MaxRetries != nil && *body.MaxRetries < 0 {
		Err(w, http.StatusBadRequest, "max_retries must be >= 0")
		return
	}
	if body.RetryBackoffSecs != nil && *body.RetryBackoffSecs < 0 {
		Err(w, http.StatusBadRequest, "retry_backoff_secs must be >= 0")
		return
	}
	if body.MaxCostUsd != nil && *body.MaxCostUsd < 0 {
		Err(w, http.StatusBadRequest, "max_cost_usd must be >= 0")
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

	if body.EnabledPlugins == "" {
		body.EnabledPlugins = existing.EnabledPlugins
	}
	if body.EnabledMCPServers == "" {
		body.EnabledMCPServers = existing.EnabledMcpServers
	}
	if body.CommandAllowlist == "" {
		body.CommandAllowlist = existing.CommandAllowlist
	}
	if body.CommandDenylist == "" {
		body.CommandDenylist = existing.CommandDenylist
	}
	maxRetries := existing.MaxRetries
	if body.MaxRetries != nil {
		maxRetries = *body.MaxRetries
	}
	retryBackoffSecs := existing.RetryBackoffSecs
	if body.RetryBackoffSecs != nil {
		retryBackoffSecs = *body.RetryBackoffSecs
	}
	resumeSessions := existing.ResumeSessions
	if body.ResumeSessions != nil {
		if *body.ResumeSessions {
			resumeSessions = 1
		} else {
			resumeSessions = 0
		}
	}
	subtasksEnabled := existing.SubtasksEnabled
	if body.SubtasksEnabled != nil {
		if *body.SubtasksEnabled {
			subtasksEnabled = 1
		} else {
			subtasksEnabled = 0
		}
	}
	maxSubtasks := existing.MaxSubtasks
	if body.MaxSubtasks != nil && *body.MaxSubtasks > 0 {
		maxSubtasks = *body.MaxSubtasks
	}
	maxCostUsd := existing.MaxCostUsd
	if body.MaxCostUsd != nil {
		maxCostUsd = *body.MaxCostUsd
	}
	priority := existing.Priority
	if body.Priority != nil {
		priority = *body.Priority
	}

	cfg, err := h.q.UpdateAgentConfig(r.Context(), gen.UpdateAgentConfigParams{
		Name:              body.Name,
		Provider:          body.Provider,
		Model:             body.Model,
		SystemPrompt:      body.SystemPrompt,
		Labels:            body.Labels,
		Env:               body.Env,
		MaxTokens:         body.MaxTokens,
		TimeoutSecs:       body.TimeoutSecs,
		MaxTurns:          body.MaxTurns,
		Enabled:           enabled,
		EnabledPlugins:    body.EnabledPlugins,
		EnabledMcpServers: body.EnabledMCPServers,
		CommandAllowlist:  body.CommandAllowlist,
		CommandDenylist:   body.CommandDenylist,
		MaxRetries:        maxRetries,
		RetryBackoffSecs:  retryBackoffSecs,
		ResumeSessions:    resumeSessions,
		SubtasksEnabled:   subtasksEnabled,
		MaxSubtasks:       maxSubtasks,
		MaxCostUsd:        maxCostUsd,
		Priority:          priority,
		ID:                chi.URLParam(r, "id"),
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
	providerModels := map[string][]string{
		"claude": {"sonnet", "opus", "haiku"},
	}
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		Err(w, http.StatusBadRequest, "provider query param is required")
		return
	}

	var models []string
	defaultModel := ""

	if p, ok := providerModels[provider]; ok {
		models = p
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
			middleware.LoggerFromContext(r.Context()).Warn("opencode models: failed to fetch model list", "err", err)
		}
	}

	if models == nil {
		Err(w, http.StatusNotFound, fmt.Sprintf("unknown provider: %s", provider))
		return
	}

	JSON(w, http.StatusOK, map[string]any{
		"provider":      provider,
		"default_model": defaultModel,
		"models":        models,
	})
}

// claudePluginOption is the JSON shape for a single discovered plugin,
// exposed to the frontend for per-agent-config selection.
type claudePluginOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Marketplace string `json:"marketplace"`
}

// GetClaudeOptions returns the Claude plugins and user-level MCP servers
// discovered on this machine (from ~/.claude/plugins/installed_plugins.json
// and the global mcpServers key in ~/.claude.json), for the frontend to
// present as per-agent-config selection options. This endpoint is
// claude-provider-specific for now; other providers have no equivalent.
func (h *AgentsHandler) GetClaudeOptions(w http.ResponseWriter, r *http.Request) {
	plugins, err := agent.ListInstalledClaudePlugins()
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	mcpServers, err := agent.ListAvailableClaudeMCPServers()
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	pluginOptions := make([]claudePluginOption, len(plugins))
	for i, p := range plugins {
		pluginOptions[i] = claudePluginOption{ID: p.ID, Name: p.Name, Marketplace: p.Marketplace}
	}
	if mcpServers == nil {
		mcpServers = []string{}
	}

	JSON(w, http.StatusOK, map[string]any{
		"plugins":     pluginOptions,
		"mcp_servers": mcpServers,
	})
}
