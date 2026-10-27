package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent/providers"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// validAgentConfigEffort is the set of values accepted for AgentConfig.Effort.
// "" means unset (provider default). The claude CLI itself only *warns* (does
// not error) on an unrecognized --effort value and silently falls back to the
// default — see providers/effort.go — so this validation is the only thing
// that actually catches a typo'd or stale effort level before it reaches a
// live run.
var validAgentConfigEffort = map[string]bool{
	"":       true,
	"low":    true,
	"medium": true,
	"high":   true,
	"xhigh":  true,
	"max":    true,
}

// validAgentConfigPermissionMode is the set of values accepted for
// AgentConfig.PermissionMode: "" (unset — no --permission-mode flag,
// matching pre-existing behavior) plus the claude CLI's own named modes.
var validAgentConfigPermissionMode = map[string]bool{
	"":                  true,
	"default":           true,
	"auto":              true,
	"acceptEdits":       true,
	"bypassPermissions": true,
}

// validateAgentConfigNumeric enforces the non-negative bounds shared by Create
// and Update on the optional numeric fields. It writes the 400 response and
// returns false on the first violation.
func validateAgentConfigNumeric(w http.ResponseWriter, maxRetries, retryBackoffSecs *int64, maxCostUsd *float64) bool {
	if maxRetries != nil && *maxRetries < 0 {
		Err(w, http.StatusBadRequest, "max_retries must be >= 0")
		return false
	}
	if retryBackoffSecs != nil && *retryBackoffSecs < 0 {
		Err(w, http.StatusBadRequest, "retry_backoff_secs must be >= 0")
		return false
	}
	if maxCostUsd != nil && *maxCostUsd < 0 {
		Err(w, http.StatusBadRequest, "max_cost_usd must be >= 0")
		return false
	}
	return true
}

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
// bare 1/0, which fails strict bool decoding). It also embeds the resolved
// ProviderConfig (provider/model/env) so clients can display/edit those
// values without a second round-trip. ProviderConfig.Env is redacted (see
// safeConfig) — values never leave the server via this view.
type agentConfigView struct {
	gen.AgentConfig
	// FallbackProviderConfigIDs shadows the embedded JSON-text column so the
	// API carries a real array.
	FallbackProviderConfigIDs []string `json:"fallback_provider_config_ids"`
	// EscalationLadder shadows the JSON-text column with a real array.
	EscalationLadder []agent.LadderRung  `json:"escalation_ladder"`
	Enabled          bool                `json:"enabled"`
	ResumeSessions   bool                `json:"resume_sessions"`
	SubtasksEnabled  bool                `json:"subtasks_enabled"`
	ProviderConfig   *gen.ProviderConfig `json:"provider_config,omitempty"`
	// CurrentVersion is the latest version number in the config's history
	// (nil when it has none yet).
	CurrentVersion *int64 `json:"current_version"`
}

// safeConfig builds the response view for an agent config, looking up its
// provider config (best-effort — if the lookup fails, provider_config is
// simply omitted rather than failing the whole response). "safe" refers to
// the embedded provider config's env values being redacted (see
// redactedProviderConfig / provider_env.go) before they're ever serialized —
// this is the only place agent-config responses touch env, so it's the
// choke point that keeps GET /agents and GET /agents/{id} from leaking
// secrets.
func (h *AgentsHandler) safeConfig(r *http.Request, cfg gen.AgentConfig) agentConfigView {
	view := agentConfigView{
		AgentConfig:               cfg,
		FallbackProviderConfigIDs: parseFallbackIDs(cfg.FallbackProviderConfigIds),
		EscalationLadder:          parseLadder(cfg.EscalationLadder),
		Enabled:                   cfg.Enabled != 0,
		ResumeSessions:            cfg.ResumeSessions != 0,
		SubtasksEnabled:           cfg.SubtasksEnabled != 0,
	}
	if cfg.ProviderConfigID != "" {
		if pc, err := h.q.GetProviderConfig(r.Context(), cfg.ProviderConfigID); err == nil {
			redacted := redactedProviderConfig(pc)
			view.ProviderConfig = &redacted
		}
	}
	if n, err := h.q.GetLatestAgentConfigVersionNumber(r.Context(), cfg.ID); err == nil && n > 0 {
		view.CurrentVersion = &n
	}
	return view
}

// parseFallbackIDs decodes the stored fallback_provider_config_ids JSON
// array; empty or malformed yields an empty (non-nil) list.
func parseFallbackIDs(raw string) []string {
	ids := []string{}
	_ = json.Unmarshal([]byte(raw), &ids)
	if ids == nil {
		ids = []string{}
	}
	return ids
}

// maxLadderRungs caps escalation_ladder length.
const maxLadderRungs = 10

// parseLadder decodes the stored escalation_ladder; never nil.
func parseLadder(raw string) []agent.LadderRung {
	l := agent.ParseLadder(raw)
	if l == nil {
		l = []agent.LadderRung{}
	}
	return l
}

// validateLadder checks an escalation ladder: every rung needs an existing
// provider config, a valid effort, and distinct existing fallbacks that do not
// repeat the rung provider. Returns the JSON to store, or a 400 message.
func (h *AgentsHandler) validateLadder(r *http.Request, ladder []agent.LadderRung) (string, string) {
	if len(ladder) > maxLadderRungs {
		return "", fmt.Sprintf("escalation_ladder must have at most %d rungs", maxLadderRungs)
	}
	for i, rung := range ladder {
		if rung.ProviderConfigID == "" {
			return "", fmt.Sprintf("escalation_ladder[%d].provider_config_id is required", i)
		}
		if _, err := h.q.GetProviderConfig(r.Context(), rung.ProviderConfigID); err != nil {
			return "", fmt.Sprintf("unknown provider config in escalation_ladder[%d]: %s", i, rung.ProviderConfigID)
		}
		if !validAgentConfigEffort[rung.Effort] {
			return "", fmt.Sprintf("escalation_ladder[%d].effort must be one of: (empty), low, medium, high, xhigh, max", i)
		}
		seen := map[string]bool{rung.ProviderConfigID: true}
		for _, id := range rung.FallbackProviderConfigIDs {
			if id == "" || seen[id] {
				return "", fmt.Sprintf("escalation_ladder[%d].fallback_provider_config_ids must be non-empty, distinct and not the rung provider", i)
			}
			seen[id] = true
			if _, err := h.q.GetProviderConfig(r.Context(), id); err != nil {
				return "", fmt.Sprintf("unknown provider config in escalation_ladder[%d].fallback_provider_config_ids: %s", i, id)
			}
		}
	}
	if ladder == nil {
		ladder = []agent.LadderRung{}
	}
	b, _ := json.Marshal(ladder)
	return string(b), ""
}

// validateFallbacks checks an agent config's ordered fallback providers:
// each must be an existing provider config, distinct, and not the primary.
// Returns the JSON to store, or a 400 message.
func (h *AgentsHandler) validateFallbacks(r *http.Request, primary string, ids []string) (string, string) {
	seen := map[string]bool{primary: true}
	for _, id := range ids {
		if id == "" {
			return "", "fallback_provider_config_ids must not contain empty ids"
		}
		if seen[id] {
			return "", "fallback_provider_config_ids must not repeat a provider config or include the primary"
		}
		seen[id] = true
		if _, err := h.q.GetProviderConfig(r.Context(), id); err != nil {
			return "", "unknown provider config in fallback_provider_config_ids: " + id
		}
	}
	if ids == nil {
		ids = []string{}
	}
	b, _ := json.Marshal(ids)
	return string(b), ""
}

// knownProviders lists every provider string accepted for reads, model lists
// and writes. The removed providers (anthropic, llm, openai; see
// agent.IsRemovedProvider) are deliberately absent: legacy rows that still
// reference them are kept in the database but fail at dispatch with a clear
// error, and any attempt to create or switch a config to one is rejected.
var knownProviders = map[string]bool{
	"claude": true, "opencode": true, "qwen_code": true, "codex_cli": true,
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
	q  *gen.Queries
	db *sql.DB
}

func NewAgentsHandler(q *gen.Queries, db *sql.DB) *AgentsHandler {
	return &AgentsHandler{q: q, db: db}
}

// List returns a page of agent configs (enabled or not), newest first. Query
// parameters:
//   - limit: page size (default 200, capped at 500)
//   - after: cursor (the id of the last config from the previous page)
//
// The body is a plain JSON array. When more configs remain, the id to pass
// as the next ?after= cursor is returned in the X-Next-Cursor header.
func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := parsePageLimit(r.URL.Query().Get("limit"), defaultConfigPageLimit, maxConfigPageLimit)
	configs, err := h.q.ListAllAgentConfigsPage(r.Context(), gen.ListAllAgentConfigsPageParams{
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
	safe := make([]agentConfigView, len(configs))
	for i, c := range configs {
		safe[i] = h.safeConfig(r, c)
	}
	JSON(w, http.StatusOK, safe)
}

func (h *AgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.q.GetAgentConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	JSON(w, http.StatusOK, h.safeConfig(r, cfg))
}

func (h *AgentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name              string             `json:"name"`
		ProviderConfigID  string             `json:"provider_config_id"`
		SystemPrompt      string             `json:"system_prompt"`
		Labels            string             `json:"labels"`
		MaxTokens         int64              `json:"max_tokens"`
		TimeoutSecs       int64              `json:"timeout_secs"`
		MaxTurns          int64              `json:"max_turns"`
		EnabledPlugins    string             `json:"enabled_plugins"`
		EnabledMCPServers string             `json:"enabled_mcp_servers"`
		CommandAllowlist  string             `json:"command_allowlist"`
		CommandDenylist   string             `json:"command_denylist"`
		MaxRetries        *int64             `json:"max_retries"`
		RetryBackoffSecs  *int64             `json:"retry_backoff_secs"`
		ResumeSessions    *flexBool          `json:"resume_sessions"`
		SubtasksEnabled   *flexBool          `json:"subtasks_enabled"`
		MaxSubtasks       *int64             `json:"max_subtasks"`
		MaxCostUsd        *float64           `json:"max_cost_usd"`
		Priority          *int64             `json:"priority"`
		Effort            *string            `json:"effort"`
		PermissionMode    *string            `json:"permission_mode"`
		FallbackIDs       []string           `json:"fallback_provider_config_ids"`
		EscalationLadder  []agent.LadderRung `json:"escalation_ladder"`
		Note              string             `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Name == "" || body.ProviderConfigID == "" {
		Err(w, http.StatusBadRequest, "name and provider_config_id are required")
		return
	}
	if _, err := h.q.GetProviderConfig(r.Context(), body.ProviderConfigID); err != nil {
		Err(w, http.StatusBadRequest, "unknown provider_config_id")
		return
	}
	if !validateAgentConfigNumeric(w, body.MaxRetries, body.RetryBackoffSecs, body.MaxCostUsd) {
		return
	}
	if body.Effort != nil && !validAgentConfigEffort[*body.Effort] {
		Err(w, http.StatusBadRequest, "effort must be one of: (empty), low, medium, high, xhigh, max")
		return
	}
	if body.PermissionMode != nil && !validAgentConfigPermissionMode[*body.PermissionMode] {
		Err(w, http.StatusBadRequest, "permission_mode must be one of: (empty), default, auto, acceptEdits, bypassPermissions")
		return
	}
	fallbacks, msg := h.validateFallbacks(r, body.ProviderConfigID, body.FallbackIDs)
	if msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	ladderJSON, msg := h.validateLadder(r, body.EscalationLadder)
	if msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	if body.Labels == "" {
		body.Labels = "[]"
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
	effort := ""
	if body.Effort != nil {
		effort = *body.Effort
	}
	permissionMode := ""
	if body.PermissionMode != nil {
		permissionMode = *body.PermissionMode
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

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)

	cfg, err := tq.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID:                        uuid.NewString(),
		Name:                      body.Name,
		ProviderConfigID:          body.ProviderConfigID,
		SystemPrompt:              body.SystemPrompt,
		Labels:                    body.Labels,
		MaxTokens:                 body.MaxTokens,
		TimeoutSecs:               body.TimeoutSecs,
		MaxTurns:                  body.MaxTurns,
		EnabledPlugins:            body.EnabledPlugins,
		EnabledMcpServers:         body.EnabledMCPServers,
		CommandAllowlist:          body.CommandAllowlist,
		CommandDenylist:           body.CommandDenylist,
		MaxRetries:                maxRetries,
		RetryBackoffSecs:          retryBackoffSecs,
		ResumeSessions:            resumeSessions,
		SubtasksEnabled:           subtasksEnabled,
		MaxSubtasks:               maxSubtasks,
		MaxCostUsd:                maxCostUsd,
		Priority:                  priority,
		Effort:                    effort,
		PermissionMode:            permissionMode,
		FallbackProviderConfigIds: fallbacks,
		EscalationLadder:          ladderJSON,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	createNote := strings.TrimSpace(body.Note)
	if createNote == "" {
		createNote = "Created"
	}
	if _, err := recordVersion(ctx, tq, cfg, middleware.ActorFromContext(ctx), createNote); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	if startEnabled == 0 {
		// disable the freshly created config
		cfg, err = tq.UpdateAgentConfig(ctx, gen.UpdateAgentConfigParams{
			Name: cfg.Name, ProviderConfigID: cfg.ProviderConfigID,
			SystemPrompt: cfg.SystemPrompt, Labels: cfg.Labels,
			MaxTokens: cfg.MaxTokens, TimeoutSecs: cfg.TimeoutSecs, MaxTurns: cfg.MaxTurns,
			EnabledPlugins: cfg.EnabledPlugins, EnabledMcpServers: cfg.EnabledMcpServers,
			CommandAllowlist: cfg.CommandAllowlist, CommandDenylist: cfg.CommandDenylist,
			MaxRetries: cfg.MaxRetries, RetryBackoffSecs: cfg.RetryBackoffSecs,
			ResumeSessions:  cfg.ResumeSessions,
			SubtasksEnabled: cfg.SubtasksEnabled, MaxSubtasks: cfg.MaxSubtasks,
			MaxCostUsd: cfg.MaxCostUsd, Priority: cfg.Priority,
			Effort:                    cfg.Effort,
			PermissionMode:            cfg.PermissionMode,
			FallbackProviderConfigIds: cfg.FallbackProviderConfigIds,
			EscalationLadder:          cfg.EscalationLadder,
			Enabled:                   0, ID: cfg.ID,
		})
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("X-Label-Conflict", conflict)
		JSON(w, http.StatusCreated, h.safeConfig(r, cfg))
		return
	}

	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, h.safeConfig(r, cfg))
}

func (h *AgentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name              string              `json:"name"`
		ProviderConfigID  string              `json:"provider_config_id"`
		SystemPrompt      string              `json:"system_prompt"`
		Labels            string              `json:"labels"`
		MaxTokens         int64               `json:"max_tokens"`
		TimeoutSecs       int64               `json:"timeout_secs"`
		MaxTurns          int64               `json:"max_turns"`
		Enabled           *flexBool           `json:"enabled"`
		EnabledPlugins    string              `json:"enabled_plugins"`
		EnabledMCPServers string              `json:"enabled_mcp_servers"`
		CommandAllowlist  string              `json:"command_allowlist"`
		CommandDenylist   string              `json:"command_denylist"`
		MaxRetries        *int64              `json:"max_retries"`
		RetryBackoffSecs  *int64              `json:"retry_backoff_secs"`
		ResumeSessions    *flexBool           `json:"resume_sessions"`
		SubtasksEnabled   *flexBool           `json:"subtasks_enabled"`
		MaxSubtasks       *int64              `json:"max_subtasks"`
		MaxCostUsd        *float64            `json:"max_cost_usd"`
		Priority          *int64              `json:"priority"`
		Effort            *string             `json:"effort"`
		PermissionMode    *string             `json:"permission_mode"`
		FallbackIDs       *[]string           `json:"fallback_provider_config_ids"`
		EscalationLadder  *[]agent.LadderRung `json:"escalation_ladder"`
		Note              *string             `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ProviderConfigID != "" {
		if _, err := h.q.GetProviderConfig(r.Context(), body.ProviderConfigID); err != nil {
			Err(w, http.StatusBadRequest, "unknown provider_config_id")
			return
		}
	}
	if !validateAgentConfigNumeric(w, body.MaxRetries, body.RetryBackoffSecs, body.MaxCostUsd) {
		return
	}
	if body.Effort != nil && !validAgentConfigEffort[*body.Effort] {
		Err(w, http.StatusBadRequest, "effort must be one of: (empty), low, medium, high, xhigh, max")
		return
	}
	if body.PermissionMode != nil && !validAgentConfigPermissionMode[*body.PermissionMode] {
		Err(w, http.StatusBadRequest, "permission_mode must be one of: (empty), default, auto, acceptEdits, bypassPermissions")
		return
	}

	// Read current to preserve enabled state if not sent
	existing, err := h.q.GetAgentConfig(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	enabled := existing.Enabled
	var conflict string
	if body.Enabled != nil {
		if *body.Enabled {
			// Priority-based failover allows multiple enabled configs to share
			// a label, so a conflict is informational only — surfaced to the
			// caller via X-Label-Conflict, same as Create.
			labelsToCheck := body.Labels
			if labelsToCheck == "" {
				labelsToCheck = existing.Labels
			}
			var cerr error
			conflict, cerr = h.labelConflict(r, labelsToCheck, chi.URLParam(r, "id"))
			if cerr != nil {
				Err(w, http.StatusInternalServerError, cerr.Error())
				return
			}
			enabled = 1
		} else {
			enabled = 0
		}
	}

	if body.ProviderConfigID == "" {
		body.ProviderConfigID = existing.ProviderConfigID
	}
	// Omitted keeps the stored list (re-validated against a changed primary).
	// A fallback promoted to primary drops out of the kept list.
	var fallbackIDs []string
	if body.FallbackIDs != nil {
		fallbackIDs = *body.FallbackIDs
	} else {
		for _, id := range parseFallbackIDs(existing.FallbackProviderConfigIds) {
			if id != body.ProviderConfigID {
				fallbackIDs = append(fallbackIDs, id)
			}
		}
	}
	fallbacks, msg := h.validateFallbacks(r, body.ProviderConfigID, fallbackIDs)
	if msg != "" {
		Err(w, http.StatusBadRequest, msg)
		return
	}
	ladderJSON := existing.EscalationLadder
	if body.EscalationLadder != nil {
		ladderJSON, msg = h.validateLadder(r, *body.EscalationLadder)
		if msg != "" {
			Err(w, http.StatusBadRequest, msg)
			return
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
	effort := existing.Effort
	if body.Effort != nil {
		effort = *body.Effort
	}
	permissionMode := existing.PermissionMode
	if body.PermissionMode != nil {
		permissionMode = *body.PermissionMode
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)
	if err := ensureBaselineVersion(ctx, tq, existing); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	cfg, err := tq.UpdateAgentConfig(ctx, gen.UpdateAgentConfigParams{
		Name:                      body.Name,
		ProviderConfigID:          body.ProviderConfigID,
		SystemPrompt:              body.SystemPrompt,
		Labels:                    body.Labels,
		MaxTokens:                 body.MaxTokens,
		TimeoutSecs:               body.TimeoutSecs,
		MaxTurns:                  body.MaxTurns,
		Enabled:                   enabled,
		EnabledPlugins:            body.EnabledPlugins,
		EnabledMcpServers:         body.EnabledMCPServers,
		CommandAllowlist:          body.CommandAllowlist,
		CommandDenylist:           body.CommandDenylist,
		MaxRetries:                maxRetries,
		RetryBackoffSecs:          retryBackoffSecs,
		ResumeSessions:            resumeSessions,
		SubtasksEnabled:           subtasksEnabled,
		MaxSubtasks:               maxSubtasks,
		MaxCostUsd:                maxCostUsd,
		Priority:                  priority,
		Effort:                    effort,
		PermissionMode:            permissionMode,
		FallbackProviderConfigIds: fallbacks,
		EscalationLadder:          ladderJSON,
		ID:                        chi.URLParam(r, "id"),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if behaviourChanged(existing, cfg) {
		note := ""
		if body.Note != nil {
			note = strings.TrimSpace(*body.Note)
		}
		if _, err := recordVersion(ctx, tq, cfg, middleware.ActorFromContext(ctx), note); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if conflict != "" {
		w.Header().Set("X-Label-Conflict", conflict)
	}
	JSON(w, http.StatusOK, h.safeConfig(r, cfg))
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
		out, err := agentuser.ServerCommand("opencode", "models").Output()
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
		// Known provider without a fixed model list (e.g. qwen_code): model comes
		// from provider config, so return an empty list rather than 404 so the UI
		// falls back to free-text entry instead of logging a console error.
		if knownProviders[provider] {
			models = []string{}
		} else {
			Err(w, http.StatusNotFound, fmt.Sprintf("unknown provider: %s", provider))
			return
		}
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
	plugins, err := providers.ListInstalledClaudePlugins()
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	mcpServers, err := providers.ListAvailableClaudeMCPServers()
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
