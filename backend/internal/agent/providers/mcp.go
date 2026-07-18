package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
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
	// APIToken is the bearer token, when the backend requires one.
	APIToken string
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
	dir := os.TempDir()

	resultFile := filepath.Join(dir, fmt.Sprintf("ate-result-%s.json", runID))
	configFile := filepath.Join(dir, fmt.Sprintf("ate-mcp-%s.json", runID))

	transitionsJSON, err := json.Marshal(transitions)
	if err != nil {
		return nil, fmt.Errorf("marshal transitions: %w", err)
	}
	reviewCommentsJSON, err := json.Marshal(reviewComments)
	if err != nil {
		return nil, fmt.Errorf("marshal review comments: %w", err)
	}

	env := map[string]string{
		"RUN_ID":          runID,
		"RESULT_FILE":     resultFile,
		"TRANSITIONS":     string(transitionsJSON),
		"REVIEW_COMMENTS": string(reviewCommentsJSON),
	}
	// Expose create_subtask only when the run's config opted in. The sidecar
	// posts children live to the backend REST API, so it needs the base URL,
	// bearer token, and parent task id.
	if subtasks != nil && subtasks.Enabled {
		env["SUBTASKS_ENABLED"] = "1"
		env["BACKEND_URL"] = subtasks.BackendURL
		env["TASK_ID"] = subtasks.TaskID
		env["API_TOKEN"] = subtasks.APIToken
		env["MAX_SUBTASKS"] = fmt.Sprintf("%d", subtasks.MaxSubtasks)
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
		return nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	if err := os.WriteFile(configFile, data, 0600); err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	return &MCPRunConfig{ConfigFile: configFile, ResultFile: resultFile}, nil
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

// Cleanup removes the temp files created by Prepare.
func (c *MCPRunConfig) Cleanup() {
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
