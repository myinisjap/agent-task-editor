package agent

import (
	"context"
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
		{"gemini_cli", true, "gemini", []string{"--resume"}, false},       // no id => most recent
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
