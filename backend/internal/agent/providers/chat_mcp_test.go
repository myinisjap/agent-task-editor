package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewChatMCPProvisioner_EmptyBinaryDisabled(t *testing.T) {
	if p := NewChatMCPProvisioner("", "http://localhost:8080", ""); p != nil {
		t.Fatal("expected nil provisioner when binary is empty")
	}
}

func TestChatMCPProvisioner_Claude_WritesMCPConfig(t *testing.T) {
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080", "secret")
	args, env, cleanup, err := p("claude", "sess-1")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer cleanup()

	if len(env) != 0 {
		t.Errorf("claude should use args, not env; got env %v", env)
	}
	if len(args) != 2 || args[0] != "--mcp-config" {
		t.Fatalf("expected [--mcp-config <file>], got %v", args)
	}
	file := args[1]
	data, rerr := os.ReadFile(file)
	if rerr != nil {
		t.Fatalf("read config file: %v", rerr)
	}
	var cfg mcpConfig
	if uerr := json.Unmarshal(data, &cfg); uerr != nil {
		t.Fatalf("unmarshal config: %v", uerr)
	}
	entry, ok := cfg.MCPServers[boardMCPServerName]
	if !ok {
		t.Fatalf("config missing %q server; got %v", boardMCPServerName, cfg.MCPServers)
	}
	if entry.Command != "/app/mcp-board" {
		t.Errorf("command: want /app/mcp-board, got %q", entry.Command)
	}
	if entry.Env["BACKEND_URL"] != "http://localhost:8080" {
		t.Errorf("BACKEND_URL: got %q", entry.Env["BACKEND_URL"])
	}
	if entry.Env["API_TOKEN"] != "secret" {
		t.Errorf("API_TOKEN: got %q", entry.Env["API_TOKEN"])
	}

	// cleanup removes the temp file.
	cleanup()
	if _, statErr := os.Stat(file); !os.IsNotExist(statErr) {
		t.Errorf("cleanup did not remove config file %s", file)
	}
}

func TestChatMCPProvisioner_Claude_NoTokenOmitsHeader(t *testing.T) {
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080", "")
	args, _, cleanup, err := p("claude", "sess-2")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer cleanup()

	data, _ := os.ReadFile(args[1])
	var cfg mcpConfig
	_ = json.Unmarshal(data, &cfg)
	if _, present := cfg.MCPServers[boardMCPServerName].Env["API_TOKEN"]; present {
		t.Error("API_TOKEN should be absent when no token configured")
	}
}

func TestChatMCPProvisioner_Gemini_WritesHomeAndSkipsTrust(t *testing.T) {
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080", "")
	args, env, cleanup, err := p("gemini_cli", "sess-3")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer cleanup()

	if len(args) != 1 || args[0] != "--skip-trust" {
		t.Errorf("expected [--skip-trust], got %v", args)
	}
	var home string
	for _, e := range env {
		if strings.HasPrefix(e, "GEMINI_CLI_HOME=") {
			home = strings.TrimPrefix(e, "GEMINI_CLI_HOME=")
		}
	}
	if home == "" {
		t.Fatalf("GEMINI_CLI_HOME not set; env %v", env)
	}
	settings := filepath.Join(home, ".gemini", "settings.json")
	if _, serr := os.Stat(settings); serr != nil {
		t.Fatalf("expected settings.json at %s: %v", settings, serr)
	}
	cleanup()
	if _, statErr := os.Stat(home); !os.IsNotExist(statErr) {
		t.Errorf("cleanup did not remove gemini home %s", home)
	}
}

func TestChatMCPProvisioner_Codex_WritesConfigToml(t *testing.T) {
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080", "tok")
	args, env, cleanup, err := p("codex_cli", "sess-4")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer cleanup()

	if len(args) != 0 {
		t.Errorf("codex should use env only, got args %v", args)
	}
	var home string
	for _, e := range env {
		if strings.HasPrefix(e, "CODEX_HOME=") {
			home = strings.TrimPrefix(e, "CODEX_HOME=")
		}
	}
	if home == "" {
		t.Fatalf("CODEX_HOME not set; env %v", env)
	}
	data, rerr := os.ReadFile(filepath.Join(home, "config.toml"))
	if rerr != nil {
		t.Fatalf("read config.toml: %v", rerr)
	}
	if !strings.Contains(string(data), "[mcp_servers."+boardMCPServerName+"]") {
		t.Errorf("config.toml missing board server section:\n%s", data)
	}
}

func TestChatMCPProvisioner_UnsupportedProvider_NoInjection(t *testing.T) {
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080", "")
	args, env, cleanup, err := p("opencode", "sess-5")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer cleanup()
	if len(args) != 0 || len(env) != 0 {
		t.Errorf("opencode should get no injection; args=%v env=%v", args, env)
	}
}
