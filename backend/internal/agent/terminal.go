package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"nhooyr.io/websocket"
)

// TerminalManager runs interactive CLI sessions in a PTY, one live process per
// chat session. Unlike the task Pool (headless `-p` runs), the process stays
// alive across WebSocket disconnects so a browser refresh reattaches to the same
// running CLI — including any pending approval prompt. cwd is the session's repo
// worktree, so the agent operates on that repo.
//
// Conversation history across a *process* restart (backend restart, or the CLI
// exiting) is provided by the CLI's own on-disk session store via its resume
// flag; see terminalCommand. In-process reattach replays a scrollback ring so a
// refresh shows what was already on screen.
type TerminalManager struct {
	mu       sync.Mutex
	sessions map[string]*ptySession
}

// NewTerminalManager builds an empty manager.
func NewTerminalManager() *TerminalManager {
	return &TerminalManager{sessions: make(map[string]*ptySession)}
}

// scrollbackCap bounds the per-session replay ring. Enough to redraw a full-ish
// screen plus recent output on reattach without unbounded memory growth.
const scrollbackCap = 256 * 1024

type ptySession struct {
	cmd *exec.Cmd
	tty *os.File // PTY master

	mu         sync.Mutex
	scrollback []byte    // ring of recent output, capped at scrollbackCap
	attached   io.Writer // current WS writer, nil when detached
	done       chan struct{}
}

// ErrTerminalUnsupported means the session's provider has no interactive CLI
// (e.g. the `anthropic` API provider, which is not a terminal program).
var ErrTerminalUnsupported = errors.New("provider has no interactive terminal")

// Attach connects conn to the session's PTY, starting the process on first use.
// It blocks until the connection closes (client disconnect or process exit),
// then leaves the process running for the next attach. resume asks the CLI to
// continue its most recent session in this cwd (used when the session has run
// before — i.e. after a process exit or backend restart); it's ignored when the
// process is already live (in-uptime reconnect just reattaches).
//
// Only one connection may be attached at a time; a second attach to the same
// session takes over output (the previous writer is dropped).
func (m *TerminalManager) Attach(ctx context.Context, sessionID, repoPath, provider, model string, resume bool, conn *websocket.Conn) error {
	s, err := m.ensure(sessionID, repoPath, provider, model, resume)
	if err != nil {
		return err
	}

	// Writer that forwards PTY output to this WS as binary frames.
	wsw := &wsWriter{ctx: ctx, conn: conn}

	s.mu.Lock()
	// Replay scrollback so a reconnecting browser sees the current screen.
	if len(s.scrollback) > 0 {
		_, _ = wsw.Write(s.scrollback)
	}
	s.attached = wsw
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.attached == wsw {
			s.attached = nil
		}
		s.mu.Unlock()
	}()

	// Read pump: client keystrokes / control frames -> PTY.
	for {
		typ, data, rerr := conn.Read(ctx)
		if rerr != nil {
			return nil // client gone; process stays alive
		}
		select {
		case <-s.done:
			return nil // process already exited
		default:
		}
		// Text frames beginning with the resize sentinel carry a window size;
		// everything else is raw stdin for the CLI.
		if typ == websocket.MessageText {
			if rows, cols, ok := parseResize(data); ok {
				_ = pty.Setsize(s.tty, &pty.Winsize{Rows: rows, Cols: cols})
				continue
			}
		}
		if _, werr := s.tty.Write(data); werr != nil {
			return nil
		}
	}
}

// ensure returns the running session, starting it if not present. When it must
// start the process, resume asks the CLI to continue its most recent session in
// this cwd (see Attach).
func (m *TerminalManager) ensure(sessionID, repoPath, provider, model string, resume bool) (*ptySession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		return s, nil
	}

	name, args, err := buildTerminalCommand(provider, model, resume)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = repoPath // ← run the CLI in the selected repo's worktree
	cmd.Env = os.Environ()

	tty, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	s := &ptySession{cmd: cmd, tty: tty, done: make(chan struct{})}
	m.sessions[sessionID] = s

	// Output pump: PTY -> scrollback + attached WS. Runs for the process's life.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := tty.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				s.mu.Lock()
				s.appendScrollback(chunk)
				w := s.attached
				s.mu.Unlock()
				if w != nil {
					_, _ = w.Write(chunk)
				}
			}
			if rerr != nil {
				break
			}
		}
		close(s.done)
		_ = cmd.Wait()
		m.mu.Lock()
		delete(m.sessions, sessionID)
		m.mu.Unlock()
	}()

	return s, nil
}

// Stop kills the session's process and closes its PTY. No-op if not running.
func (m *TerminalManager) Stop(sessionID string) {
	m.mu.Lock()
	s := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if s == nil {
		return
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.tty.Close()
}

// appendScrollback appends to the ring, trimming to scrollbackCap. Caller holds s.mu.
func (s *ptySession) appendScrollback(chunk []byte) {
	s.scrollback = append(s.scrollback, chunk...)
	if len(s.scrollback) > scrollbackCap {
		s.scrollback = s.scrollback[len(s.scrollback)-scrollbackCap:]
	}
}

// buildTerminalCommand is the seam ensure() uses to build the launch command;
// a var so tests can substitute an always-present program (e.g. sh) for a real
// provider CLI, which would otherwise need auth to start.
var buildTerminalCommand func(provider, model string, resume bool) (string, []string, error) = terminalCommand

// terminalCommand builds the interactive-launch command for a provider. When
// resume is set it appends the CLI's "continue most recent session in this cwd"
// form — unambiguous here because each chat session runs in its own worktree
// dir (.ate-worktrees/<session-id>), so a cwd only ever hosts one session's
// history. These continue forms differ per CLI and were verified against each
// tool's help/docs:
//   - claude/qwen/opencode: `--continue` (cwd/project-scoped)
//   - gemini: `--resume` with no id ("immediately loads the most recent session")
//   - codex: `resume --last` (a subcommand; cwd-filtered by default)
func terminalCommand(provider, model string, resume bool) (name string, args []string, err error) {
	switch provider {
	case "claude":
		name = "claude"
		if resume {
			args = append(args, "--continue")
		}
		if model != "" {
			args = append(args, "--model", model)
		}
	case "codex_cli":
		name = "codex"
		if resume {
			args = append(args, "resume", "--last") // subcommand; cwd-filtered
		}
		if model != "" {
			args = append(args, "--model", model)
		}
	case "gemini_cli":
		name = "gemini"
		if resume {
			args = append(args, "--resume") // no id => most recent session
		}
		if model != "" {
			args = append(args, "--model", model)
		}
	case "qwen_code":
		name = "qwen"
		if resume {
			args = append(args, "--continue")
		}
		if model != "" {
			args = append(args, "--model", model)
		}
	case "opencode":
		name = "opencode"
		if resume {
			args = append(args, "--continue")
		}
		if model != "" {
			args = append(args, "--model", model)
		}
	default:
		// anthropic (API provider) and anything unknown: no interactive terminal.
		return "", nil, ErrTerminalUnsupported
	}
	return name, args, nil
}

// wsWriter adapts a WebSocket connection to io.Writer, sending each write as one
// binary frame. Write errors are swallowed — a dead connection is detected by
// the read pump, which returns and detaches this writer.
type wsWriter struct {
	ctx  context.Context
	conn *websocket.Conn
}

func (w *wsWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// parseResize decodes a "resize" control frame of the form "\x00resize:<cols>,<rows>".
// Returns ok=false for any frame that isn't a resize (i.e. normal stdin).
func parseResize(data []byte) (rows, cols uint16, ok bool) {
	const prefix = "\x00resize:"
	if len(data) < len(prefix) || string(data[:len(prefix)]) != prefix {
		return 0, 0, false
	}
	var c, r int
	if _, serr := fmt.Sscanf(string(data[len(prefix):]), "%d,%d", &c, &r); serr != nil {
		return 0, 0, false
	}
	if c <= 0 || r <= 0 {
		return 0, 0, false
	}
	return uint16(r), uint16(c), true
}
