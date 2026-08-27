package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
)

// ChatMCPProvisioner builds the per-provider CLI wiring that exposes extra MCP
// tools to an interactive chat session — currently the board server
// (list_repos / list_workflows / create_task), which lets a chat work through a
// plan and create tickets. Given the session's provider and id, it returns
// additional CLI args, additional environment entries ("KEY=VALUE"), and a
// cleanup func (always non-nil) to remove any temp files it created, called when
// the CLI process exits.
//
// It is injected from cmd/server with an implementation in the providers package
// so this package stays free of provider-specific MCP config shapes. A nil
// provisioner (the default) leaves chat sessions exactly as before.
type ChatMCPProvisioner func(provider, sessionID string) (args []string, env []string, cleanup func(), err error)

// EnvAllowlistFunc returns the allowlisted subset of the backend's own
// environment for the given provider — the base env for that provider's CLI
// subprocess. Injected from cmd/server with providers.EnvAllowlistFor so
// this package stays free of provider-specific allowlists, mirroring
// ChatMCPProvisioner above. A nil func (the zero value) falls back to
// os.Environ() for backward compatibility with any caller that doesn't wire
// it up — see the doc comment on TerminalManager.EnvAllowlist for why this
// should always be set in production.
type EnvAllowlistFunc func(provider string) []string

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

	// ChatMCP, when set, wires extra MCP tools (the board server) into each
	// session's CLI. Nil disables the feature (no change to the launched CLI).
	ChatMCP ChatMCPProvisioner

	// EnvAllowlist, when set, scopes each session's CLI subprocess env to
	// that provider's allowlist (see providers.EnvAllowlistFor) instead of
	// the backend's full os.Environ() — closing the same backend-secret-leak
	// path (LLM_API_KEY, API_TOKEN, DB creds, etc. otherwise visible to
	// `env`/`printenv` inside the CLI's Bash tool) that the headless runners
	// were scoped against in #321. Wired from cmd/server with
	// providers.EnvAllowlistFor; nil (e.g. in tests that don't set it) falls
	// back to os.Environ() for backward compatibility.
	EnvAllowlist EnvAllowlistFunc

	// MaxSessions caps the number of concurrent PTY subprocesses this manager
	// will keep alive at once. Each session holds a live subprocess plus a
	// scrollbackCap buffer indefinitely (until its process exits or it's
	// explicitly reaped), so on a long-lived board with no bound this grows
	// without limit. 0 (the zero value) means unlimited — set by the caller
	// (see cmd/server/main.go) after construction; unset by default so
	// existing deployments see no behavior change until configured.
	MaxSessions int

	// IdleTimeout, if > 0, is how long a session may go without an attached
	// WebSocket connection before ReapLoop stops it, releasing its subprocess
	// and scrollback buffer. 0 (the default) disables idle reaping — a
	// session then lives until its process exits or Stop is called
	// explicitly, matching pre-existing behavior.
	IdleTimeout time.Duration
}

// NewTerminalManager builds an empty manager. MaxSessions/IdleTimeout default
// to unbounded/disabled; set the exported fields after construction to
// enable them (see cmd/server/main.go).
func NewTerminalManager() *TerminalManager {
	return &TerminalManager{sessions: make(map[string]*ptySession)}
}

// scrollbackCap bounds the per-session replay ring. Enough to redraw a full-ish
// screen plus recent output on reattach without unbounded memory growth.
const scrollbackCap = 256 * 1024

type ptySession struct {
	cmd *exec.Cmd
	tty *os.File // PTY master

	mu             sync.Mutex
	scrollback     []byte    // ring of recent output, capped at scrollbackCap
	attached       io.Writer // current WS writer, nil when detached
	lastDetachedAt time.Time // when attached last went nil; zero while attached
	done           chan struct{}
	cleanup        func() // removes per-session MCP temp files; run after the process exits
}

// ErrTerminalUnsupported means the session's provider has no interactive CLI
// (e.g. the `anthropic` API provider, which is not a terminal program).
var ErrTerminalUnsupported = errors.New("provider has no interactive terminal")

// ErrTooManySessions is returned by Attach/ensure when MaxSessions is set and
// starting a new session's process would exceed it. Existing sessions
// (including idle ones not yet reaped) are unaffected; only *new* sessions are
// refused until one exits or is reaped.
var ErrTooManySessions = errors.New("too many concurrent terminal sessions")

// Attach connects conn to the session's PTY, starting the process on first use.
// It blocks until the connection closes (client disconnect or process exit),
// then leaves the process running for the next attach. resume asks the CLI to
// continue its most recent session in this cwd (used when the session has run
// before — i.e. after a process exit or backend restart); it's ignored when the
// process is already live (in-uptime reconnect just reattaches).
//
// repoID/pins carry the session's repo runtime pins (repos.runtime_languages,
// already parsed by the caller via runtime.ParsePins) so the interactive CLI
// sees the same toolchain a headless task run on this repo would (see
// prepareChatRuntime). Both are ignored (no-op) once the session's process is
// already running — pins can't be changed on a live session without
// restarting it, matching how a headless run's pins are fixed at dispatch.
//
// Only one connection may be attached at a time; a second attach to the same
// session takes over output (the previous writer is dropped).
func (m *TerminalManager) Attach(ctx context.Context, sessionID, repoPath, provider, model string, resume bool, repoID string, pins []runtime.Pin, conn *websocket.Conn) error {
	// Prep runs BEFORE ensure() and its manager-wide lock — mise install / uv
	// venv can take real time (cold installs), and ensure() holds m.mu for
	// its whole body, so running prep inside it (or under the lock at all)
	// would block every other session's Attach/Stop for as long as prep
	// takes. Only needed for a session that isn't already running (an
	// already-live process keeps whatever toolchain it started with); skip
	// pins entirely otherwise so a reattach never re-runs mise/uv.
	if !m.isRunning(sessionID) {
		if err := prepareChatRuntime(ctx, repoID, pins); err != nil {
			return fmt.Errorf("chat runtime prep failed: %w", err)
		}
	} else {
		pins = nil
	}

	s, err := m.ensure(sessionID, repoPath, provider, model, resume, repoID, pins)
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
	s.lastDetachedAt = time.Time{} // attached: not idle
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.attached == wsw {
			s.attached = nil
			s.lastDetachedAt = time.Now() // starts this session's idle clock for ReapLoop
		}
		s.mu.Unlock()
	}()

	// The read pump below blocks in conn.Read until the client sends
	// something. If the CLI process exits first (crash, /exit, auth expiry)
	// nothing would wake it: the session is already gone from m.sessions so
	// the reaper can't reach it, and the handler goroutine + WS connection
	// would leak with the browser showing a frozen terminal. Close the
	// connection on process exit so Read returns and the caller can react.
	//
	// watchDone stops this goroutine when Attach returns for any other
	// reason (client disconnect); without it a long-lived session would
	// accumulate one watcher per attach. Registered before the read loop so
	// it runs (and the watcher exits) before a subsequent attach could race
	// it; defer ordering vs. the detach-bookkeeping defer above doesn't
	// matter functionally.
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-s.done:
			_ = conn.Close(websocket.StatusNormalClosure, "terminal session ended")
		case <-watchDone:
		case <-ctx.Done():
		}
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

// isRunning reports whether sessionID already has a live process, without
// starting one — used by Attach to decide whether runtime pins need
// (re-)preparing before ensure() is called.
func (m *TerminalManager) isRunning(sessionID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[sessionID]
	return ok
}

// ensure returns the running session, starting it if not present. When it must
// start the process, resume asks the CLI to continue its most recent session in
// this cwd (see Attach). pins (already prepared by Attach via
// prepareChatRuntime before this is called) wraps the launch command with
// `mise x` exactly like providers.applyRuntime does for headless task runs —
// see applyChatRuntime.
func (m *TerminalManager) ensure(sessionID, repoPath, provider, model string, resume bool, repoID string, pins []runtime.Pin) (*ptySession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		return s, nil
	}

	// Cap concurrent live processes. Only applies to *starting a new* session —
	// an already-running session (handled above) is never refused, since a
	// reattach must never fail just because the manager is at capacity.
	if m.MaxSessions > 0 && len(m.sessions) >= m.MaxSessions {
		return nil, ErrTooManySessions
	}

	name, args, err := buildTerminalCommand(provider, model, resume)
	if err != nil {
		return nil, err
	}

	// Wire the board MCP tools into this session's CLI when configured. Failures
	// are non-fatal: the chat still works, just without the create-task tools.
	var extraEnv []string
	var cleanup func()
	if m.ChatMCP != nil {
		mcpArgs, mcpEnv, cl, perr := m.ChatMCP(provider, sessionID)
		if perr != nil {
			slog.Warn("chat MCP provisioning failed; continuing without board tools", "session_id", sessionID, "err", perr)
		} else {
			args = append(args, mcpArgs...)
			extraEnv = mcpEnv
			cleanup = cl
		}
	}

	// Wrap with `mise x` for any non-python pins, exactly like
	// providers.applyRuntime does for headless task runs (mirrored here
	// rather than shared, since providers imports agent and this package
	// can't import providers back). A python pin instead prepends the
	// chat-specific venv's bin/ to PATH (see prepareChatRuntime / chatVenvDir
	// — never <repoPath>/.venv: that would live inside the user's own
	// checkout across reconnects). A node pin gets the explicit-interpreter
	// rewrite (see applyChatRuntime's doc comment) so claude/qwen's own CLI
	// process still runs on the image's bundled node even while node/npm/npx
	// inside the agent's Bash tool resolve to the pin.
	name, args, runtimeEnv, venvBin, err := applyChatRuntime(pins, repoID, name, args)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("apply chat runtime: %w", err)
	}

	cmd := exec.Command(name, args...)
	cmd.Dir = repoPath // ← run the CLI in the selected repo's worktree
	// Base env: the provider's allowlisted subset of the backend's own
	// environment (see EnvAllowlist doc comment / #321), not the full
	// os.Environ() — a chat session's CLI has the same Bash access as a
	// headless run, so it must not be able to read backend-only secrets
	// (LLM_API_KEY, API_TOKEN, DB creds, etc.) via `env`/`printenv`/
	// /proc/self/environ either. Falls back to os.Environ() only when
	// EnvAllowlist isn't wired up (e.g. some tests), to avoid silently
	// breaking callers that haven't set it.
	var baseEnv []string
	if m.EnvAllowlist != nil {
		baseEnv = m.EnvAllowlist(provider)
	} else {
		baseEnv = os.Environ()
	}
	// Advertise a color-capable terminal. The backend process has no TERM in the
	// container, so without this the PTY inherits an empty TERM and many CLIs
	// disable color. xterm.js on the client is a 256-color/truecolor emulator, so
	// these values are accurate. Set after the base env so they win over any
	// inherited value.
	cmd.Env = append(baseEnv, "TERM=xterm-256color", "COLORTERM=truecolor")
	// Force-disabled per provider: each CLI's binary is pinned by a
	// *_CLI_VERSION build arg in backend/Dockerfile, so a self-update mid-session
	// would silently drift the running binary away from what's pinned — and
	// otherwise surfaces as an "update available" warning in the chat terminal.
	// Mirrors the same env forcing in each headless Runner (claude.go, qwen.go);
	// codex has no env var for this, so its opt-out is a CLI flag (see
	// terminalCommand above) instead.
	switch provider {
	case "claude":
		cmd.Env = append(cmd.Env, "DISABLE_AUTOUPDATER=1")
	case "qwen_code":
		cmd.Env = append(cmd.Env, "NO_UPDATE_NOTIFIER=1")
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Env = append(cmd.Env, runtimeEnv...)
	if venvBin != "" {
		cmd.Env = prependChatPath(cmd.Env, venvBin)
	}

	tty, err := pty.Start(cmd)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("start pty: %w", err)
	}

	s := &ptySession{cmd: cmd, tty: tty, done: make(chan struct{}), cleanup: cleanup}
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
		if s.cleanup != nil {
			s.cleanup()
		}
		// Owner-scoped delete: Stop()/reapIdleOnce may already have removed
		// this entry, and a reattach may have inserted a *fresh* session
		// under the same id before cmd.Wait() returned here. Deleting
		// unconditionally would orphan that new session — alive, but
		// unreachable by Stop/reapIdleOnce and no longer counted against
		// MaxSessions. Same fix shape as #244's ClearActiveAgentRunIfOwner.
		m.mu.Lock()
		if m.sessions[sessionID] == s {
			delete(m.sessions, sessionID)
		}
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

// reapCheckInterval is how often ReapLoop scans for idle sessions. Fixed
// (rather than derived from IdleTimeout) so a long IdleTimeout still gets
// reaped reasonably promptly after crossing the threshold, and a short one in
// tests doesn't require a matching tiny interval.
const reapCheckInterval = 30 * time.Second

// ReapLoop periodically stops sessions that have had no attached WebSocket
// connection for at least IdleTimeout, releasing their subprocess and
// scrollback buffer. No-op (never stops anything) while IdleTimeout <= 0.
// Runs until ctx is cancelled; intended to be started once from cmd/server
// alongside the manager, mirroring the internal/backup and
// internal/logretention scheduler pattern.
func (m *TerminalManager) ReapLoop(ctx context.Context) {
	ticker := time.NewTicker(reapCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reapIdleOnce()
		}
	}
}

// reapIdleOnce stops every session that's been detached for at least
// IdleTimeout. Exported behavior only through ReapLoop; kept unexported and
// directly callable so tests don't need to wait on the ticker.
func (m *TerminalManager) reapIdleOnce() {
	if m.IdleTimeout <= 0 {
		return
	}
	now := time.Now()

	var toReap []string
	m.mu.Lock()
	for id, s := range m.sessions {
		s.mu.Lock()
		idleSince := s.lastDetachedAt
		s.mu.Unlock()
		if !idleSince.IsZero() && now.Sub(idleSince) >= m.IdleTimeout {
			toReap = append(toReap, id)
		}
	}
	m.mu.Unlock()

	for _, id := range toReap {
		slog.Info("terminal: reaping idle session", "session_id", id, "idle_timeout", m.IdleTimeout)
		m.Stop(id)
	}
}

// prepareChatRuntime installs a chat session's repo runtime pins (mise
// install) and, for a python pin, ensures the chat-specific venv exists —
// the interactive-session counterpart to the dispatcher's runtime prep
// (Dispatcher.prepareRuntime / Pool.prepareRuntime) for headless task runs.
// No-op for an unconfigured repo (nil/empty pins) or an empty repoID —
// mirrors the byte-identical guarantee: a repo with no runtime_languages
// pins never shells out to mise/uv for its chat sessions either.
//
// Unlike a task run, this deliberately does NOT create <repoPath>/.venv:
// repoPath here is the session's own worktree, which persists across
// reconnects and is the user's live checkout to poke around in — dropping a
// build artifact there (even one excluded from git) is a worse experience
// than for a short-lived, throwaway task worktree. Instead the venv lives
// outside any repo checkout, keyed by repo id + python version (see
// chatVenvDir), so multiple chat sessions against the same repo/version
// reuse one venv the same way task runs reuse the shared uv cache.
func prepareChatRuntime(ctx context.Context, repoID string, pins []runtime.Pin) error {
	if repoID == "" || len(pins) == 0 {
		return nil
	}

	if err := runtime.Install(ctx, pins); err != nil {
		return err
	}

	for _, p := range pins {
		if p.ID != "python" {
			continue
		}
		pythonPath, err := runtime.ResolvePythonPath(ctx, p.Version)
		if err != nil {
			return err
		}
		return runtime.EnsureVenv(ctx, chatVenvDir(repoID), pythonPath, p.Version)
	}
	return nil
}

// chatVenvDir returns the base directory whose .venv subdirectory
// prepareChatRuntime creates/reuses for a repo's chat sessions — a sibling
// of mise's own data dir (same shared volume in production, so it persists
// across container restarts) rather than anywhere inside a repo checkout.
// Keyed by repo id only (not python version): EnsureVenv's own
// recreate-on-mismatch logic (see runtime.EnsureVenv) already handles a
// version-pin change by rebuilding the venv in place, so a second directory
// per version isn't needed.
func chatVenvDir(repoID string) string {
	return filepath.Join(filepath.Dir(runtime.MiseDataDir()), "chat-venvs", repoID)
}

// applyChatRuntime wraps a chat session's launch command with `mise x` for
// any non-python pins, mirroring providers.applyRuntime's shape for headless
// task runs. A nil/empty pins slice returns name/args unchanged and no extra
// env/venv dir. Returns the extra env entries to append (MISE_YES/
// MISE_DATA_DIR/MISE_TRUSTED_CONFIG_PATHS whenever pins are present, plus
// UV_CACHE_DIR for a python pin) and, only for a python pin, the venv's
// bin/ dir — the caller prepends that to cmd.Env's actual PATH (via
// prependChatPath) rather than this function guessing at the base PATH
// itself.
//
// A node pin gets the same explicit-interpreter treatment as
// providers.applyRuntime (duplicated here rather than shared — providers
// imports agent, so this package can't import providers back): when name
// resolves to a node script (claude/qwen — see isChatNodeScript), the
// launch becomes `mise x node@<pin> ... -- <systemNode> <absName> <args...>`
// so the CLI process itself always runs on the image's own bundled node,
// never the pinned one, while node/npm/npx inside the agent's Bash tool
// still resolve through mise x's PATH to the pin. codex/opencode are native
// binaries and spawn unwrapped, as before. Fails closed: if a node pin is
// present, name is a node script, but the system node or name's own
// absolute path can't be resolved, this returns an error — the caller must
// treat it as a launch failure rather than ever spawning on the wrong
// interpreter.
func applyChatRuntime(pins []runtime.Pin, repoID, name string, args []string) (string, []string, []string, string, error) {
	if len(pins) == 0 {
		return name, args, nil, "", nil
	}

	miseArgs := make([]string, 0, len(pins)+2+len(args))
	miseArgs = append(miseArgs, "x")
	hasPython, hasNode := false, false
	for _, p := range pins {
		switch p.ID {
		case "python":
			hasPython = true
			continue
		case "node":
			hasNode = true
		}
		miseArgs = append(miseArgs, p.ID+"@"+p.Version)
	}

	miseArgs = append(miseArgs, "--")
	if hasNode {
		nodePath, namePath, err := explicitInterpreterForChatNodeScript(name)
		if err != nil {
			return "", nil, nil, "", fmt.Errorf("resolve system interpreter for node-pinned CLI %q: %w", name, err)
		}
		if nodePath != "" {
			miseArgs = append(miseArgs, nodePath, namePath)
		} else {
			miseArgs = append(miseArgs, name)
		}
	} else {
		miseArgs = append(miseArgs, name)
	}
	miseArgs = append(miseArgs, args...)

	env := []string{"MISE_YES=1"}
	if dir := runtime.MiseDataDir(); dir != "" {
		env = append(env, "MISE_DATA_DIR="+dir)
	}
	venvDir := chatVenvDir(repoID)
	env = append(env, "MISE_TRUSTED_CONFIG_PATHS="+venvDir)

	var venvBin string
	if hasPython {
		venvBin = filepath.Join(venvDir, ".venv", "bin")
		if dir := runtime.UvCacheDir(); dir != "" {
			env = append(env, "UV_CACHE_DIR="+dir)
		}
	}

	return "mise", miseArgs, env, venvBin, nil
}

// explicitInterpreterForChatNodeScript mirrors
// providers.explicitInterpreterForNodeScript (duplicated for the same
// import-direction reason as applyChatRuntime): resolves name via
// exec.LookPath, and if it's a node script (see isChatNodeScript) returns
// the system node's absolute path plus name's own absolute path as the two
// argv elements that must replace it. Returns nodePath == "" (nil error) for
// a native binary — spawn unwrapped, as before.
func explicitInterpreterForChatNodeScript(name string) (nodePath, namePath string, err error) {
	namePath, err = exec.LookPath(name)
	if err != nil {
		return "", "", fmt.Errorf("resolve %q: %w", name, err)
	}
	isScript, err := isChatNodeScript(namePath)
	if err != nil {
		return "", "", fmt.Errorf("inspect %q: %w", namePath, err)
	}
	if !isScript {
		return "", "", nil
	}
	nodePath, err = exec.LookPath("node")
	if err != nil {
		return "", "", fmt.Errorf("resolve system node: %w", err)
	}
	return nodePath, namePath, nil
}

// isChatNodeScript mirrors providers.isNodeScript: reads path's first line
// and reports whether its shebang references node, either
// `#!/usr/bin/env node` (npm's standard global-install wrapper) or a
// shebang path ending in "/node". A native binary (codex, opencode) has no
// text shebang at all, so this returns false for those, never true by
// assumption.
func isChatNodeScript(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 4096)
	if !sc.Scan() {
		return false, sc.Err()
	}
	line := sc.Text()
	if !strings.HasPrefix(line, "#!") {
		return false, nil
	}
	shebang := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	fields := strings.Fields(shebang)
	if len(fields) == 0 {
		return false, nil
	}
	interp := fields[0]
	if filepath.Base(interp) == "env" && len(fields) > 1 {
		interp = fields[1]
	}
	return filepath.Base(interp) == "node", nil
}

// prependChatPath returns a copy of env with dir prepended to the PATH entry
// (added fresh if env has none) — mirrors providers/cli.go's prependPath,
// duplicated rather than shared for the same import-direction reason as
// applyChatRuntime.
func prependChatPath(env []string, dir string) []string {
	out := make([]string, len(env))
	found := false
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out[i] = "PATH=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
			found = true
			continue
		}
		out[i] = kv
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
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
		// Codex's TUI (unlike its headless `exec` mode) prints an "Update
		// available!" banner from a background version check; suppress it
		// since CODEX_CLI_VERSION is pinned in backend/Dockerfile and we don't
		// want the running binary drifting from what's built into the image.
		// -c overrides config.toml; no env var exists for this in Codex.
		args = append(args, "-c", "check_for_update=false")
		if resume {
			args = append(args, "resume", "--last") // subcommand; cwd-filtered
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
