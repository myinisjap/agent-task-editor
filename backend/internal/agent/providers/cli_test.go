package providers

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- sanitizeArgs (cli.go) ---

// TestSanitizeArgs_StripsNulBytes verifies a NUL byte anywhere in an arg is
// removed while the rest of the argument is preserved — regression coverage
// for the live EINVAL ("fork/exec ...: invalid argument") failure caused by a
// prior planning agent writing a literal '\x00' (suggested as a JS array
// join delimiter) into task notes, which then leaked into the "-p" prompt
// argument via buildPrompt's "NOTES FROM PRIOR AGENT" section.
func TestSanitizeArgs_StripsNulBytes(t *testing.T) {
	in := []string{"notes.join('\x00')"}
	out := sanitizeArgs(in)
	want := "notes.join('')"
	if out[0] != want {
		t.Errorf("sanitizeArgs(%q) = %q, want %q", in[0], out[0], want)
	}
}

// TestSanitizeArgs_LeavesCleanArgsUnchanged verifies args without a NUL byte
// pass through unmodified, including ordinary whitespace like newlines and
// tabs — prompts legitimately contain those and must not be touched.
func TestSanitizeArgs_LeavesCleanArgsUnchanged(t *testing.T) {
	in := []string{"-p", "line one\nline two\tindented", "--model", "sonnet"}
	out := sanitizeArgs(in)
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("sanitizeArgs mutated clean arg %d: got %q, want %q", i, out[i], in[i])
		}
	}
}

// TestMergeEnv_BlocksDangerousKeys verifies mergeEnv drops every key in
// dangerousEnvKeys (case-insensitively), regardless of how the caller cased
// it, while preserving benign keys unchanged. This is an untested security
// control (see #251 §4): user-supplied AgentConfig.Env values must never be
// able to hijack subprocess execution via PATH/LD_PRELOAD/etc.
func TestMergeEnv_BlocksDangerousKeys(t *testing.T) {
	base := []string{"EXISTING=1"}

	for key := range dangerousEnvKeys {
		t.Run(key, func(t *testing.T) {
			extra := map[string]string{key: "evil-value"}
			out := mergeEnv(base, extra)
			for _, kv := range out {
				if strings.HasPrefix(kv, key+"=") {
					t.Fatalf("mergeEnv did not block dangerous key %q: got %v", key, out)
				}
			}
		})
	}
}

// TestMergeEnv_BlocksDangerousKeys_CaseInsensitive verifies the block applies
// regardless of the case the caller used for the key (mergeEnv upper-cases
// before checking dangerousEnvKeys).
func TestMergeEnv_BlocksDangerousKeys_CaseInsensitive(t *testing.T) {
	out := mergeEnv(nil, map[string]string{"path": "/evil/bin", "Ld_Preload": "/evil.so"})
	for _, kv := range out {
		lower := strings.ToLower(kv)
		if strings.HasPrefix(lower, "path=") || strings.HasPrefix(lower, "ld_preload=") {
			t.Fatalf("mergeEnv did not block a differently-cased dangerous key: got %v", out)
		}
	}
}

// TestMergeEnv_PreservesBenignKeys verifies non-dangerous keys pass through
// unmodified, appended after the base slice.
func TestMergeEnv_PreservesBenignKeys(t *testing.T) {
	base := []string{"EXISTING=1"}
	out := mergeEnv(base, map[string]string{"MY_CUSTOM_VAR": "hello", "ANOTHER_ONE": "world"})

	want := map[string]bool{"EXISTING=1": false, "MY_CUSTOM_VAR=hello": false, "ANOTHER_ONE=world": false}
	for _, kv := range out {
		if _, ok := want[kv]; ok {
			want[kv] = true
		}
	}
	for kv, found := range want {
		if !found {
			t.Errorf("expected %q in merged env, got %v", kv, out)
		}
	}
	if len(out) != 3 {
		t.Errorf("len(out) = %d, want 3 (base + 2 benign extras), got %v", len(out), out)
	}
}

// TestMergeEnv_DoesNotMutateBase verifies mergeEnv doesn't alias/mutate the
// caller's base slice in place (base is os.Environ() in production — mutating
// it in place would be a subtle cross-call bug).
func TestMergeEnv_DoesNotMutateBase(t *testing.T) {
	base := []string{"A=1", "B=2"}
	baseCopy := append([]string(nil), base...)

	_ = mergeEnv(base, map[string]string{"C": "3"})

	for i := range base {
		if base[i] != baseCopy[i] {
			t.Errorf("mergeEnv mutated base in place: got %v, want %v", base, baseCopy)
		}
	}
}

// TestMergeEnv_EmptyExtra verifies a nil/empty extra map returns a copy of
// base unchanged.
func TestMergeEnv_EmptyExtra(t *testing.T) {
	base := []string{"A=1"}
	out := mergeEnv(base, nil)
	if len(out) != 1 || out[0] != "A=1" {
		t.Errorf("mergeEnv(base, nil) = %v, want %v", out, base)
	}
}

// --- spawn (cli.go) ---

// TestSpawn_TableTest covers both branches of spawn: empty Container runs the
// binary directly (workdir via cmd.Dir, env via cmd.Env), non-empty Container
// wraps it in `docker exec` with the env passed as -e flags and the workdir
// via -w, since docker exec has no equivalent to cmd.Dir/cmd.Env for a
// process already running in another container.
func TestSpawn_TableTest(t *testing.T) {
	env := []string{"FOO=bar", "BAZ=qux"}

	tests := []struct {
		name     string
		rt       runtimeSpec
		wantPath string
		wantArgs []string
		wantDir  string
		wantEnv  []string
	}{
		{
			name:     "empty container runs binary directly",
			rt:       runtimeSpec{},
			wantPath: "mybin",
			wantArgs: []string{"mybin", "-p", "hello"},
			wantDir:  "/repo/path",
			wantEnv:  env,
		},
		{
			name:     "set container wraps in docker exec",
			rt:       runtimeSpec{Container: "ate-repo-1"},
			wantPath: "docker",
			wantArgs: []string{"docker", "exec", "-i", "-w", "/repo/path", "-e", "FOO=bar", "-e", "BAZ=qux", "ate-repo-1", "mybin", "-p", "hello"},
			wantDir:  "",  // docker exec runs from the backend's own cwd; -w sets the *container's* workdir instead
			wantEnv:  nil, // env travels as -e flags in Args, not cmd.Env, since docker exec doesn't inherit the caller's env into the target container
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := spawn(context.Background(), tt.rt, "/repo/path", "mybin", []string{"-p", "hello"}, env)

			if !strings.HasSuffix(cmd.Path, tt.wantPath) && cmd.Path != tt.wantPath {
				// exec.CommandContext resolves bare names via LookPath, which may
				// return an absolute path (e.g. "/usr/bin/docker") or the bare
				// name unchanged if not found on PATH in the test environment.
				if filepath.Base(cmd.Path) != tt.wantPath {
					t.Errorf("cmd.Path = %q, want basename %q", cmd.Path, tt.wantPath)
				}
			}
			if !reflect.DeepEqual(cmd.Args, tt.wantArgs) {
				t.Errorf("cmd.Args = %v, want %v", cmd.Args, tt.wantArgs)
			}
			if cmd.Dir != tt.wantDir {
				t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, tt.wantDir)
			}
			if !reflect.DeepEqual(cmd.Env, tt.wantEnv) {
				t.Errorf("cmd.Env = %v, want %v", cmd.Env, tt.wantEnv)
			}
		})
	}
}

// --- allowlistEnv / per-provider allowlists (cli.go, #321) ---

// allProviderAllowlists lists every per-provider allowlist so the shared
// regression checks below (PATH/HOME present, backend secrets absent) run
// against all of them without duplicating the test body per provider.
var allProviderAllowlists = map[string]map[string]bool{
	"claude":   claudeEnvAllowlist,
	"codex":    codexEnvAllowlist,
	"qwen":     qwenEnvAllowlist,
	"opencode": opencodeEnvAllowlist,
}

// TestAllowlistEnv_IncludesAllowedExcludesSecrets verifies allowlistEnv pulls
// through allowlisted keys (using a provider-specific key as a sentinel) but
// never leaks a backend-only secret that isn't on the allowlist — the core
// security property of #321.
func TestAllowlistEnv_IncludesAllowedExcludesSecrets(t *testing.T) {
	t.Setenv("LLM_API_KEY", "sentinel-secret")
	t.Setenv("API_TOKEN", "sentinel-token")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-sentinel")
	t.Setenv("HOME", "/home/sentinel")
	t.Setenv("PATH", "/usr/bin:/bin")

	out := allowlistEnv(claudeEnvAllowlist)

	assertContains(t, out, "ANTHROPIC_API_KEY=anthropic-sentinel")
	assertContains(t, out, "HOME=/home/sentinel")
	assertContains(t, out, "PATH=/usr/bin:/bin")
	assertNotContainsKey(t, out, "LLM_API_KEY")
	assertNotContainsKey(t, out, "API_TOKEN")
}

// TestAllowlistEnv_EveryProviderHasPathAndHome is a regression guard for the
// binary-resolution/credential-path subtlety noted in #321: every provider's
// binary() resolves a bare name via PATH, and every CLI reads
// credentials/config from HOME, so both must always be in every provider
// allowlist.
func TestAllowlistEnv_EveryProviderHasPathAndHome(t *testing.T) {
	for name, allow := range allProviderAllowlists {
		t.Run(name, func(t *testing.T) {
			if !allow["PATH"] {
				t.Errorf("%s allowlist missing PATH (required to resolve the CLI binary)", name)
			}
			if !allow["HOME"] {
				t.Errorf("%s allowlist missing HOME (required for CLI credential/config lookup)", name)
			}
		})
	}
}

// TestAllowlistEnv_EveryProviderHasGitHubToken guards the credential-path
// regression fixed alongside this test: agents authenticate their own git
// push / gh calls via the container's `gh auth git-credential` helper, which
// reads GITHUB_TOKEN/GH_TOKEN from the subprocess environment. If these fall
// out of the allowlist, an agent can commit its work but never push it
// (git fails with "could not read Username for 'https://github.com'"), even
// though the backend's own PushBranch keeps working, so the gap is easy to
// miss. Every provider that shells out to git must admit at least one.
func TestAllowlistEnv_EveryProviderHasGitHubToken(t *testing.T) {
	for name, allow := range allProviderAllowlists {
		t.Run(name, func(t *testing.T) {
			if !allow["GITHUB_TOKEN"] {
				t.Errorf("%s allowlist missing GITHUB_TOKEN (required for the agent's own git push auth via gh credential helper)", name)
			}
			if !allow["GH_TOKEN"] {
				t.Errorf("%s allowlist missing GH_TOKEN (gh's own token env var; kept in parity with GITHUB_TOKEN)", name)
			}
		})
	}
}

// TestAllowlistEnv_NoProviderLeaksBackendSecrets verifies that none of the
// backend-only secrets are present in any provider's allowlist output, and
// that none of the allowlists happen to key on a name that would re-admit
// one of those secrets.
func TestAllowlistEnv_NoProviderLeaksBackendSecrets(t *testing.T) {
	secrets := []string{"LLM_API_KEY", "LLM_BASE_URL", "API_TOKEN", "DATABASE_URL", "DB_PASSWORD"}
	for _, s := range secrets {
		t.Setenv(s, "leaked-if-present-"+s)
	}

	for name, allow := range allProviderAllowlists {
		t.Run(name, func(t *testing.T) {
			out := allowlistEnv(allow)
			for _, s := range secrets {
				assertNotContainsKey(t, out, s)
			}
		})
	}
}

// TestComposedSubprocessEnv_ExcludesBackendSecrets covers AC5 directly: the
// full composed subprocess env (allowlist + operator AgentConfig.Env, via
// mergeEnv) for every provider must not include a backend-only sentinel set
// via the test's own environment, while still including the operator-
// supplied extra var.
func TestComposedSubprocessEnv_ExcludesBackendSecrets(t *testing.T) {
	t.Setenv("LLM_API_KEY", "sentinel-secret")
	t.Setenv("API_TOKEN", "sentinel-token")

	extra := map[string]string{"CUSTOM": "x"}

	for name, allow := range allProviderAllowlists {
		t.Run(name, func(t *testing.T) {
			out := mergeEnv(allowlistEnv(allow), extra)
			assertNotContainsKey(t, out, "LLM_API_KEY")
			assertNotContainsKey(t, out, "API_TOKEN")
			assertContains(t, out, "CUSTOM=x")
		})
	}
}

// TestEnvAllowlistFor_MatchesProviderAllowlist verifies the exported
// EnvAllowlistFor lookup (used by the interactive terminal manager — see
// agent.TerminalManager.EnvAllowlist — since that package can't import
// providers directly) returns the same result as calling allowlistEnv with
// that provider's allowlist directly, for every known provider string.
func TestEnvAllowlistFor_MatchesProviderAllowlist(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sentinel-anthropic")
	t.Setenv("OPENAI_API_KEY", "sentinel-openai")
	t.Setenv("LLM_API_KEY", "sentinel-secret")

	providerNames := map[string]map[string]bool{
		"claude":    claudeEnvAllowlist,
		"codex_cli": codexEnvAllowlist,
		"qwen_code": qwenEnvAllowlist,
		"opencode":  opencodeEnvAllowlist,
	}
	for name, allow := range providerNames {
		t.Run(name, func(t *testing.T) {
			got := EnvAllowlistFor(name)
			want := allowlistEnv(allow)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("EnvAllowlistFor(%q) = %v, want %v", name, got, want)
			}
			assertNotContainsKey(t, got, "LLM_API_KEY")
		})
	}
}

// TestEnvAllowlistFor_UnknownProviderFallsBackToCommonBase verifies an
// unrecognized provider string never falls back to the full environment —
// only commonBaseEnvKeys (PATH/HOME/locale/proxy/TLS vars, no provider API
// keys) — so a typo'd or future provider string can't accidentally regress
// to the pre-#321 os.Environ() passthrough.
func TestEnvAllowlistFor_UnknownProviderFallsBackToCommonBase(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sentinel-anthropic")
	t.Setenv("LLM_API_KEY", "sentinel-secret")
	t.Setenv("HOME", "/home/sentinel")

	got := EnvAllowlistFor("some-unknown-provider")
	assertContains(t, got, "HOME=/home/sentinel")
	assertNotContainsKey(t, got, "ANTHROPIC_API_KEY")
	assertNotContainsKey(t, got, "LLM_API_KEY")
}

func assertContains(t *testing.T, env []string, want string) {
	t.Helper()
	for _, kv := range env {
		if kv == want {
			return
		}
	}
	t.Errorf("expected %q in env, got %v", want, env)
}

func assertNotContainsKey(t *testing.T, env []string, key string) {
	t.Helper()
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			t.Errorf("expected key %q to be absent from env, but found %q in %v", key, kv, env)
		}
	}
}

// --- openRawDump / rawDump (cli.go) ---

// TestOpenRawDump_NoopWhenEnvUnset verifies openRawDump returns nil (a
// no-op dump) when AGENT_RAW_LOG_DIR is not set, and that WriteLine/Close on
// a nil *rawDump don't panic — the hot path for every production run, since
// this env var is dev-only.
func TestOpenRawDump_NoopWhenEnvUnset(t *testing.T) {
	t.Setenv("AGENT_RAW_LOG_DIR", "")
	d := openRawDump("run-1")
	if d != nil {
		t.Fatalf("want nil rawDump when AGENT_RAW_LOG_DIR is unset, got %v", d)
	}
	// Must not panic on a nil receiver.
	d.WriteLine("hello")
	d.Close()
}

// TestOpenRawDump_WritesToConfiguredDir verifies that when AGENT_RAW_LOG_DIR
// is set, openRawDump creates <dir>/<runID>.jsonl and WriteLine appends
// newline-terminated lines to it.
func TestOpenRawDump_WritesToConfiguredDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_RAW_LOG_DIR", dir)

	d := openRawDump("run-xyz")
	if d == nil {
		t.Fatal("want non-nil rawDump when AGENT_RAW_LOG_DIR is set")
	}
	d.WriteLine(`{"type":"result"}`)
	d.WriteLine(`{"type":"done"}`)
	d.Close()

	data, err := os.ReadFile(filepath.Join(dir, "run-xyz.jsonl"))
	if err != nil {
		t.Fatalf("expected raw dump file to exist: %v", err)
	}
	want := "{\"type\":\"result\"}\n{\"type\":\"done\"}\n"
	if string(data) != want {
		t.Errorf("raw dump content = %q, want %q", string(data), want)
	}
}
