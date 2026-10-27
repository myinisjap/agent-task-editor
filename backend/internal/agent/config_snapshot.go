package agent

import (
	"encoding/json"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ConfigSnapshot is the behaviour-relevant slice of an agent config that is
// stored per version (and as an experiment's candidate). Everything except
// name, labels, enabled and priority (identity / routing) is included. The
// JSON keys must match the backfill in migration 075.
type ConfigSnapshot struct {
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

// JSONTextOrEmptyArray returns raw if it is valid JSON, else "[]".
func JSONTextOrEmptyArray(raw string) json.RawMessage {
	if strings.TrimSpace(raw) == "" || !json.Valid([]byte(raw)) {
		return json.RawMessage("[]")
	}
	return json.RawMessage(raw)
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// SnapshotOf extracts the snapshot of cfg.
func SnapshotOf(cfg gen.AgentConfig) ConfigSnapshot {
	return ConfigSnapshot{
		SystemPrompt:              cfg.SystemPrompt,
		ProviderConfigID:          cfg.ProviderConfigID,
		FallbackProviderConfigIDs: JSONTextOrEmptyArray(cfg.FallbackProviderConfigIds),
		EscalationLadder:          JSONTextOrEmptyArray(cfg.EscalationLadder),
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
		EnabledPlugins:            JSONTextOrEmptyArray(cfg.EnabledPlugins),
		EnabledMcpServers:         JSONTextOrEmptyArray(cfg.EnabledMcpServers),
		CommandAllowlist:          JSONTextOrEmptyArray(cfg.CommandAllowlist),
		CommandDenylist:           JSONTextOrEmptyArray(cfg.CommandDenylist),
	}
}

// ApplyTo overlays the snapshot onto cfg, keeping id, name, labels, enabled,
// priority and timestamps. cfg is passed by value so the caller's copy is
// never mutated.
func (s ConfigSnapshot) ApplyTo(cfg gen.AgentConfig) gen.AgentConfig {
	cfg.SystemPrompt = s.SystemPrompt
	cfg.ProviderConfigID = s.ProviderConfigID
	cfg.FallbackProviderConfigIds = string(JSONTextOrEmptyArray(string(s.FallbackProviderConfigIDs)))
	cfg.EscalationLadder = string(JSONTextOrEmptyArray(string(s.EscalationLadder)))
	cfg.Effort = s.Effort
	cfg.PermissionMode = s.PermissionMode
	cfg.MaxTurns = s.MaxTurns
	cfg.MaxTokens = s.MaxTokens
	cfg.TimeoutSecs = s.TimeoutSecs
	cfg.MaxCostUsd = s.MaxCostUsd
	cfg.MaxRetries = s.MaxRetries
	cfg.RetryBackoffSecs = s.RetryBackoffSecs
	cfg.ResumeSessions = b2i(s.ResumeSessions)
	cfg.SubtasksEnabled = b2i(s.SubtasksEnabled)
	cfg.MaxSubtasks = s.MaxSubtasks
	cfg.EnabledPlugins = string(JSONTextOrEmptyArray(string(s.EnabledPlugins)))
	cfg.EnabledMcpServers = string(JSONTextOrEmptyArray(string(s.EnabledMcpServers)))
	cfg.CommandAllowlist = string(JSONTextOrEmptyArray(string(s.CommandAllowlist)))
	cfg.CommandDenylist = string(JSONTextOrEmptyArray(string(s.CommandDenylist)))
	return cfg
}
