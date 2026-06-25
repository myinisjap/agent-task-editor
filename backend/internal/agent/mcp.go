package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MCPConfig is written as JSON and passed to the Claude CLI via --mcp-config.
type mcpServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
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

// TransitionHint describes an available transition for the MCP sidecar.
type TransitionHint struct {
	ToLabel string `json:"to_label"`
	Path    string `json:"path"` // success | failure | either
}

// Prepare creates temp files for one agent run and returns the config.
// transitions is the list of agent-available transitions from the task's current label.
// The caller must call Cleanup when the run ends.
func (m *MCPManager) Prepare(runID string, transitions []TransitionHint) (*MCPRunConfig, error) {
	dir := os.TempDir()

	resultFile := filepath.Join(dir, fmt.Sprintf("ate-result-%s.json", runID))
	configFile := filepath.Join(dir, fmt.Sprintf("ate-mcp-%s.json", runID))

	transitionsJSON, err := json.Marshal(transitions)
	if err != nil {
		return nil, fmt.Errorf("marshal transitions: %w", err)
	}

	cfg := mcpConfig{
		MCPServers: map[string]mcpServerEntry{
			"task-editor": {
				Command: m.ServerBinary,
				Env: map[string]string{
					"RUN_ID":      runID,
					"RESULT_FILE": resultFile,
					"TRANSITIONS": string(transitionsJSON),
				},
			},
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	if err := os.WriteFile(configFile, data, 0600); err != nil {
		return nil, fmt.Errorf("write mcp config: %w", err)
	}

	return &MCPRunConfig{ConfigFile: configFile, ResultFile: resultFile}, nil
}

// Cleanup removes the temp files created by Prepare.
func (c *MCPRunConfig) Cleanup() {
	_ = os.Remove(c.ConfigFile)
	_ = os.Remove(c.ResultFile)
}

// ReadResult reads and parses the result written by the MCP sidecar.
// Returns a zero Result with Status="completed" if the file does not exist
// (agent finished without calling signal_complete).
func (c *MCPRunConfig) ReadResult() Result {
	data, err := os.ReadFile(c.ResultFile)
	if err != nil {
		status := "completed"
		return Result{Status: status}
	}

	var r Result
	if err := json.Unmarshal(data, &r); err != nil {
		status := "completed"
		return Result{Status: status}
	}
	return r
}
