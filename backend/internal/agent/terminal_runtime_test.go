package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
)

// --- applyChatRuntime (pure) ---

// TestApplyChatRuntime_NoPinsIsPassthrough is the byte-identical guard for
// chat sessions on an unconfigured repo: no pins means the launch command and
// env are returned completely unchanged, with no venv dir.
func TestApplyChatRuntime_NoPinsIsPassthrough(t *testing.T) {
	name, args, env, venvBin, err := applyChatRuntime(nil, "repo-1", "claude", []string{"--model", "sonnet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "claude" {
		t.Errorf("name = %q, want unchanged %q", name, "claude")
	}
	if len(args) != 2 || args[0] != "--model" || args[1] != "sonnet" {
		t.Errorf("args = %v, want unchanged", args)
	}
	if env != nil {
		t.Errorf("env = %v, want nil", env)
	}
	if venvBin != "" {
		t.Errorf("venvBin = %q, want empty", venvBin)
	}
}

// TestApplyChatRuntime_WrapsWithMiseX verifies a non-python, non-node pin
// wraps the chat launch command with `mise x` and injects the mise-related
// env vars (mirroring providers.applyRuntime's shape for headless runs).
// Uses a native-binary fixture (not "claude") so this stays focused on
// mise-wrapping, without also exercising the node-script rewrite (see the
// node-pin tests below for that).
func TestApplyChatRuntime_WrapsWithMiseX(t *testing.T) {
	dir := t.TempDir()
	cliPath := writeFixtureNativeChatBinary(t, dir, "codex")

	pins := []runtime.Pin{{ID: "go", Version: "1.21"}}
	name, args, env, venvBin, err := applyChatRuntime(pins, "repo-1", cliPath, []string{"--model", "sonnet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name != "mise" {
		t.Fatalf("name = %q, want mise", name)
	}
	wantArgs := []string{"x", "go@1.21", "--", cliPath, "--model", "sonnet"}
	if strings.Join(args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
	if venvBin != "" {
		t.Errorf("venvBin = %q, want empty (no python pin)", venvBin)
	}

	got := map[string]bool{}
	for _, kv := range env {
		got[kv] = true
	}
	if !got["MISE_YES=1"] {
		t.Errorf("expected MISE_YES=1 in env, got %v", env)
	}
	sawDataDir, sawTrusted := false, false
	for _, kv := range env {
		if strings.HasPrefix(kv, "MISE_DATA_DIR=") {
			sawDataDir = true
		}
		if strings.HasPrefix(kv, "MISE_TRUSTED_CONFIG_PATHS=") {
			sawTrusted = true
		}
	}
	if !sawDataDir {
		t.Errorf("expected MISE_DATA_DIR in env, got %v", env)
	}
	if !sawTrusted {
		t.Errorf("expected MISE_TRUSTED_CONFIG_PATHS in env, got %v", env)
	}
}

// TestApplyChatRuntime_PythonOnlyStillWrapsWithMiseXAndReturnsVenvBin
// verifies a python-only pin list still routes through mise x (consistent
// with providers.applyRuntime's TestApplyRuntime_PythonOnlyStillWrapsWithMiseX)
// and returns the venv bin dir for the caller to prepend to PATH.
func TestApplyChatRuntime_PythonOnlyStillWrapsWithMiseXAndReturnsVenvBin(t *testing.T) {
	pins := []runtime.Pin{{ID: "python", Version: "3.12"}}
	name, args, env, venvBin, err := applyChatRuntime(pins, "repo-1", "claude", []string{"--model", "sonnet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name != "mise" {
		t.Fatalf("name = %q, want mise", name)
	}
	wantArgs := []string{"x", "--", "claude", "--model", "sonnet"}
	if strings.Join(args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
	if venvBin == "" {
		t.Fatal("expected a non-empty venv bin dir for a python pin")
	}
	if !strings.HasSuffix(venvBin, "/.venv/bin") {
		t.Errorf("venvBin = %q, want it to end with /.venv/bin", venvBin)
	}
	if !strings.Contains(venvBin, "repo-1") {
		t.Errorf("venvBin = %q, want it keyed by repo id %q", venvBin, "repo-1")
	}
	sawUVCache := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "UV_CACHE_DIR=") {
			sawUVCache = true
		}
	}
	if !sawUVCache {
		t.Errorf("expected UV_CACHE_DIR in env for a python pin, got %v", env)
	}
}

// --- applyChatRuntime: node pin explicit-interpreter fix ---

func writeFixtureNodeChatScript(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/usr/bin/env node\nrequire('./cli.js')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFixtureNativeChatBinary(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestIsChatNodeScript_Detection mirrors providers.TestIsNodeScript_* —
// duplicated logic, same behavior: a node script is detected by shebang,
// never assumed; a native binary is never misdetected.
func TestIsChatNodeScript_Detection(t *testing.T) {
	dir := t.TempDir()

	script := writeFixtureNodeChatScript(t, dir, "claude")
	got, err := isChatNodeScript(script)
	if err != nil || !got {
		t.Errorf("isChatNodeScript(%q) = (%v, %v), want (true, nil)", script, got, err)
	}

	native := writeFixtureNativeChatBinary(t, dir, "codex")
	got, err = isChatNodeScript(native)
	if err != nil || got {
		t.Errorf("isChatNodeScript(%q) = (%v, %v), want (false, nil)", native, got, err)
	}
}

// TestApplyChatRuntime_NodePin_RewritesNodeScriptToExplicitInterpreter is
// finding 9's follow-up fix: a node pin whose chat CLI resolves to a node
// script must spawn as `mise x node@<pin> -- <systemNode> <absCLIPath>
// <args...>`, not `mise x node@<pin> -- <cli> <args...>` (which would run
// the CLI itself, and crash it, on the pinned node).
func TestApplyChatRuntime_NodePin_RewritesNodeScriptToExplicitInterpreter(t *testing.T) {
	dir := t.TempDir()
	cliPath := writeFixtureNodeChatScript(t, dir, "claude")
	sysNode := writeFixtureNativeChatBinary(t, dir, "node") // stand-in system node

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pins := []runtime.Pin{{ID: "node", Version: "22"}}
	name, args, _, _, err := applyChatRuntime(pins, "repo-1", cliPath, []string{"--model", "sonnet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name != "mise" {
		t.Fatalf("name = %q, want mise", name)
	}
	wantArgs := []string{"x", "node@22", "--", sysNode, cliPath, "--model", "sonnet"}
	if strings.Join(args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

// TestApplyChatRuntime_NodePin_NativeBinaryUnwrapped verifies a node pin
// does NOT rewrite the spawn for a native-binary chat CLI (codex, opencode).
func TestApplyChatRuntime_NodePin_NativeBinaryUnwrapped(t *testing.T) {
	dir := t.TempDir()
	cliPath := writeFixtureNativeChatBinary(t, dir, "codex")

	pins := []runtime.Pin{{ID: "node", Version: "22"}}
	name, args, _, _, err := applyChatRuntime(pins, "repo-1", cliPath, []string{"exec"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name != "mise" {
		t.Fatalf("name = %q, want mise", name)
	}
	wantArgs := []string{"x", "node@22", "--", cliPath, "exec"}
	if strings.Join(args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

// TestApplyChatRuntime_NodePin_FailsClosedWhenCLIUnresolvable verifies a
// node pin with an unresolvable CLI name returns an error rather than
// falling back to an unwrapped/guessed spawn.
func TestApplyChatRuntime_NodePin_FailsClosedWhenCLIUnresolvable(t *testing.T) {
	pins := []runtime.Pin{{ID: "node", Version: "22"}}
	_, _, _, _, err := applyChatRuntime(pins, "repo-1", "definitely-not-a-real-binary-xyz", []string{"-p"})
	if err == nil {
		t.Fatal("expected an error when the CLI binary can't be resolved for a node-pinned chat session")
	}
}

// TestApplyChatRuntime_NodePin_FailsClosedWhenSystemNodeUnresolvable
// verifies a node pin whose CLI IS a node script, but the system `node`
// itself can't be resolved, fails closed.
func TestApplyChatRuntime_NodePin_FailsClosedWhenSystemNodeUnresolvable(t *testing.T) {
	dir := t.TempDir()
	cliPath := writeFixtureNodeChatScript(t, dir, "claude")

	t.Setenv("PATH", dir) // no "node" binary on PATH

	pins := []runtime.Pin{{ID: "node", Version: "22"}}
	_, _, _, _, err := applyChatRuntime(pins, "repo-1", cliPath, []string{"-p"})
	if err == nil {
		t.Fatal("expected an error when system node can't be resolved for a node-script chat CLI")
	}
}

// TestChatVenvDir_OutsideAnyRepoCheckout verifies the chat venv's base dir is
// not the session's worktree path — the whole point of finding 9's design
// (never <repoPath>/.venv, which would live inside the user's live checkout).
func TestChatVenvDir_OutsideAnyRepoCheckout(t *testing.T) {
	dir := chatVenvDir("repo-42")
	if strings.Contains(dir, ".ate-worktrees") {
		t.Errorf("chatVenvDir(%q) = %q, must never be inside a repo's worktree dir", "repo-42", dir)
	}
	if !strings.Contains(dir, "repo-42") {
		t.Errorf("chatVenvDir(%q) = %q, want it keyed by repo id", "repo-42", dir)
	}
}

// TestChatVenvDir_DistinctPerRepo verifies two different repos never share a
// venv dir (each repo's chat sessions get their own).
func TestChatVenvDir_DistinctPerRepo(t *testing.T) {
	a := chatVenvDir("repo-a")
	b := chatVenvDir("repo-b")
	if a == b {
		t.Errorf("chatVenvDir should differ per repo id, both got %q", a)
	}
}

// --- prepareChatRuntime (no-op cases; no I/O) ---

// TestPrepareChatRuntime_NoPinsIsNoOp verifies an unconfigured repo (nil
// pins) never shells out to mise/uv — same byte-identical guarantee as
// applyChatRuntime, at the prep layer.
func TestPrepareChatRuntime_NoPinsIsNoOp(t *testing.T) {
	if err := prepareChatRuntime(context.Background(), "repo-1", nil); err != nil {
		t.Fatalf("unexpected error for nil pins: %v", err)
	}
}

// TestPrepareChatRuntime_EmptyRepoIDIsNoOp verifies a missing repo id (a
// defensive case; the handler always passes one) also short-circuits before
// any mise/uv call, even with pins present.
func TestPrepareChatRuntime_EmptyRepoIDIsNoOp(t *testing.T) {
	pins := []runtime.Pin{{ID: "go", Version: "1.21"}}
	if err := prepareChatRuntime(context.Background(), "", pins); err != nil {
		t.Fatalf("unexpected error for empty repoID: %v", err)
	}
}

// --- Attach integration: no pins is byte-identical to before this feature ---

// TestTerminalManager_Attach_NoPinsNeverWraps is the end-to-end regression
// guard: with pins=nil (the common case — a repo with no runtime_languages
// configured), Attach must launch the plain command untouched, exactly as
// before finding 9's fix. Uses the same real-`sh`-over-PTY pattern as
// TestTerminalManager_ChatMCPInjectsEnv so this observes the actual spawned
// process, not just applyChatRuntime's return values.
func TestTerminalManager_Attach_NoPinsNeverWraps(t *testing.T) {
	m := NewTerminalManager()
	sessionID := "no-pins-sess"
	defer m.Stop(sessionID)

	orig := buildTerminalCommand
	buildTerminalCommand = func(_, _ string, _ bool) (string, []string, error) { return "sh", nil, nil }
	t.Cleanup(func() { buildTerminalCommand = orig })

	repoDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		_ = m.Attach(r.Context(), sessionID, repoDir, "claude", "", false, "repo-1", nil, conn)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// echo $0 reports the shell's own argv[0] — "sh" if launched directly,
	// "mise" if (incorrectly) wrapped.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo ARGV0=$0\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := readUntil(t, ctx, conn, "ARGV0=")
	if strings.Contains(out, "mise") {
		t.Errorf("expected an unwrapped sh with nil pins, got mise involved:\n%s", out)
	}
}
