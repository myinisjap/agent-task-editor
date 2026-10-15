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

// boardMCPServerName is the MCP server name the board tools (list_repos,
// list_workflows, create_task) are registered under for interactive chat
// sessions. Kept distinct from the task sidecar's "task-editor" so the two never
// collide if both are ever present.
const boardMCPServerName = "task-editor-board"

// NewChatMCPProvisioner returns an agent.ChatMCPProvisioner that exposes the
// board MCP server to a chat session's interactive CLI, so the chat can create
// tickets. binary is the path to the mcp-board executable; backendURL is how
// that executable reaches the REST API. It authenticates with a per-session
// scoped token (see scopedtoken) that only works for the routes mcp-board
// calls and is revoked when the session's cleanup runs -- never the admin
// API_TOKEN, which a same-user chat CLI could read from the config/env.
//
// Returns nil when binary is empty — chat sessions then launch unchanged (board
// tools disabled), mirroring how an empty MCP_SERVER_PATH disables the task
// sidecar.
//
// The wiring mirrors the task sidecar's per-provider mechanisms:
//   - claude / qwen_code: a per-session --mcp-config JSON file
//   - codex_cli: per-session `-c mcp_servers.<name>.*` config overrides
//   - anything else (e.g. opencode): no injection (no per-invocation mechanism)
func NewChatMCPProvisioner(binary, backendURL string) agent.ChatMCPProvisioner {
	if binary == "" {
		return nil
	}
	return func(provider, sessionID string) ([]string, []string, func(), error) {
		noop := func() {}
		switch provider {
		case "claude", "qwen_code", "codex_cli":
		default:
			return nil, nil, noop, nil
		}
		token, err := scopedtoken.Default.Issue(scopedtoken.Grant{Kind: scopedtoken.KindChat, Chat: sessionID})
		if err != nil {
			return nil, nil, noop, fmt.Errorf("mint chat token: %w", err)
		}
		entry := mcpServerEntry{
			Type:    "stdio",
			Command: binary,
			Args:    []string{},
			Env:     map[string]string{"BACKEND_URL": backendURL, "API_TOKEN": token},
		}
		revoke := func() { scopedtoken.Default.Revoke(token) }

		switch provider {
		case "claude", "qwen_code":
			cfg := mcpConfig{MCPServers: map[string]mcpServerEntry{boardMCPServerName: entry}}
			data, err := json.Marshal(cfg)
			if err != nil {
				revoke()
				return nil, nil, noop, fmt.Errorf("marshal chat mcp config: %w", err)
			}
			// The MCP config format embeds the sidecar env, so the file goes
			// in a private 0700 per-session dir (not a shared /tmp path).
			dir, err := os.MkdirTemp("", "ate-chat-mcp-")
			if err != nil {
				revoke()
				return nil, nil, noop, fmt.Errorf("create chat mcp dir: %w", err)
			}
			if err := agentuser.ShareDir(dir, false); err != nil {
				revoke()
				_ = os.RemoveAll(dir)
				return nil, nil, noop, fmt.Errorf("share chat mcp dir: %w", err)
			}
			file := filepath.Join(dir, "mcp.json")
			if err := os.WriteFile(file, data, 0600); err != nil {
				revoke()
				_ = os.RemoveAll(dir)
				return nil, nil, noop, fmt.Errorf("write chat mcp config: %w", err)
			}
			if err := agentuser.ShareFile(file); err != nil {
				revoke()
				_ = os.RemoveAll(dir)
				return nil, nil, noop, fmt.Errorf("share chat mcp config: %w", err)
			}
			return []string{"--mcp-config", file}, nil, func() { revoke(); _ = os.RemoveAll(dir) }, nil

		case "codex_cli":
			// -c overrides, not a per-session CODEX_HOME: that would hide the
			// user's `codex login` and session history (see codexMCPConfigArgs).
			return codexMCPConfigArgs(boardMCPServerName, entry), nil, revoke, nil
		}
		return nil, nil, noop, nil
	}
}
