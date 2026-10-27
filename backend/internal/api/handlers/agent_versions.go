package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// agentConfigSnapshot is the behaviour-relevant slice of an agent config that
// is stored per version. Everything except name, labels, enabled and priority
// (identity / routing) is included; changing only those never writes a
// version. The JSON keys must match the backfill in migration 074.
type agentConfigSnapshot struct {
	SystemPrompt              string          `json:"system_prompt"`
	ProviderConfigID          string          `json:"provider_config_id"`
	FallbackProviderConfigIDs json.RawMessage `json:"fallback_provider_config_ids"`
	EscalationLadder          json.RawMessage `json:"escalation_ladder"`
	Effort                    string          `json:"effort"`
	PermissionMode            string          `json:"permission_mode"`
	MaxTurns                  int64           `json:"max_turns"`
	MaxTokens                 int64           `json:"max_tokens"`
	TimeoutSecs               int64           `json:"timeout_secs"`
	MaxCostUsd                float64         `json:"max_cost_usd"`
	MaxRetries                int64           `json:"max_retries"`
	RetryBackoffSecs          int64           `json:"retry_backoff_secs"`
	ResumeSessions            bool            `json:"resume_sessions"`
	SubtasksEnabled           bool            `json:"subtasks_enabled"`
	MaxSubtasks               int64           `json:"max_subtasks"`
	EnabledPlugins            json.RawMessage `json:"enabled_plugins"`
	EnabledMcpServers         json.RawMessage `json:"enabled_mcp_servers"`
	CommandAllowlist          json.RawMessage `json:"command_allowlist"`
	CommandDenylist           json.RawMessage `json:"command_denylist"`
}

// jsonTextOrEmptyArray returns raw if it is valid JSON, else "[]".
func jsonTextOrEmptyArray(raw string) json.RawMessage {
	if strings.TrimSpace(raw) == "" || !json.Valid([]byte(raw)) {
		return json.RawMessage("[]")
	}
	return json.RawMessage(raw)
}

func snapshotOf(cfg gen.AgentConfig) agentConfigSnapshot {
	return agentConfigSnapshot{
		SystemPrompt:              cfg.SystemPrompt,
		ProviderConfigID:          cfg.ProviderConfigID,
		FallbackProviderConfigIDs: jsonTextOrEmptyArray(cfg.FallbackProviderConfigIds),
		EscalationLadder:          jsonTextOrEmptyArray(cfg.EscalationLadder),
		Effort:                    cfg.Effort,
		PermissionMode:            cfg.PermissionMode,
		MaxTurns:                  cfg.MaxTurns,
		MaxTokens:                 cfg.MaxTokens,
		TimeoutSecs:               cfg.TimeoutSecs,
		MaxCostUsd:                cfg.MaxCostUsd,
		MaxRetries:                cfg.MaxRetries,
		RetryBackoffSecs:          cfg.RetryBackoffSecs,
		ResumeSessions:            cfg.ResumeSessions != 0,
		SubtasksEnabled:           cfg.SubtasksEnabled != 0,
		MaxSubtasks:               cfg.MaxSubtasks,
		EnabledPlugins:            jsonTextOrEmptyArray(cfg.EnabledPlugins),
		EnabledMcpServers:         jsonTextOrEmptyArray(cfg.EnabledMcpServers),
		CommandAllowlist:          jsonTextOrEmptyArray(cfg.CommandAllowlist),
		CommandDenylist:           jsonTextOrEmptyArray(cfg.CommandDenylist),
	}
}

// jsonSemanticallyEqual compares two JSON-text columns by value, treating
// empty/invalid as [].
func jsonSemanticallyEqual(a, b string) bool {
	var av, bv any
	_ = json.Unmarshal(jsonTextOrEmptyArray(a), &av)
	_ = json.Unmarshal(jsonTextOrEmptyArray(b), &bv)
	return reflect.DeepEqual(av, bv)
}

// behaviourChanged reports whether any snapshotted field differs between two
// configs. name, labels, enabled and priority are deliberately ignored.
func behaviourChanged(a, b gen.AgentConfig) bool {
	if a.SystemPrompt != b.SystemPrompt || a.ProviderConfigID != b.ProviderConfigID ||
		a.Effort != b.Effort || a.PermissionMode != b.PermissionMode ||
		a.MaxTurns != b.MaxTurns || a.MaxTokens != b.MaxTokens || a.TimeoutSecs != b.TimeoutSecs ||
		a.MaxCostUsd != b.MaxCostUsd || a.MaxRetries != b.MaxRetries ||
		a.RetryBackoffSecs != b.RetryBackoffSecs || a.MaxSubtasks != b.MaxSubtasks ||
		(a.ResumeSessions != 0) != (b.ResumeSessions != 0) ||
		(a.SubtasksEnabled != 0) != (b.SubtasksEnabled != 0) {
		return true
	}
	return !jsonSemanticallyEqual(a.FallbackProviderConfigIds, b.FallbackProviderConfigIds) ||
		!jsonSemanticallyEqual(a.EscalationLadder, b.EscalationLadder) ||
		!jsonSemanticallyEqual(a.EnabledPlugins, b.EnabledPlugins) ||
		!jsonSemanticallyEqual(a.EnabledMcpServers, b.EnabledMcpServers) ||
		!jsonSemanticallyEqual(a.CommandAllowlist, b.CommandAllowlist) ||
		!jsonSemanticallyEqual(a.CommandDenylist, b.CommandDenylist)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// recordVersion appends a snapshot of cfg as the config's next version.
func recordVersion(ctx context.Context, tq *gen.Queries, cfg gen.AgentConfig, actor, note string) (gen.AgentConfigVersion, error) {
	b, err := json.Marshal(snapshotOf(cfg))
	if err != nil {
		return gen.AgentConfigVersion{}, err
	}
	return tq.CreateAgentConfigVersion(ctx, gen.CreateAgentConfigVersionParams{
		ID:            uuid.NewString(),
		AgentConfigID: cfg.ID,
		Snapshot:      string(b),
		ActorID:       nilIfEmpty(actor),
		Note:          nilIfEmpty(note),
	})
}

// ensureBaselineVersion lazily records v1 for a config that has no versions
// (created outside Create, or inserted between migration and deploy).
func ensureBaselineVersion(ctx context.Context, tq *gen.Queries, cfg gen.AgentConfig) error {
	n, err := tq.GetLatestAgentConfigVersionNumber(ctx, cfg.ID)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = recordVersion(ctx, tq, cfg, "", "Initial version")
	return err
}

type agentConfigVersionView struct {
	ID            string          `json:"id"`
	AgentConfigID string          `json:"agent_config_id"`
	Version       int64           `json:"version"`
	Snapshot      json.RawMessage `json:"snapshot"`
	ActorID       *string         `json:"actor_id"`
	Note          *string         `json:"note"`
	CreatedAt     time.Time       `json:"created_at"`
}

func versionView(v gen.AgentConfigVersion) agentConfigVersionView {
	snap := json.RawMessage(v.Snapshot)
	if !json.Valid(snap) {
		snap = json.RawMessage("{}")
	}
	return agentConfigVersionView{
		ID: v.ID, AgentConfigID: v.AgentConfigID, Version: v.Version,
		Snapshot: snap, ActorID: v.ActorID, Note: v.Note, CreatedAt: v.CreatedAt,
	}
}

// ListVersions handles GET /agents/{id}/versions (newest first, with snapshots).
func (h *AgentsHandler) ListVersions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	cfg, err := h.q.GetAgentConfig(r.Context(), id)
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	if err := ensureBaselineVersion(r.Context(), h.q, cfg); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	versions, err := h.q.ListAgentConfigVersions(r.Context(), id)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]agentConfigVersionView, len(versions))
	for i, v := range versions {
		out[i] = versionView(v)
	}
	JSON(w, http.StatusOK, out)
}

// GetVersion handles GET /agents/{id}/versions/{version}.
func (h *AgentsHandler) GetVersion(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.ParseInt(chi.URLParam(r, "version"), 10, 64)
	if err != nil {
		Err(w, http.StatusBadRequest, "version must be an integer")
		return
	}
	v, err := h.q.GetAgentConfigVersion(r.Context(), gen.GetAgentConfigVersionParams{
		AgentConfigID: chi.URLParam(r, "id"), Version: n,
	})
	if err != nil {
		Err(w, http.StatusNotFound, "version not found")
		return
	}
	JSON(w, http.StatusOK, versionView(v))
}

// RestoreVersion handles POST /agents/{id}/versions/{version}/restore. It
// applies the snapshot onto the current config (keeping name, labels, enabled
// and priority) and records the result as a new version when it differs.
func (h *AgentsHandler) RestoreVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	n, err := strconv.ParseInt(chi.URLParam(r, "version"), 10, 64)
	if err != nil {
		Err(w, http.StatusBadRequest, "version must be an integer")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			Err(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	existing, err := h.q.GetAgentConfig(ctx, id)
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	ver, err := h.q.GetAgentConfigVersion(ctx, gen.GetAgentConfigVersionParams{AgentConfigID: id, Version: n})
	if err != nil {
		Err(w, http.StatusNotFound, "version not found")
		return
	}
	var snap agentConfigSnapshot
	if err := json.Unmarshal([]byte(ver.Snapshot), &snap); err != nil {
		Err(w, http.StatusInternalServerError, "stored snapshot is corrupt")
		return
	}

	// Re-validate: provider configs referenced by the snapshot may be gone.
	if _, err := h.q.GetProviderConfig(ctx, snap.ProviderConfigID); err != nil {
		Err(w, http.StatusBadRequest, fmt.Sprintf("cannot restore v%d: provider config %s no longer exists", n, snap.ProviderConfigID))
		return
	}
	if !validAgentConfigEffort[snap.Effort] {
		Err(w, http.StatusBadRequest, fmt.Sprintf("cannot restore v%d: invalid effort %q", n, snap.Effort))
		return
	}
	if !validAgentConfigPermissionMode[snap.PermissionMode] {
		Err(w, http.StatusBadRequest, fmt.Sprintf("cannot restore v%d: invalid permission_mode %q", n, snap.PermissionMode))
		return
	}
	var fallbackIDs []string
	_ = json.Unmarshal(jsonTextOrEmptyArray(string(snap.FallbackProviderConfigIDs)), &fallbackIDs)
	fallbacks, msg := h.validateFallbacks(r, snap.ProviderConfigID, fallbackIDs)
	if msg != "" {
		Err(w, http.StatusBadRequest, fmt.Sprintf("cannot restore v%d: %s", n, msg))
		return
	}
	var ladder []agent.LadderRung
	_ = json.Unmarshal(jsonTextOrEmptyArray(string(snap.EscalationLadder)), &ladder)
	ladderJSON, msg := h.validateLadder(r, ladder)
	if msg != "" {
		Err(w, http.StatusBadRequest, fmt.Sprintf("cannot restore v%d: %s", n, msg))
		return
	}

	b2i := func(b bool) int64 {
		if b {
			return 1
		}
		return 0
	}
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
		Name: existing.Name, ProviderConfigID: snap.ProviderConfigID,
		SystemPrompt: snap.SystemPrompt, Labels: existing.Labels,
		MaxTokens: snap.MaxTokens, TimeoutSecs: snap.TimeoutSecs, MaxTurns: snap.MaxTurns,
		Enabled:           existing.Enabled,
		EnabledPlugins:    string(jsonTextOrEmptyArray(string(snap.EnabledPlugins))),
		EnabledMcpServers: string(jsonTextOrEmptyArray(string(snap.EnabledMcpServers))),
		CommandAllowlist:  string(jsonTextOrEmptyArray(string(snap.CommandAllowlist))),
		CommandDenylist:   string(jsonTextOrEmptyArray(string(snap.CommandDenylist))),
		MaxRetries:        snap.MaxRetries, RetryBackoffSecs: snap.RetryBackoffSecs,
		ResumeSessions:  b2i(snap.ResumeSessions),
		SubtasksEnabled: b2i(snap.SubtasksEnabled), MaxSubtasks: snap.MaxSubtasks,
		MaxCostUsd: snap.MaxCostUsd, Priority: existing.Priority,
		Effort: snap.Effort, PermissionMode: snap.PermissionMode,
		FallbackProviderConfigIds: fallbacks,
		EscalationLadder:          ladderJSON,
		ID:                        id,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if behaviourChanged(existing, cfg) {
		note := strings.TrimSpace(body.Note)
		if note == "" {
			note = fmt.Sprintf("Restored from v%d", n)
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
	JSON(w, http.StatusOK, h.safeConfig(r, cfg))
}
