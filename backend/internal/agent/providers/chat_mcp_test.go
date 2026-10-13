package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/scopedtoken"
)

func TestNewChatMCPProvisioner_EmptyBinaryDisabled(t *testing.T) {
	if p := NewChatMCPProvisioner("", "http://localhost:8080"); p != nil {
		t.Fatal("expected nil provisioner when binary is empty")
	}
}

func TestChatMCPProvisioner_Claude_WritesMCPConfig(t *testing.T) {
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080")
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
	tok := entry.Env["API_TOKEN"]
	if !strings.HasPrefix(tok, "ate_") || len(tok) < 64 {
		t.Errorf("API_TOKEN should be a scoped token, got %q", tok)
	}
	g, live := scopedtoken.Default.Lookup(tok)
	if !live || g.Kind != scopedtoken.KindChat || g.Chat != "sess-1" {
		t.Errorf("token not a live chat grant for sess-1: %+v live=%v", g, live)
	}
	// The config lives in a private 0700 per-session dir.
	st, serr := os.Stat(filepath.Dir(file))
	if serr != nil || st.Mode().Perm() != 0700 {
		t.Errorf("config dir perms: %v %v", st, serr)
	}

	// cleanup removes the temp files and revokes the token.
	cleanup()
	if _, statErr := os.Stat(file); !os.IsNotExist(statErr) {
		t.Errorf("cleanup did not remove config file %s", file)
	}
	if _, live := scopedtoken.Default.Lookup(tok); live {
		t.Error("chat token still valid after cleanup")
	}
}

func TestChatMCPProvisioner_NeverEmbedsAdminToken(t *testing.T) {
	t.Setenv("API_TOKEN", "admin-secret")
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080")
	for _, prov := range []string{"claude", "codex_cli"} {
		args, env, cleanup, err := p(prov, "sess-x-"+prov)
		if err != nil {
			t.Fatal(err)
		}
		var blob string
		if len(args) == 2 {
			b, _ := os.ReadFile(args[1])
			blob = string(b)
		}
		for _, e := range env {
			if strings.HasPrefix(e, "CODEX_HOME=") {
				b, _ := os.ReadFile(filepath.Join(strings.TrimPrefix(e, "CODEX_HOME="), "config.toml"))
				blob += string(b)
			}
		}
		if blob == "" || strings.Contains(blob, "admin-secret") {
			t.Errorf("%s: config missing or contains admin token: %q", prov, blob)
		}
		cleanup()
	}
}

func TestChatMCPProvisioner_Codex_WritesConfigToml(t *testing.T) {
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080")
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
	p := NewChatMCPProvisioner("/app/mcp-board", "http://localhost:8080")
	args, env, cleanup, err := p("opencode", "sess-5")
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	defer cleanup()
	if len(args) != 0 || len(env) != 0 {
		t.Errorf("opencode should get no injection; args=%v env=%v", args, env)
	}
}
