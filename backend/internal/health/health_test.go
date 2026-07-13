package health

import (
	"errors"
	"strings"
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
	// No providers configured → no qwen/opencode/anthropic/llm/gemini/codex rows.
	for _, id := range []string{"qwen_cli", "opencode_cli", "anthropic_api", "llm_api", "gemini_cli", "codex_cli"} {
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

	checks = Checks(Input{Providers: map[string]bool{"gemini_cli": true, "codex_cli": true}}, d)
	if !hasID(checks, "gemini_cli") || !hasID(checks, "codex_cli") {
		t.Fatalf("expected gemini/codex checks when providers in use")
	}
}

func TestGeminiCheck_MissingBinary(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"gemini_cli": true}}, d), "gemini_cli")
	if got.Status != StatusError {
		t.Fatalf("status = %q, want error", got.Status)
	}
	if got.Hint == "" {
		t.Fatalf("expected a fix hint")
	}
}

func TestGeminiCheck_InstalledButNoCreds(t *testing.T) {
	d := fakeDeps(map[string]bool{"gemini": true}, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"gemini_cli": true}}, d), "gemini_cli")
	if got.Status != StatusWarn {
		t.Fatalf("status = %q, want warn", got.Status)
	}
}

func TestGeminiCheck_AuthViaEnv(t *testing.T) {
	env := map[string]string{"GEMINI_API_KEY": "k"}
	d := fakeDeps(map[string]bool{"gemini": true}, nil, env, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"gemini_cli": true}}, d), "gemini_cli")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}

	env = map[string]string{"GOOGLE_API_KEY": "k"}
	d = fakeDeps(map[string]bool{"gemini": true}, nil, env, "/home/u", false)
	got = find(t, Checks(Input{Providers: map[string]bool{"gemini_cli": true}}, d), "gemini_cli")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok (GOOGLE_API_KEY)", got.Status)
	}
}

func TestGeminiCheck_AuthViaCredentialsFile(t *testing.T) {
	files := map[string]bool{"/home/u/.gemini/oauth_creds.json": true}
	d := fakeDeps(map[string]bool{"gemini": true}, files, nil, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"gemini_cli": true}}, d), "gemini_cli")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestCodexCheck_MissingBinary(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"codex_cli": true}}, d), "codex_cli")
	if got.Status != StatusError {
		t.Fatalf("status = %q, want error", got.Status)
	}
	if got.Hint == "" {
		t.Fatalf("expected a fix hint")
	}
}

func TestCodexCheck_InstalledButNoCreds(t *testing.T) {
	d := fakeDeps(map[string]bool{"codex": true}, nil, nil, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"codex_cli": true}}, d), "codex_cli")
	if got.Status != StatusWarn {
		t.Fatalf("status = %q, want warn", got.Status)
	}
}

func TestCodexCheck_AuthViaEnv(t *testing.T) {
	env := map[string]string{"OPENAI_API_KEY": "k"}
	d := fakeDeps(map[string]bool{"codex": true}, nil, env, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"codex_cli": true}}, d), "codex_cli")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestCodexCheck_AuthViaCredentialsFile(t *testing.T) {
	files := map[string]bool{"/home/u/.codex/auth.json": true}
	d := fakeDeps(map[string]bool{"codex": true}, files, nil, "/home/u", false)
	got := find(t, Checks(Input{Providers: map[string]bool{"codex_cli": true}}, d), "codex_cli")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
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

func TestDBSizeCheck(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)

	// Unreadable/zero size: warn, but never error (a large DB isn't itself a
	// failure state).
	got := find(t, Checks(Input{}, d), "db_size")
	if got.Status != StatusWarn {
		t.Fatalf("status = %q, want warn when size is 0/unreadable", got.Status)
	}

	got = find(t, Checks(Input{DBSizeBytes: 44_356_812, AgentLogsCount: 118204}, d), "db_size")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if !strings.Contains(got.Detail, "MB") {
		t.Errorf("expected human-readable MB size in detail, got %q", got.Detail)
	}
	if !strings.Contains(got.Detail, "118,204") {
		t.Errorf("expected thousands-separated row count in detail, got %q", got.Detail)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatCount(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{118204, "118,204"},
		{1234567, "1,234,567"},
	}
	for _, c := range cases {
		if got := formatCount(c.in); got != c.want {
			t.Errorf("formatCount(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVersionCheck(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)

	got := find(t, Checks(Input{}, d), "version")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Detail != "running dev (unreleased build)" {
		t.Errorf("expected dev detail, got %q", got.Detail)
	}

	got = find(t, Checks(Input{Version: "v1.4.0"}, d), "version")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Detail != "running v1.4.0" {
		t.Errorf("expected 'running v1.4.0', got %q", got.Detail)
	}
}

func TestUpdateCheck_DisabledByDefault(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	checks := Checks(Input{Version: "v1.0.0"}, d)
	if hasID(checks, "update_check") {
		t.Fatalf("did not expect update_check row when CheckForUpdates is false")
	}
}

func TestUpdateCheck_UpToDate(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	d.LatestGitHubRelease = func() (string, bool) { return "v1.4.0", true }
	got := find(t, Checks(Input{Version: "v1.4.0", CheckForUpdates: true}, d), "update_check")
	if got.Status != StatusOK {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if !strings.Contains(got.Detail, "up to date") {
		t.Errorf("expected 'up to date' in detail, got %q", got.Detail)
	}
}

func TestUpdateCheck_UpdateAvailable(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	d.LatestGitHubRelease = func() (string, bool) { return "v1.5.0", true }
	got := find(t, Checks(Input{Version: "v1.4.0", CheckForUpdates: true}, d), "update_check")
	if got.Status != StatusWarn {
		t.Fatalf("status = %q, want warn", got.Status)
	}
	if !strings.Contains(got.Detail, "update available: v1.5.0") {
		t.Errorf("expected 'update available: v1.5.0' in detail, got %q", got.Detail)
	}
	if got.Hint == "" {
		t.Errorf("expected a hint linking to releases")
	}
}

func TestUpdateCheck_FailsSoft(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	d.LatestGitHubRelease = func() (string, bool) { return "", false }
	got := find(t, Checks(Input{Version: "v1.4.0", CheckForUpdates: true}, d), "update_check")
	if got.Status != StatusWarn {
		t.Fatalf("status = %q, want warn (never error)", got.Status)
	}
	if !strings.Contains(got.Detail, "could not check for updates") {
		t.Errorf("expected 'could not check for updates' in detail, got %q", got.Detail)
	}
}

func TestUpdateCheck_DevBuildIsInformationalOnly(t *testing.T) {
	d := fakeDeps(nil, nil, nil, "/home/u", false)
	d.LatestGitHubRelease = func() (string, bool) { return "v1.5.0", true }
	got := find(t, Checks(Input{Version: "dev", CheckForUpdates: true}, d), "update_check")
	if got.Status != StatusWarn {
		t.Fatalf("status = %q, want warn for a dev build", got.Status)
	}
	if !strings.Contains(got.Detail, "v1.5.0") {
		t.Errorf("expected latest tag mentioned in detail, got %q", got.Detail)
	}
}

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.4.0", "v1.5.0", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.4.0-rc1", "v1.4.0", 0},
	}
	for _, c := range cases {
		if got := semverCompare(c.a, c.b); got != c.want {
			t.Errorf("semverCompare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
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
