package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// terminalTestHandler upgrades to a WebSocket and hands it to the manager,
// running an interactive `sh` (instead of a real provider CLI, which needs auth)
// in repoDir so the test can assert on cwd, streaming, and scrollback replay.
func terminalTestHandler(t *testing.T, m *TerminalManager, sessionID, repoDir string) http.HandlerFunc {
	// Swap the command builder to launch an interactive shell with no prompt/rc
	// noise. Restored after the test via t.Cleanup.
	orig := buildTerminalCommand
	buildTerminalCommand = func(_, _ string, _ bool) (string, []string, error) {
		return "sh", nil, nil
	}
	t.Cleanup(func() { buildTerminalCommand = orig })

	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		_ = m.Attach(r.Context(), sessionID, repoDir, "claude", "", false, conn)
	}
}

// TestTerminalCommand pins the per-provider interactive resume syntax — the
// bits that differ from a naive "--resume <id> everywhere" (codex subcommand,
// opencode --session), verified against each CLI's help/docs.
func TestTerminalCommand(t *testing.T) {
	cases := []struct {
		provider string
		resume   bool
		wantName string
		wantArgs []string
		wantErr  bool
	}{
		{"claude", false, "claude", nil, false},
		{"claude", true, "claude", []string{"--continue"}, false},
		{"codex_cli", true, "codex", []string{"resume", "--last"}, false}, // subcommand
		{"qwen_code", true, "qwen", []string{"--continue"}, false},
		{"opencode", true, "opencode", []string{"--continue"}, false},
		{"anthropic", false, "", nil, true}, // API provider: no terminal
		{"bogus", false, "", nil, true},
	}
	for _, c := range cases {
		name, args, err := terminalCommand(c.provider, "", c.resume)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err=%v wantErr=%v", c.provider, err, c.wantErr)
		}
		if c.wantErr {
			continue
		}
		if name != c.wantName {
			t.Errorf("%s: name=%q want %q", c.provider, name, c.wantName)
		}
		if strings.Join(args, " ") != strings.Join(c.wantArgs, " ") {
			t.Errorf("%s: args=%v want %v", c.provider, args, c.wantArgs)
		}
	}
}

// TestTerminalManagerAttachStreams drives a real PTY over a real WebSocket: it
// spawns a process (a shell, standing in for a provider CLI) in a specific cwd,
// then checks that (1) it runs in that cwd, (2) stdin sent over the WS reaches
// the process, (3) the process's output streams back as binary frames, and
// (4) reattaching replays scrollback. This exercises the runtime path unit
// tests of the pure functions can't.
func TestTerminalManagerAttachStreams(t *testing.T) {
	m := NewTerminalManager()
	sessionID := "test-sess"
	defer m.Stop(sessionID)

	// A temp dir stands in for the repo; the shell echoes its cwd so we can
	// assert the PTY actually ran there (the core "runs in the selected repo" req).
	repoDir := t.TempDir()

	srv := httptest.NewServer(terminalTestHandler(t, m, sessionID, repoDir))
	defer srv.Close()

	// --- First attach: send a command, read output. ---
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn1, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send a command that prints "CWD=<pwd>". The PTY echoes the raw stdin
	// (containing the literal "$(pwd)"), so we key on the *expanded* form
	// "CWD=<repoDir>" which can only come from the shell actually running it.
	want := "CWD=" + repoDir
	if err := conn1.Write(ctx, websocket.MessageBinary, []byte("echo CWD=$(pwd)\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := readUntil(t, ctx, conn1, want)
	if !strings.Contains(got, want) {
		t.Errorf("stdin didn't reach the PTY, or it didn't run in cwd %q; got:\n%s", repoDir, got)
	}

	// TERM must reach the PTY as a color-capable value, else CLIs disable color
	// (the backend container has no TERM, so it must be set explicitly).
	if err := conn1.Write(ctx, websocket.MessageBinary, []byte("echo TERM=$TERM\n")); err != nil {
		t.Fatalf("write TERM probe: %v", err)
	}
	termOut := readUntil(t, ctx, conn1, "TERM=xterm-256color")
	if !strings.Contains(termOut, "TERM=xterm-256color") {
		t.Errorf("PTY TERM not color-capable; got:\n%s", termOut)
	}

	// Resize must arrive as a TEXT frame (parseResize only inspects text
	// frames); a binary frame would fall through to the PTY as literal stdin.
	// `stty size` prints "<rows> <cols>", so after resizing to 33x77 the shell
	// should report exactly that — proving the frame resized the PTY rather
	// than being typed into it.
	if err := conn1.Write(ctx, websocket.MessageText, []byte("\x00resize:77,33")); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	if err := conn1.Write(ctx, websocket.MessageBinary, []byte("stty size\n")); err != nil {
		t.Fatalf("write stty: %v", err)
	}
	size := readUntil(t, ctx, conn1, "33 77")
	if !strings.Contains(size, "33 77") {
		t.Errorf("resize text frame didn't apply (want stty size '33 77'); got:\n%s", size)
	}
	_ = conn1.Close(websocket.StatusNormalClosure, "")

	// --- Reattach: scrollback should replay the earlier output. ---
	conn2, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("redial: %v", err)
	}
	defer func() { _ = conn2.Close(websocket.StatusNormalClosure, "") }()
	replay := readUntil(t, ctx, conn2, want)
	if !strings.Contains(replay, want) {
		t.Errorf("reattach did not replay scrollback; got:\n%s", replay)
	}
}

// TestTerminalManager_ChatMCPInjectsEnv verifies that a configured ChatMCP
// provisioner's environment reaches the launched CLI process, and that it is
// called with the session's provider and id. The env probe (echoing a var only
// the provisioner sets) can only succeed if injection actually happened.
func TestTerminalManager_ChatMCPInjectsEnv(t *testing.T) {
	m := NewTerminalManager()
	sessionID := "mcp-sess"
	defer m.Stop(sessionID)

	var gotProvider, gotSession string
	cleanupCalled := make(chan struct{}, 1)
	m.ChatMCP = func(provider, sid string) ([]string, []string, func(), error) {
		gotProvider, gotSession = provider, sid
		return nil, []string{"ATE_BOARD_TEST=zzz42"}, func() { cleanupCalled <- struct{}{} }, nil
	}

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
		_ = m.Attach(r.Context(), sessionID, repoDir, "claude", "", false, conn)
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

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo BOARD=$ATE_BOARD_TEST\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := readUntil(t, ctx, conn, "BOARD=zzz42")
	if !strings.Contains(out, "BOARD=zzz42") {
		t.Errorf("ChatMCP env did not reach the PTY; got:\n%s", out)
	}
	if gotProvider != "claude" || gotSession != sessionID {
		t.Errorf("provisioner called with provider=%q session=%q; want claude/%s", gotProvider, gotSession, sessionID)
	}

	// Stopping the session must run the provisioner's cleanup.
	m.Stop(sessionID)
	select {
	case <-cleanupCalled:
	case <-time.After(5 * time.Second):
		t.Error("cleanup was not called after Stop")
	}
}

// TestTerminalManager_EnvAllowlistScopesSubprocessEnv verifies that when
// EnvAllowlist is configured, the launched CLI's env is exactly what that
// func returns (plus TERM/COLORTERM/extraEnv) — a backend-only secret
// present in the test's own environment (standing in for the backend
// process's os.Environ()) must not reach the PTY, since EnvAllowlist is
// supposed to replace os.Environ() as the base, not supplement it. This is
// the interactive-terminal counterpart to the #321 fix already covering the
// headless CLI runners (see providers/cli_test.go).
func TestTerminalManager_EnvAllowlistScopesSubprocessEnv(t *testing.T) {
	t.Setenv("ATE_TERMINAL_TEST_SECRET", "leaked-if-present")

	m := NewTerminalManager()
	sessionID := "allowlist-sess"
	defer m.Stop(sessionID)

	var gotProvider string
	m.EnvAllowlist = func(provider string) []string {
		gotProvider = provider
		return []string{"ATE_TERMINAL_TEST_ALLOWED=yes"}
	}

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
		_ = m.Attach(r.Context(), sessionID, repoDir, "claude", "", false, conn)
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

	// The allowlisted var must reach the PTY.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo ALLOWED=$ATE_TERMINAL_TEST_ALLOWED\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := readUntil(t, ctx, conn, "ALLOWED=yes")
	if !strings.Contains(out, "ALLOWED=yes") {
		t.Errorf("EnvAllowlist's env did not reach the PTY; got:\n%s", out)
	}

	// The backend-only secret must NOT reach the PTY.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo SECRET=[$ATE_TERMINAL_TEST_SECRET]\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	out = readUntil(t, ctx, conn, "SECRET=[]")
	if strings.Contains(out, "leaked-if-present") {
		t.Errorf("backend-only secret leaked into terminal subprocess env; got:\n%s", out)
	}

	if gotProvider != "claude" {
		t.Errorf("EnvAllowlist called with provider=%q, want claude", gotProvider)
	}
}

// TestTerminalManager_MaxSessionsCapsNewSessionsOnly verifies that once
// MaxSessions live processes are running, starting a *new* session is
// refused with ErrTooManySessions, while reattaching to an *existing*
// session (even one at/over the cap boundary) is never refused — a full
// manager must not lock out a client reconnecting to its own session.
func TestTerminalManager_MaxSessionsCapsNewSessionsOnly(t *testing.T) {
	m := NewTerminalManager()
	m.MaxSessions = 1
	orig := buildTerminalCommand
	buildTerminalCommand = func(_, _ string, _ bool) (string, []string, error) { return "sh", nil, nil }
	t.Cleanup(func() { buildTerminalCommand = orig })

	repoDir := t.TempDir()

	// First session starts fine (0 < cap 1).
	s1, err := m.ensure("sess-1", repoDir, "claude", "", false)
	if err != nil {
		t.Fatalf("first session should start under the cap: %v", err)
	}
	defer m.Stop("sess-1")
	if s1 == nil {
		t.Fatal("expected a non-nil session")
	}

	// A second, *new* session is refused: starting it would exceed the cap.
	if _, err := m.ensure("sess-2", repoDir, "claude", "", false); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("expected ErrTooManySessions for a new session over the cap, got %v", err)
	}

	// Reattaching to the *existing* session (sess-1) must still succeed even
	// though the manager is at capacity.
	if _, err := m.ensure("sess-1", repoDir, "claude", "", false); err != nil {
		t.Fatalf("reattach to an existing session must never be refused by the cap: %v", err)
	}
}

// TestTerminalManager_ReapIdleOnceStopsDetachedSessionsPastTimeout verifies
// that reapIdleOnce stops a session whose lastDetachedAt is older than
// IdleTimeout, releases it from the session map, and leaves a
// still-attached (or recently-detached) session alone.
func TestTerminalManager_ReapIdleOnceStopsDetachedSessionsPastTimeout(t *testing.T) {
	m := NewTerminalManager()
	m.IdleTimeout = 50 * time.Millisecond
	orig := buildTerminalCommand
	buildTerminalCommand = func(_, _ string, _ bool) (string, []string, error) { return "sh", nil, nil }
	t.Cleanup(func() { buildTerminalCommand = orig })

	repoDir := t.TempDir()

	idle, err := m.ensure("idle-sess", repoDir, "claude", "", false)
	if err != nil {
		t.Fatalf("ensure idle-sess: %v", err)
	}
	fresh, err := m.ensure("fresh-sess", repoDir, "claude", "", false)
	if err != nil {
		t.Fatalf("ensure fresh-sess: %v", err)
	}
	defer m.Stop("fresh-sess") // no-op if already reaped by the (unexpected) failure path

	// idle-sess has been detached "a while" (past the timeout); fresh-sess
	// only just detached (well within it).
	idle.mu.Lock()
	idle.lastDetachedAt = time.Now().Add(-time.Hour)
	idle.mu.Unlock()
	fresh.mu.Lock()
	fresh.lastDetachedAt = time.Now()
	fresh.mu.Unlock()

	m.reapIdleOnce()

	m.mu.Lock()
	_, idleStillPresent := m.sessions["idle-sess"]
	_, freshStillPresent := m.sessions["fresh-sess"]
	m.mu.Unlock()

	if idleStillPresent {
		t.Error("expected idle-sess to be reaped (stopped and removed)")
	}
	if !freshStillPresent {
		t.Error("expected fresh-sess (detached well within IdleTimeout) to survive")
	}
}

// TestTerminalManager_ReapIdleOnceDisabledByDefault verifies IdleTimeout<=0
// (the zero value) never reaps anything, preserving pre-existing behavior
// for callers that don't opt in.
func TestTerminalManager_ReapIdleOnceDisabledByDefault(t *testing.T) {
	m := NewTerminalManager() // IdleTimeout left at its zero value
	orig := buildTerminalCommand
	buildTerminalCommand = func(_, _ string, _ bool) (string, []string, error) { return "sh", nil, nil }
	t.Cleanup(func() { buildTerminalCommand = orig })

	repoDir := t.TempDir()
	s, err := m.ensure("never-idle", repoDir, "claude", "", false)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	defer m.Stop("never-idle")

	s.mu.Lock()
	s.lastDetachedAt = time.Now().Add(-24 * time.Hour)
	s.mu.Unlock()

	m.reapIdleOnce()

	m.mu.Lock()
	_, present := m.sessions["never-idle"]
	m.mu.Unlock()
	if !present {
		t.Error("expected reapIdleOnce to be a no-op when IdleTimeout is disabled (0)")
	}
}

// TestTerminalManager_OutputPumpDeleteIsOwnerScoped verifies the output
// pump's session-map cleanup at the end of ensure() only deletes its own
// *ptySession* entry: if a session is Stop()'d and a fresh session is
// inserted under the same id before the old pump's goroutine observes the
// process exit, the old pump must not delete the new session out from under
// it (same ownership-bug class as #244's ClearActiveAgentRunIfOwner).
func TestTerminalManager_OutputPumpDeleteIsOwnerScoped(t *testing.T) {
	m := NewTerminalManager()
	orig := buildTerminalCommand
	buildTerminalCommand = func(_, _ string, _ bool) (string, []string, error) { return "sh", nil, nil }
	t.Cleanup(func() { buildTerminalCommand = orig })

	repoDir := t.TempDir()
	const sessionID = "owner-scoped-sess"

	oldSession, err := m.ensure(sessionID, repoDir, "claude", "", false)
	if err != nil {
		t.Fatalf("ensure (old): %v", err)
	}

	// Stop kills the process and removes it from the map immediately.
	m.Stop(sessionID)

	// Wait for the old session's output pump to observe the process exit
	// (its done channel closes) before reattaching, so the race is against
	// the pump's *cleanup* (delete(m.sessions, ...)) rather than its exit
	// detection.
	select {
	case <-oldSession.done:
	case <-time.After(5 * time.Second):
		t.Fatal("old session's process did not exit in time")
	}
	// Small grace period: cmd.Wait()/cleanup()/delete run just after done is
	// closed, so give the goroutine a moment to reach (or nearly reach) the
	// delete before we insert the new session under the same id.
	time.Sleep(20 * time.Millisecond)

	newSession, err := m.ensure(sessionID, repoDir, "claude", "", false)
	if err != nil {
		t.Fatalf("ensure (new): %v", err)
	}
	defer m.Stop(sessionID)
	if newSession == oldSession {
		t.Fatal("expected a fresh session to be created under the same id")
	}

	// Poll: the old pump's goroutine may still be finishing (cmd.Wait,
	// cleanup) after done closed; once it reaches its delete, the map must
	// still hold the *new* session, not be missing the entry or holding the
	// old one.
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		got := m.sessions[sessionID]
		m.mu.Unlock()
		if got == newSession {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected m.sessions[%q] to remain the new session, got %v (want %v)", sessionID, got, newSession)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestTerminalManager_AttachUnblocksOnProcessExit verifies that when the CLI
// process exits while a client is attached but idle (not sending anything),
// Attach's read pump unblocks (conn.Read errors because the server side
// closes the connection) instead of leaking the handler goroutine and WS
// connection forever.
func TestTerminalManager_AttachUnblocksOnProcessExit(t *testing.T) {
	m := NewTerminalManager()
	sessionID := "exit-unblock-sess"
	defer m.Stop(sessionID)

	orig := buildTerminalCommand
	buildTerminalCommand = func(_, _ string, _ bool) (string, []string, error) {
		return "sh", nil, nil
	}
	t.Cleanup(func() { buildTerminalCommand = orig })

	repoDir := t.TempDir()

	attachReturned := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
		_ = m.Attach(r.Context(), sessionID, repoDir, "claude", "", false, conn)
		close(attachReturned)
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

	// Terminate the shell; the client then goes idle (sends nothing further).
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("exit\n")); err != nil {
		t.Fatalf("write exit: %v", err)
	}

	// The server must close the connection once the CLI process exits, which
	// unblocks conn.Read on the client side within a couple of seconds even
	// though the client itself sends nothing more.
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	for {
		if _, _, err := conn.Read(readCtx); err != nil {
			break // connection closed by the server, as expected
		}
	}

	// The handler goroutine (and therefore Attach) must also have returned.
	select {
	case <-attachReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("Attach did not return after the CLI process exited")
	}
}

// readUntil reads frames until `marker` appears in the accumulated output or the
// context deadline hits.
func readUntil(t *testing.T, ctx context.Context, c *websocket.Conn, marker string) string {
	t.Helper()
	var sb strings.Builder
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return sb.String()
		}
		sb.Write(data)
		if strings.Contains(sb.String(), marker) {
			return sb.String()
		}
	}
}
