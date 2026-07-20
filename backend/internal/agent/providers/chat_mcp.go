package providers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// boardMCPServerName is the MCP server name the board tools (list_repos,
// list_workflows, create_task) are registered under for interactive chat
// sessions. Kept distinct from the task sidecar's "task-editor" so the two never
// collide if both are ever present.
const boardMCPServerName = "task-editor-board"

// NewChatMCPProvisioner returns an agent.ChatMCPProvisioner that exposes the
// board MCP server to a chat session's interactive CLI, so the chat can create
// tickets. binary is the path to the mcp-board executable; backendURL and
// apiToken are how that executable reaches the REST API.
//
// Returns nil when binary is empty — chat sessions then launch unchanged (board
// tools disabled), mirroring how an empty MCP_SERVER_PATH disables the task
// sidecar.
//
// The wiring mirrors the task sidecar's per-provider mechanisms:
//   - claude / qwen_code: a per-session --mcp-config JSON file
//   - gemini_cli: a per-session GEMINI_CLI_HOME with .gemini/settings.json
//     (plus --skip-trust so the CLI loads MCP servers in the worktree)
//   - codex_cli: a per-session CODEX_HOME with config.toml
//   - anything else (e.g. opencode): no injection (no per-invocation mechanism)
func NewChatMCPProvisioner(binary, backendURL, apiToken string) agent.ChatMCPProvisioner {
	if binary == "" {
		return nil
	}
	return func(provider, sessionID string) ([]string, []string, func(), error) {
		noop := func() {}
		entry := mcpServerEntry{
			Type:    "stdio",
			Command: binary,
			Args:    []string{},
			Env:     map[string]string{"BACKEND_URL": backendURL},
		}
		if apiToken != "" {
			entry.Env["API_TOKEN"] = apiToken
		}

		switch provider {
		case "claude", "qwen_code":
			cfg := mcpConfig{MCPServers: map[string]mcpServerEntry{boardMCPServerName: entry}}
			data, err := json.Marshal(cfg)
			if err != nil {
				return nil, nil, noop, fmt.Errorf("marshal chat mcp config: %w", err)
			}
			file := filepath.Join(os.TempDir(), fmt.Sprintf("ate-chat-mcp-%s.json", sessionID))
			if err := os.WriteFile(file, data, 0600); err != nil {
				return nil, nil, noop, fmt.Errorf("write chat mcp config: %w", err)
			}
			return []string{"--mcp-config", file}, nil, func() { _ = os.Remove(file) }, nil

		case "gemini_cli":
			dir := filepath.Join(os.TempDir(), fmt.Sprintf("ate-chat-gemini-%s", sessionID))
			geminiDir := filepath.Join(dir, ".gemini")
			if err := os.MkdirAll(geminiDir, 0700); err != nil {
				return nil, nil, noop, fmt.Errorf("mkdir gemini home: %w", err)
			}
			settings := mcpConfig{MCPServers: map[string]mcpServerEntry{boardMCPServerName: entry}}
			data, err := json.Marshal(settings)
			if err != nil {
				return nil, nil, noop, fmt.Errorf("marshal gemini settings: %w", err)
			}
			if err := os.WriteFile(filepath.Join(geminiDir, "settings.json"), data, 0600); err != nil {
				return nil, nil, noop, fmt.Errorf("write gemini settings: %w", err)
			}
			return []string{"--skip-trust"}, []string{"GEMINI_CLI_HOME=" + dir}, func() { _ = os.RemoveAll(dir) }, nil

		case "codex_cli":
			dir := filepath.Join(os.TempDir(), fmt.Sprintf("ate-chat-codex-%s", sessionID))
			if err := os.MkdirAll(dir, 0700); err != nil {
				return nil, nil, noop, fmt.Errorf("mkdir codex home: %w", err)
			}
			toml := renderCodexMCPTOML(boardMCPServerName, entry)
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(toml), 0600); err != nil {
				return nil, nil, noop, fmt.Errorf("write codex config.toml: %w", err)
			}
			return nil, []string{"CODEX_HOME=" + dir}, func() { _ = os.RemoveAll(dir) }, nil

		default:
			return nil, nil, noop, nil
		}
	}
}
