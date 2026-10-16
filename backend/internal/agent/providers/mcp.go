package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
	"github.com/myinisjap/agent-task-editor/backend/internal/scopedtoken"
)

// MCPConfig is written as JSON and passed to the Claude CLI via --mcp-config.
type mcpServerEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// rawMCPConfig is used when merging in externally-defined server entries
// (e.g. from ~/.claude.json) whose shape we don't fully control/model.
type rawMCPConfig struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

type mcpConfig struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

// MCPRunConfig holds the temp file paths created for a single agent run.
type MCPRunConfig struct {
	ConfigFile string // path to the JSON config passed to --mcp-config
	ResultFile string // path where the sidecar writes its JSON result
	// Token is the per-run scoped bearer token handed to the sidecar ("" when
	// subtasks are disabled). It is revoked by Cleanup.
	Token string
	// Env is the sidecar's environment map (RUN_ID, RESULT_FILE,
	// TRANSITIONS, REVIEW_COMMENTS, and — when subtasks are enabled —
	// SUBTASKS_ENABLED/BACKEND_URL/TASK_ID/API_TOKEN/MAX_SUBTASKS). It is the
	// same map written into ConfigFile's "env" entry for the file-based
	// providers (claude/qwen/codex); exposed here so providers that deliver
	// MCP config inline (opencode's OPENCODE_CONFIG_CONTENT) can reuse it
	// without re-minting a token or re-marshaling transitions/comments. Do
	// not log this map — it contains the scoped API_TOKEN.
	Env map[string]string
	// dir is the 0700 per-run directory holding ConfigFile.
	dir string
}

// MCPManager generates per-run MCP sidecar configuration.
type MCPManager struct {
	// Path to the compiled mcp-server binary.
	ServerBinary string
}

// SubtaskEnv carries what the sidecar needs to expose the create_subtask tool,
// which writes live through the backend REST API (unlike the deferred
// result-file tools). Nil, or Enabled=false, means the tool isn't exposed.
type SubtaskEnv struct {
	// BackendURL is the base URL of the backend (same container, localhost).
	BackendURL string
	// TaskID is the parent task the created children hang under.
	TaskID string
	// Enabled gates whether the tool is exposed at all.
	Enabled bool
	// MaxSubtasks is surfaced to the agent in the tool description.
	MaxSubtasks int64
}

// Prepare creates temp files for one agent run and returns the config.
// transitions is the list of agent-available transitions from the task's current label.
// reviewComments is the list of open inline diff review comments on the task,
// passed so the sidecar's resolve_comment tool can validate comment IDs.
// extraServers, if non-nil, is merged into the mcpServers map as raw JSON entries
// (e.g. user-selected Claude MCP servers read from ~/.claude.json). A server named
// "task-editor" in extraServers is ignored to avoid colliding with the sidecar entry.
// The caller must call Cleanup when the run ends.
func (m *MCPManager) Prepare(runID string, transitions []agent.TransitionHint, reviewComments []agent.ReviewComment, extraServers map[string]json.RawMessage, subtasks *SubtaskEnv) (*MCPRunConfig, error) {
	// The per-run directory is created first because the sidecar's result file
	// lives inside it: the sidecar runs as the agent user and /tmp is sticky,
	// so a result file created directly in /tmp could not be removed by the
	// server afterwards. Inside this server-owned dir it can.
	dir, err := os.MkdirTemp("", "ate-mcp-")
	if err != nil {
		return nil, fmt.Errorf("create mcp dir: %w", err)
	}
	if err := agentuser.ShareDir(dir, true); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("share mcp dir: %w", err)
	}
	resultFile := filepath.Join(dir, "result.json")

	env, token, err := buildSidecarEnv(runID, resultFile, transitions, reviewComments, subtasks)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	cfg := mcpConfig{
		MCPServers: map[string]mcpServerEntry{
			"task-editor": {
				Type:    "stdio",
				Command: m.ServerBinary,
				Args:    []string{},
				Env:     env,
			},
		},
	}

	data, err := marshalMCPConfigWithExtras(cfg, extraServers)
	if err != nil {
		scopedtoken.Default.Revoke(token)
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	// The MCP config format forces the sidecar env (including the scoped
	// token) into the file, so it lives in the private per-run directory
	// (0700, or 0770 shared with the agent group when the agent runs as a
	// separate user; never a predictable shared /tmp path) and is removed by
	// Cleanup.
	configFile := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(configFile, data, 0600); err != nil {
		scopedtoken.Default.Revoke(token)
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("write mcp config: %w", err)
	}
	// The sidecar is the agent's child and must read this file as the agent user.
	if err := agentuser.ShareFile(configFile); err != nil {
		scopedtoken.Default.Revoke(token)
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("share mcp config: %w", err)
	}

	return &MCPRunConfig{ConfigFile: configFile, ResultFile: resultFile, Token: token, Env: env, dir: dir}, nil
}

// buildSidecarEnv builds the env map handed to the MCP sidecar process and,
// when subtasks are enabled, mints the scoped bearer token the create_subtask
// tool uses. This is the single source of truth for the sidecar env contract
// (RUN_ID, RESULT_FILE, TRANSITIONS, REVIEW_COMMENTS, and the optional
// SUBTASKS_ENABLED/BACKEND_URL/TASK_ID/API_TOKEN/MAX_SUBTASKS) shared by every
// provider's MCP delivery mechanism — the file-based --mcp-config providers
// (claude/qwen/codex) via Prepare's mcpServerEntry.Env, and opencode's inline
// OPENCODE_CONFIG_CONTENT mcp.*.environment via MCPRunConfig.Env. Adding a new
// sidecar env var belongs here, not duplicated at each call site (see ADR
// 0004). The returned token is "" when subtasks is nil or disabled; on error
// the caller is responsible for cleanup (this function mints a token it
// cannot itself revoke on an error it didn't cause).
func buildSidecarEnv(runID, resultFile string, transitions []agent.TransitionHint, reviewComments []agent.ReviewComment, subtasks *SubtaskEnv) (map[string]string, string, error) {
	transitionsJSON, err := json.Marshal(transitions)
	if err != nil {
		return nil, "", fmt.Errorf("marshal transitions: %w", err)
	}
	reviewCommentsJSON, err := json.Marshal(reviewComments)
	if err != nil {
		return nil, "", fmt.Errorf("marshal review comments: %w", err)
	}

	env := map[string]string{
		"RUN_ID":          runID,
		"RESULT_FILE":     resultFile,
		"TRANSITIONS":     string(transitionsJSON),
		"REVIEW_COMMENTS": string(reviewCommentsJSON),
	}
	token := ""
	// Expose create_subtask only when the run's config opted in. The sidecar
	// posts children live to the backend REST API, so it needs the base URL,
	// bearer token, and parent task id.
	if subtasks != nil && subtasks.Enabled {
		env["SUBTASKS_ENABLED"] = "1"
		env["BACKEND_URL"] = subtasks.BackendURL
		env["TASK_ID"] = subtasks.TaskID
		// Never the admin API_TOKEN: agents share the backend's OS user and
		// can read this env/config. Mint a token that only works for
		// POST /tasks/<this task>/subtasks and dies in Cleanup.
		tok, err := scopedtoken.Default.Issue(scopedtoken.Grant{Kind: scopedtoken.KindRun, RunID: runID, TaskID: subtasks.TaskID})
		if err != nil {
			return nil, "", fmt.Errorf("mint sidecar token: %w", err)
		}
		token = tok
		env["API_TOKEN"] = tok
		env["MAX_SUBTASKS"] = fmt.Sprintf("%d", subtasks.MaxSubtasks)
	}
	return env, token, nil
}

// marshalMCPConfigWithExtras marshals cfg to JSON and merges in extraServers
// (raw JSON entries) under mcpServers, skipping any name that collides with
// a server already present in cfg (e.g. "task-editor").
func marshalMCPConfigWithExtras(cfg mcpConfig, extraServers map[string]json.RawMessage) ([]byte, error) {
	if len(extraServers) == 0 {
		return json.Marshal(cfg)
	}
	merged := map[string]json.RawMessage{}
	for name, entry := range cfg.MCPServers {
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		merged[name] = raw
	}
	for name, raw := range extraServers {
		if _, exists := merged[name]; exists {
			continue
		}
		merged[name] = raw
	}
	return json.Marshal(rawMCPConfig{MCPServers: merged})
}

// Cleanup revokes the run's scoped token and removes the temp files created by Prepare.
func (c *MCPRunConfig) Cleanup() {
	scopedtoken.Default.Revoke(c.Token)
	if c.dir != "" {
		_ = os.RemoveAll(c.dir)
	}
	_ = os.Remove(c.ConfigFile)
	_ = os.Remove(c.ResultFile)
}

// ReadResult reads and parses the result written by the MCP sidecar.
// Returns a zero Result with Status="completed" if the file does not exist
// (agent finished without calling signal_complete).
func (c *MCPRunConfig) ReadResult() agent.Result {
	data, err := os.ReadFile(c.ResultFile)
	if err != nil {
		status := "completed"
		return agent.Result{Status: status}
	}

	var r agent.Result
	if err := json.Unmarshal(data, &r); err != nil {
		status := "completed"
		return agent.Result{Status: status}
	}
	// A result file with no Status means the sidecar persisted intermediate
	// state (e.g. resolve_comment calls) but the agent never reached
	// signal_complete/request_human — same semantics as a missing file.
	if r.Status == "" {
		r.Status = "completed"
	}
	return r
}
