package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	name, args, env, venvBin := applyChatRuntime(nil, "repo-1", "claude", []string{"--model", "sonnet"})
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
func TestApplyChatRuntime_WrapsWithMiseX(t *testing.T) {
	pins := []runtime.Pin{{ID: "go", Version: "1.21"}}
	name, args, env, venvBin := applyChatRuntime(pins, "repo-1", "claude", []string{"--model", "sonnet"})

	if name != "mise" {
		t.Fatalf("name = %q, want mise", name)
	}
	wantArgs := []string{"x", "go@1.21", "--", "claude", "--model", "sonnet"}
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

// TestApplyChatRuntime_NodeExcludedFromMiseXArgs verifies a node pin never
// appears in the mise x argv for a chat session — finding 9's documented
// interaction with the still-pending "does a node pin also run the provider
// CLI itself under pinned node" design decision. Until that's resolved, node
// is dropped here the same way python is dropped for its own reason.
func TestApplyChatRuntime_NodeExcludedFromMiseXArgs(t *testing.T) {
	pins := []runtime.Pin{{ID: "go", Version: "1.21"}, {ID: "node", Version: "22"}}
	_, args, _, _ := applyChatRuntime(pins, "repo-1", "claude", nil)

	for _, a := range args {
		if strings.Contains(a, "node") {
			t.Errorf("node must not appear in chat mise x argv, got args %v", args)
		}
	}
	wantArgs := []string{"x", "go@1.21", "--", "claude"}
	if strings.Join(args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

// TestApplyChatRuntime_PythonOnlyStillWrapsWithMiseXAndReturnsVenvBin
// verifies a python-only pin list still routes through mise x (consistent
// with providers.applyRuntime's TestApplyRuntime_PythonOnlyStillWrapsWithMiseX)
// and returns the venv bin dir for the caller to prepend to PATH.
func TestApplyChatRuntime_PythonOnlyStillWrapsWithMiseXAndReturnsVenvBin(t *testing.T) {
	pins := []runtime.Pin{{ID: "python", Version: "3.12"}}
	name, args, env, venvBin := applyChatRuntime(pins, "repo-1", "claude", []string{"--model", "sonnet"})

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
