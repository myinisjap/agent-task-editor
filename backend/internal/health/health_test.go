package health

import (
	"errors"
	"testing"
)

// fakeDeps builds a Deps whose behaviour is fully driven by the maps/values
// passed in, so checks can be exercised without touching the real environment.
func fakeDeps(onPath map[string]bool, files map[string]bool, env map[string]string, home string, ghAuthed bool) *Deps {
	return &Deps{
		LookPath: func(bin string) (string, error) {
			if onPath[bin] {
				return "/usr/bin/" + bin, nil
			}
			return "", errors.New("not found")
		},
		FileExists: func(p string) bool { return files[p] },
		Getenv:     func(k string) string { return env[k] },
		HomeDir:    func() (string, error) { return home, nil },
		GHAuthStatus: func() (bool, string) {
			if ghAuthed {
				return true, "gh auth"
			}
			return false, "no creds"
		},
	}
}

// find returns the check with the given id, or fails the test.
func find(t *testing.T, checks []Check, id string) Check {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q not found in %+v", id, checks)
	return Check{}
}

func hasID(checks []Check, id string) bool {
	for _, c := range checks {
		if c.ID == id {
			return true
		}
	}
	return false
}

func TestClaudeCheck_MissingBinary(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{}, d), "claude_cli")
	if got.Status != StatusError {
		t.Fatalf("status = %q, want error", got.Status)
	}
	if got.Hint == "" {
		t.Fatalf("expected a fix hint")
	}
}

func TestClaudeCheck_InstalledButNoCreds(t *testing.T) {
	d := fakeDeps(map[string]bool{"claude": true}, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{}, d), "claude_cli")
	if got.Status != StatusWarn {
		t.Fatalf("status = %q, want warn", got.Status)
	}
}

func TestClaudeCheck_AuthViaCredentialsFile(t *testing.T) {
	files := map[string]bool{"/home/u/.claude/.credentials.json": true}
	d := fakeDeps(map[string]bool{"claude": true}, files, nil, "/home/u", false)
	got := find(t, Checks(Input{}, d), "claude_cli")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestClaudeCheck_AuthViaEnv(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
	d := fakeDeps(map[string]bool{"claude": true}, nil, env, "/home/u", false)
	got := find(t, Checks(Input{}, d), "claude_cli")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestProviderChecks_OnlyEmittedWhenUsed(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	checks := Checks(Input{}, d)
	// No providers configured → no qwen/opencode/anthropic/llm rows.
	for _, id := range []string{"qwen_cli", "opencode_cli", "anthropic_api", "llm_api"} {
		if hasID(checks, id) {
			t.Fatalf("did not expect check %q when provider unused", id)
		}
	}

	checks = Checks(Input{Providers: map[string]bool{"qwen_code": true, "opencode": true}}, d)
	if !hasID(checks, "qwen_cli") || !hasID(checks, "opencode_cli") {
		t.Fatalf("expected qwen/opencode checks when providers in use")
	}
	if find(t, checks, "qwen_cli").Status != StatusError {
		t.Fatalf("qwen missing binary should be error")
	}
}

func TestBinaryCheck_Present(t *testing.T) {
	d := fakeDeps(map[string]bool{"qwen": true}, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"qwen_code": true}}, d), "qwen_cli")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestAnthropicCheck_KeyPresence(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"anthropic": true}}, d), "anthropic_api")
	if got.Status != StatusError {
		t.Fatalf("missing key should be error, got %q", got.Status)
	}
	got = find(t, Checks(Input{Providers: map[string]bool{"anthropic": true}, LLMAPIKey: "k"}, d), "anthropic_api")
	if got.Status != StatusOK {
		t.Fatalf("key present should be ok, got %q", got.Status)
	}
}

func TestLLMCheck_KeyAndBaseURL(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	// No key.
	got := find(t, Checks(Input{Providers: map[string]bool{"llm": true}}, d), "llm_api")
	if got.Status != StatusError {
		t.Fatalf("missing key should be error, got %q", got.Status)
	}
	// Key but no base URL.
	got = find(t, Checks(Input{Providers: map[string]bool{"llm": true}, LLMAPIKey: "k"}, d), "llm_api")
	if got.Status != StatusWarn {
		t.Fatalf("missing base URL should warn, got %q", got.Status)
	}
	// Both present.
	got = find(t, Checks(Input{Providers: map[string]bool{"llm": true}, LLMAPIKey: "k", LLMBaseURL: "https://x"}, d), "llm_api")
	if got.Status != StatusOK {
		t.Fatalf("key+baseurl should be ok, got %q", got.Status)
	}
}

func TestMCPCheck(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{}, d), "mcp_sidecar")
	if got.Status != StatusWarn {
		t.Fatalf("unset MCP should warn, got %q", got.Status)
	}

	got = find(t, Checks(Input{MCPBinary: "/opt/mcp"}, d), "mcp_sidecar")
	if got.Status != StatusError {
		t.Fatalf("configured-but-missing MCP should error, got %q", got.Status)
	}

	d = fakeDeps(nil, map[string]bool{"/opt/mcp": true}, nil, "/home/u", false)
	got = find(t, Checks(Input{MCPBinary: "/opt/mcp"}, d), "mcp_sidecar")
	if got.Status != StatusOK {
		t.Fatalf("present MCP should be ok, got %q", got.Status)
	}
}

func TestGHCheck(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", true)
	if find(t, Checks(Input{}, d), "gh_auth").Status != StatusOK {
		t.Fatalf("authed gh should be ok")
	}
	d = fakeDeps(nil, nil, nil, "/home/u", false)
	if find(t, Checks(Input{}, d), "gh_auth").Status != StatusWarn {
		t.Fatalf("unauthed gh should warn")
	}
}

func TestRepoBaseDirCheck(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	if find(t, Checks(Input{}, d), "repo_base_dir").Status != StatusWarn {
		t.Fatalf("unset repo base dir should warn")
	}
	d = fakeDeps(nil, nil, nil, "/home/u", false)
	if find(t, Checks(Input{RepoBaseDir: "/nope"}, d), "repo_base_dir").Status != StatusError {
		t.Fatalf("missing repo base dir should error")
	}
	d = fakeDeps(nil, map[string]bool{"/repos": true}, nil, "/home/u", false)
	if find(t, Checks(Input{RepoBaseDir: "/repos"}, d), "repo_base_dir").Status != StatusOK {
		t.Fatalf("existing repo base dir should be ok")
	}
}
