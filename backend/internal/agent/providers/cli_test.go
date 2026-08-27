package providers

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
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

// --- applyRuntime (cli.go) ---

// TestApplyRuntime_NilSpecIsPassthrough is the §4.1 byte-identical guarantee:
// a repo with no runtime configured (nil RuntimeSpec) must get back exactly
// the binary/args/env it was given, untouched.
func TestApplyRuntime_NilSpecIsPassthrough(t *testing.T) {
	args := []string{"-p", "do the thing"}
	env := []string{"PATH=/usr/bin", "HOME=/home/node"}

	gotBinary, gotArgs, gotEnv, err := applyRuntime(nil, "claude", args, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBinary != "claude" {
		t.Errorf("binary = %q, want %q", gotBinary, "claude")
	}
	if !reflect.DeepEqual(gotArgs, args) {
		t.Errorf("args = %v, want %v", gotArgs, args)
	}
	if !reflect.DeepEqual(gotEnv, env) {
		t.Errorf("env = %v, want %v", gotEnv, env)
	}
}

// TestApplyRuntime_EmptyPinsIsPassthrough covers a non-nil spec with zero
// pins (e.g. ParsePins("") never actually produces this, but a defensive
// caller might construct one) — same byte-identical guarantee as nil.
func TestApplyRuntime_EmptyPinsIsPassthrough(t *testing.T) {
	args := []string{"exec", "--json"}
	env := []string{"PATH=/usr/bin"}
	spec := &agent.RuntimeSpec{Pins: nil, WorktreeDir: "/repo/.ate-worktrees/t1"}

	gotBinary, gotArgs, gotEnv, err := applyRuntime(spec, "codex", args, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBinary != "codex" || !reflect.DeepEqual(gotArgs, args) || !reflect.DeepEqual(gotEnv, env) {
		t.Errorf("got (%q, %v, %v), want passthrough of (%q, %v, %v)", gotBinary, gotArgs, gotEnv, "codex", args, env)
	}
}

// TestApplyRuntime_WrapsWithMiseXInDeterministicOrder verifies non-python
// pins are wrapped as `mise x <id>@<version>... -- <binary> <args...>` in
// the same order as RuntimeSpec.Pins. Uses a native-binary fixture (not
// "claude") so this stays focused on pin/arg ordering without also
// exercising the node-script rewrite (see TestApplyRuntime_NodePin* below for
// that) — go+rust here, no node pin.
func TestApplyRuntime_WrapsWithMiseXInDeterministicOrder(t *testing.T) {
	spec := &agent.RuntimeSpec{
		Pins: []runtime.Pin{
			{ID: "go", Version: "1.21"},
			{ID: "rust", Version: "1.75"},
		},
	}
	env := []string{"PATH=/usr/bin"}

	gotBinary, gotArgs, gotEnv, err := applyRuntime(spec, "codex", []string{"-p", "hi"}, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBinary != "mise" {
		t.Fatalf("binary = %q, want %q", gotBinary, "mise")
	}
	wantArgs := []string{"x", "go@1.21", "rust@1.75", "--", "codex", "-p", "hi"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args = %v, want %v", gotArgs, wantArgs)
	}
	// Original entries survive untouched; MISE_YES/MISE_DATA_DIR get appended
	// (see TestApplyRuntime_InjectsMiseEnv) even with no python pin, since any
	// pinned repo — not just a python one — can ship its own mise.toml.
	if len(gotEnv) < len(env) || !reflect.DeepEqual(gotEnv[:len(env)], env) {
		t.Errorf("env = %v, want it to start with unchanged %v", gotEnv, env)
	}
}

// TestApplyRuntime_InjectsMiseEnv verifies applyRuntime injects
// MISE_YES=1, MISE_DATA_DIR, and MISE_TRUSTED_CONFIG_PATHS into the child
// env whenever pins are present — this is finding 5's fix: allowlistEnv
// strips the backend's own ENV (including any MISE_DATA_DIR override) before
// a provider's child env is built, and there was no allowlist entry for
// MISE_YES/MISE_TRUSTED_CONFIG_PATHS at all, so a repo shipping its own
// mise.toml would hit mise's untrusted-config prompt in this non-TTY child
// and fail.
func TestApplyRuntime_InjectsMiseEnv(t *testing.T) {
	t.Setenv("MISE_DATA_DIR", "/data/mise")

	spec := &agent.RuntimeSpec{
		Pins:        []runtime.Pin{{ID: "go", Version: "1.21"}},
		WorktreeDir: "/repo/.ate-worktrees/t1",
	}
	_, _, gotEnv, err := applyRuntime(spec, "claude", []string{"-p", "hi"}, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]string{
		"MISE_YES":                  "1",
		"MISE_DATA_DIR":             "/data/mise",
		"MISE_TRUSTED_CONFIG_PATHS": "/repo/.ate-worktrees/t1",
	}
	got := map[string]string{}
	for _, kv := range gotEnv {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for k, wantV := range want {
		if got[k] != wantV {
			t.Errorf("env[%s] = %q, want %q (full env: %v)", k, got[k], wantV, gotEnv)
		}
	}
}

// TestApplyRuntime_PythonExcludedFromMiseXArgs verifies a python pin never
// appears in the `mise x` argv (it would shadow the venv's PATH prepend) but
// still triggers the venv PATH prepend + UV_CACHE_DIR in env.
func TestApplyRuntime_PythonExcludedFromMiseXArgs(t *testing.T) {
	spec := &agent.RuntimeSpec{
		Pins: []runtime.Pin{
			{ID: "go", Version: "1.21"},
			{ID: "python", Version: "3.12"},
		},
		WorktreeDir: "/repo/.ate-worktrees/t1",
	}
	env := []string{"PATH=/usr/bin", "HOME=/home/node"}

	gotBinary, gotArgs, gotEnv, err := applyRuntime(spec, "claude", []string{"-p", "hi"}, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBinary != "mise" {
		t.Fatalf("binary = %q, want %q", gotBinary, "mise")
	}
	for _, a := range gotArgs {
		if strings.Contains(a, "python") {
			t.Errorf("python must not appear in mise x argv, got args %v", gotArgs)
		}
	}
	wantArgs := []string{"x", "go@1.21", "--", "claude", "-p", "hi"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args = %v, want %v", gotArgs, wantArgs)
	}

	wantPathPrefix := "PATH=" + filepath.Join("/repo/.ate-worktrees/t1", ".venv", "bin") + string(os.PathListSeparator) + "/usr/bin"
	if gotEnv[0] != wantPathPrefix {
		t.Errorf("PATH entry = %q, want %q", gotEnv[0], wantPathPrefix)
	}
	sawUVCacheDir := false
	for _, kv := range gotEnv {
		if strings.HasPrefix(kv, "UV_CACHE_DIR=") {
			sawUVCacheDir = true
		}
	}
	if !sawUVCacheDir {
		t.Errorf("expected UV_CACHE_DIR in env when python is pinned, got %v", gotEnv)
	}
}

// TestApplyRuntime_PythonOnlyStillWrapsWithMiseX verifies a python-only pin
// list still routes the spawn through `mise x` (with no non-python pins in
// its argv) rather than falling back to a plain spawn — mise x with no
// installs is a documented no-op passthrough, and this keeps the venv PATH
// prepend wired through the same code path as the mixed-pin case.
func TestApplyRuntime_PythonOnlyStillWrapsWithMiseX(t *testing.T) {
	spec := &agent.RuntimeSpec{
		Pins:        []runtime.Pin{{ID: "python", Version: "3.12"}},
		WorktreeDir: "/repo/.ate-worktrees/t1",
	}

	gotBinary, gotArgs, _, err := applyRuntime(spec, "claude", []string{"-p", "hi"}, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBinary != "mise" {
		t.Fatalf("binary = %q, want %q", gotBinary, "mise")
	}
	wantArgs := []string{"x", "--", "claude", "-p", "hi"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args = %v, want %v", gotArgs, wantArgs)
	}
}

// --- node pin: explicit-interpreter fix (isNodeScript / explicitInterpreterForNodeScript / applyRuntime) ---

// writeFixtureScript writes a node-script-shebang fixture (mimicking npm's
// global-install wrapper for claude/qwen) to dir/name, executable.
func writeFixtureNodeScript(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/usr/bin/env node\nrequire('./cli.js')\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeFixtureNativeBinary writes a fixture with no shebang at all (a stand-in
// for codex's Rust / opencode's Go native binaries — arbitrary non-text bytes
// as the first line, executable).
func writeFixtureNativeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeFixtureDirectNodeShebang writes a fixture with a direct (non-env)
// shebang path ending in /node, e.g. "#!/usr/local/bin/node".
func writeFixtureDirectNodeShebang(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/usr/local/bin/node\nconsole.log('hi')\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIsNodeScript_EnvNodeShebang(t *testing.T) {
	dir := t.TempDir()
	path := writeFixtureNodeScript(t, dir, "claude")
	got, err := isNodeScript(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected #!/usr/bin/env node to be detected as a node script")
	}
}

func TestIsNodeScript_DirectNodePathShebang(t *testing.T) {
	dir := t.TempDir()
	path := writeFixtureDirectNodeShebang(t, dir, "claude")
	got, err := isNodeScript(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected a shebang path ending in /node to be detected as a node script")
	}
}

// TestIsNodeScript_NativeBinaryIsNotDetected is the "checked, not assumed"
// regression guard: a native binary (codex/opencode's actual shape — no text
// shebang) must never be misdetected as a node script.
func TestIsNodeScript_NativeBinaryIsNotDetected(t *testing.T) {
	dir := t.TempDir()
	path := writeFixtureNativeBinary(t, dir, "codex")
	got, err := isNodeScript(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected a native binary (no shebang) to not be detected as a node script")
	}
}

func TestIsNodeScript_OtherShebangIsNotNode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := isNodeScript(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected a #!/bin/sh shebang to not be detected as a node script")
	}
}

// TestApplyRuntime_NodePin_RewritesNodeScriptToExplicitInterpreter is the
// core regression guard for the explicit-interpreter fix: a node pin whose
// CLI resolves to a node script must spawn as
// `mise x node@<pin> -- <systemNode> <absCLIPath> <args...>`, not
// `mise x node@<pin> -- <cli> <args...>` (which would run the CLI itself on
// the pinned node and crash it).
func TestApplyRuntime_NodePin_RewritesNodeScriptToExplicitInterpreter(t *testing.T) {
	dir := t.TempDir()
	cliPath := writeFixtureNodeScript(t, dir, "claude")
	sysNode := writeFixtureNativeBinary(t, dir, "node") // stand-in system node; just needs to be resolvable+executable

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	spec := &agent.RuntimeSpec{Pins: []runtime.Pin{{ID: "node", Version: "22"}}}
	gotBinary, gotArgs, _, err := applyRuntime(spec, cliPath, []string{"-p", "hi"}, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBinary != "mise" {
		t.Fatalf("binary = %q, want %q", gotBinary, "mise")
	}
	wantArgs := []string{"x", "node@22", "--", sysNode, cliPath, "-p", "hi"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args = %v, want %v", gotArgs, wantArgs)
	}
}

// TestApplyRuntime_NodePin_NativeBinaryUnwrapped verifies a node pin does NOT
// rewrite the spawn for a native-binary provider (codex, opencode) — those
// aren't node scripts, so mise x's PATH prepend never touches how the CLI
// process itself resolves its interpreter, and the previous plain-binary
// spawn is preserved.
func TestApplyRuntime_NodePin_NativeBinaryUnwrapped(t *testing.T) {
	dir := t.TempDir()
	cliPath := writeFixtureNativeBinary(t, dir, "codex")

	spec := &agent.RuntimeSpec{Pins: []runtime.Pin{{ID: "node", Version: "22"}}}
	gotBinary, gotArgs, _, err := applyRuntime(spec, cliPath, []string{"exec", "--json"}, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBinary != "mise" {
		t.Fatalf("binary = %q, want %q", gotBinary, "mise")
	}
	wantArgs := []string{"x", "node@22", "--", cliPath, "exec", "--json"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args = %v, want %v", gotArgs, wantArgs)
	}
}

// TestApplyRuntime_NoNodePin_NeverInspectsBinary verifies the passthrough/
// non-node-pin paths never call exec.LookPath on binary at all — a
// non-existent bare binary name must not error just because there's no node
// pin driving the shim check.
func TestApplyRuntime_NoNodePin_NeverInspectsBinary(t *testing.T) {
	spec := &agent.RuntimeSpec{Pins: []runtime.Pin{{ID: "go", Version: "1.21"}}}
	gotBinary, gotArgs, _, err := applyRuntime(spec, "definitely-not-a-real-binary-xyz", []string{"-p", "hi"}, []string{"PATH=/usr/bin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBinary != "mise" {
		t.Fatalf("binary = %q, want %q", gotBinary, "mise")
	}
	wantArgs := []string{"x", "go@1.21", "--", "definitely-not-a-real-binary-xyz", "-p", "hi"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args = %v, want %v", gotArgs, wantArgs)
	}
}

// TestApplyRuntime_NodePin_FailsClosedWhenCLIUnresolvable verifies a node
// pin with a CLI binary that can't be resolved at all (exec.LookPath fails)
// returns an error rather than falling back to an unwrapped/guessed spawn —
// never launch on the wrong interpreter, per the fail-closed requirement.
func TestApplyRuntime_NodePin_FailsClosedWhenCLIUnresolvable(t *testing.T) {
	spec := &agent.RuntimeSpec{Pins: []runtime.Pin{{ID: "node", Version: "22"}}}
	_, _, _, err := applyRuntime(spec, "definitely-not-a-real-binary-xyz", []string{"-p", "hi"}, []string{"PATH=/usr/bin"})
	if err == nil {
		t.Fatal("expected an error when the CLI binary can't be resolved for a node-pinned run")
	}
}

// TestApplyRuntime_NodePin_FailsClosedWhenSystemNodeUnresolvable verifies a
// node pin whose CLI IS a node script, but the system `node` itself can't be
// resolved (e.g. a broken image), fails closed rather than spawning the CLI
// directly (which would silently skip the whole point of pinning node).
func TestApplyRuntime_NodePin_FailsClosedWhenSystemNodeUnresolvable(t *testing.T) {
	dir := t.TempDir()
	cliPath := writeFixtureNodeScript(t, dir, "claude")

	// PATH has no "node" binary at all — only the fixture dir's "claude" and
	// whatever coreutils are needed to exec.LookPath cliPath itself (an
	// absolute path, so PATH isn't consulted to find cliPath — only to find
	// "node").
	t.Setenv("PATH", dir)

	spec := &agent.RuntimeSpec{Pins: []runtime.Pin{{ID: "node", Version: "22"}}}
	_, _, _, err := applyRuntime(spec, cliPath, []string{"-p", "hi"}, []string{"PATH=/usr/bin"})
	if err == nil {
		t.Fatal("expected an error when system node can't be resolved for a node-script CLI")
	}
}
