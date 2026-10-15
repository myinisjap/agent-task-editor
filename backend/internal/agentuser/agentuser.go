// Package agentuser runs agent-facing subprocesses (provider CLIs, chat
// terminals, the deprecated providers' bash tool) as a separate unprivileged
// OS user so a prompt-injected or misbehaving agent cannot read the server's
// own secrets: the SQLite DB under /data, /proc/<server pid>/environ
// (API_TOKEN, GITHUB_TOKEN, provider keys) or the server user's ~/.config/gh.
//
// How it works. The container entrypoint starts the server as the unprivileged
// "node" user but with CAP_SETUID and CAP_SETGID as *ambient* capabilities
// (setpriv) plus CAP_KILL (to cancel the agent's processes), and exports
// AGENT_UID/AGENT_GID/AGENT_HOME. Apply then sets
// SysProcAttr.Credential on the child, so the fork/exec switches uid/gid
// before exec.
//
// Keeping the capabilities away from the child. Ambient capabilities survive
// execve, and a uid change between two non-root ids does not clear them, so a
// bare Credential switch would hand the agent CAP_SETUID and let it become
// "node" again. Apply therefore wraps the command in
// `setpriv --inh-caps=-all --ambient-caps=-all --`: the wrapper is exec'd
// (as the agent uid) with the caps still ambient, empties the inheritable and
// ambient sets, then execs the real CLI. With no file capabilities, no
// inheritable and no ambient caps, the CLI's permitted/effective sets are
// empty. Init additionally runs a probe through the very same path at startup
// and refuses to enable isolation if the child has any capability or the wrong
// uid, so a regression degrades to the old behaviour with an error rather than
// to a false sense of security.
//
// Server-side children (git, gh, mise, uv, ...) get the same cap-dropping
// wrapper via ServerCommand/DropCaps, because an agent can plant hooks or
// config in the repos they operate on.
//
// Fallback. When the server lacks the ability to switch (no CAP_SETUID/
// CAP_SETGID, AGENT_UID unset, `./dev.sh dev` outside Docker, compose `user:`
// pinned, ...) isolation is disabled with a startup warning and children run as
// the current user; this never prevents the server from starting.
package agentuser

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	capKill   = 5
	capSetgid = 6
	capSetuid = 7
)

// Config is the resolved agent-user decision for this process.
type Config struct {
	// Enabled is true when children are switched to UID/GID.
	Enabled bool
	UID     uint32
	GID     uint32
	// Home is the HOME advertised to agent children (the agent account's own
	// home, not the server user's).
	Home string
	// SetprivPath is the absolute path of the capability-clearing wrapper
	// ("" when none is used, which Decide only allows for a root server).
	SetprivPath string
	// Reason explains why Enabled is false (empty when enabled).
	Reason string
}

// Inputs are the facts Decide bases its choice on; gathered from the
// environment by Detect, supplied directly by tests.
type Inputs struct {
	Isolation   string // AGENT_ISOLATION ("", "true", "false", ...)
	AgentUID    string // AGENT_UID
	AgentGID    string // AGENT_GID
	AgentHome   string // AGENT_HOME
	EUID        int
	CapEff      uint64 // effective capability bitmask of this process
	SetprivPath string // resolved setpriv binary, "" if not found
}

// Decide is the pure credential-selection / fallback decision.
func Decide(in Inputs) Config {
	off := func(reason string) Config { return Config{Reason: reason} }

	switch strings.ToLower(strings.TrimSpace(in.Isolation)) {
	case "false", "0", "no", "off":
		return off("AGENT_ISOLATION disables the separate agent user")
	}
	if strings.TrimSpace(in.AgentUID) == "" {
		return off("AGENT_UID is not set (not started via the container entrypoint?)")
	}
	uid, err := strconv.ParseUint(strings.TrimSpace(in.AgentUID), 10, 32)
	if err != nil {
		return off(fmt.Sprintf("AGENT_UID %q is not a number", in.AgentUID))
	}
	if uid == 0 {
		return off("AGENT_UID is 0; refusing to treat root as the unprivileged agent user")
	}
	g := strings.TrimSpace(in.AgentGID)
	if g == "" {
		return off("AGENT_GID is not set")
	}
	gid, err := strconv.ParseUint(g, 10, 32)
	if err != nil {
		return off(fmt.Sprintf("AGENT_GID %q is not a number", in.AgentGID))
	}
	if gid == 0 {
		return off("AGENT_GID is 0; refusing to run agents in root's group")
	}
	if in.EUID >= 0 && uid == uint64(in.EUID) {
		return off("the server already runs as AGENT_UID; nothing to switch")
	}
	// CAP_KILL is needed because the server cancels/times out runs by signalling
	// the agent's process group, which belongs to a different uid.
	if need := uint64(1<<capSetuid | 1<<capSetgid | 1<<capKill); in.CapEff&need != need {
		return off("server lacks CAP_SETUID/CAP_SETGID/CAP_KILL (running non-root without the capabilities, e.g. compose `user:` or outside Docker)")
	}
	if in.SetprivPath == "" && in.EUID != 0 {
		// Without the wrapper a non-root server's ambient caps would leak into
		// the child. (A root server loses them by kernel rule on setuid.)
		return off("setpriv (util-linux) not found; cannot guarantee the agent child drops CAP_SETUID")
	}
	home := strings.TrimSpace(in.AgentHome)
	if home == "" {
		home = "/home/agent"
	}
	return Config{Enabled: true, UID: uint32(uid), GID: uint32(gid), Home: home, SetprivPath: in.SetprivPath}
}

// ParseCapEff extracts the CapEff bitmask from /proc/<pid>/status contents.
func ParseCapEff(status string) (uint64, bool) {
	for _, line := range strings.Split(status, "\n") {
		if v, ok := strings.CutPrefix(line, "CapEff:"); ok {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// Detect gathers Inputs from the live process environment.
func Detect() Inputs {
	in := Inputs{
		Isolation: os.Getenv("AGENT_ISOLATION"),
		AgentUID:  os.Getenv("AGENT_UID"),
		AgentGID:  os.Getenv("AGENT_GID"),
		AgentHome: os.Getenv("AGENT_HOME"),
		EUID:      os.Geteuid(),
	}
	if b, err := os.ReadFile("/proc/self/status"); err == nil {
		in.CapEff, _ = ParseCapEff(string(b))
	}
	if p, err := exec.LookPath("setpriv"); err == nil {
		in.SetprivPath = p
	}
	return in
}

// ChildEnv returns env with HOME/USER/LOGNAME pointed at the agent account.
// A nil env means "inherit os.Environ()" for exec.Cmd, which is materialised
// here so the overrides don't drop the rest.
func ChildEnv(env []string, c Config) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == "HOME" || k == "USER" || k == "LOGNAME" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+c.Home, "USER=agent", "LOGNAME=agent")
}

// Env returns env adjusted for the agent account when isolation is active (HOME
// etc.), or env unchanged otherwise.
func Env(env []string) []string {
	if c := Current(); c.Enabled {
		return ChildEnv(env, c)
	}
	return env
}

// Wrap returns the Path/Args for cmd after inserting the capability-clearing
// wrapper (pure; no wrapper when c.SetprivPath is empty).
func Wrap(c Config, path string, args []string) (string, []string) {
	if c.SetprivPath == "" {
		return path, args
	}
	out := []string{c.SetprivPath, "--inh-caps=-all", "--ambient-caps=-all", "--", path}
	if len(args) > 1 {
		out = append(out, args[1:]...)
	}
	return c.SetprivPath, out
}

var current atomic.Pointer[Config]

// Current returns the active configuration (disabled until Init runs).
func Current() Config {
	if c := current.Load(); c != nil {
		return *c
	}
	return Config{Reason: "not initialised"}
}

// Enabled reports whether agent children are switched to the agent user.
func Enabled() bool { return Current().Enabled }

// Apply makes cmd run as the agent user, if isolation is active. It must be
// called after cmd.Path/Args/Env are final and before Start; it merges into any
// existing SysProcAttr (e.g. Setpgid). A no-op when isolation is disabled.
func Apply(cmd *exec.Cmd) {
	c := Current()
	if !c.Enabled || cmd == nil || cmd.Err != nil {
		return
	}
	apply(c, cmd)
}

func apply(c Config, cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: c.UID, Gid: c.GID, Groups: []uint32{c.GID}}
	// Deliberately no AmbientCaps: nothing is added, and the wrapper below
	// removes the inherited ones.
	cmd.SysProcAttr.AmbientCaps = nil
	cmd.Path, cmd.Args = Wrap(c, cmd.Path, cmd.Args)
	cmd.Env = ChildEnv(cmd.Env, c)
}

// DropCaps strips the server's ambient capabilities from a command the SERVER
// itself runs (git, gh, mise, uv, ...), by wrapping it in the same
// `setpriv --inh-caps=-all --ambient-caps=-all --` prefix agent children get.
// Without it such children inherit CAP_SETUID/SETGID/KILL (ambient caps survive
// execve), and since agents can write to the repos those tools operate on, a
// planted git hook, core.fsmonitor/core.sshCommand, or mise/uv config would run
// with CAP_SETUID and could become any uid. Must be called after Path/Args are
// final and before Start. A no-op when isolation is not active.
func DropCaps(cmd *exec.Cmd) {
	c := Current()
	if !c.Enabled || cmd == nil || cmd.Err != nil {
		return
	}
	cmd.Path, cmd.Args = Wrap(c, cmd.Path, cmd.Args)
}

// ServerCommand is exec.Command for server-side (non-agent) children: every
// such spawn must use it (or ServerCommandContext) so no child carries the
// server's capabilities. A guard test rejects raw exec.Command elsewhere.
func ServerCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	DropCaps(cmd)
	return cmd
}

// ServerCommandContext is the context-aware form of ServerCommand.
func ServerCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	DropCaps(cmd)
	return cmd
}

// ShareDir makes a server-created private temp directory (MkdirTemp, 0700)
// usable by the agent user, which shares the server's primary group but not its
// uid and so could not otherwise enter it. writable=true is for directories the
// agent must create files in (the MCP sidecar's result file). The
// server cannot chown to the agent uid (it only holds CAP_SETUID/SETGID), so
// access is granted via the shared group. A no-op when isolation is off.
func ShareDir(dir string, writable bool) error {
	if !Enabled() {
		return nil
	}
	mode := os.FileMode(0o750)
	if writable {
		mode = 0o770
	}
	return os.Chmod(dir, mode)
}

// SharedFileMode is the mode for a server-written file that the agent user must
// also be able to read and write (e.g. ~/.claude/.credentials.json after a
// token refresh): 0660 via the shared group when isolation is on, else the
// historical 0600.
func SharedFileMode() os.FileMode {
	if Enabled() {
		return 0o660
	}
	return 0o600
}

// ShareFile makes a server-written 0600 file group-readable for the agent
// user. A no-op when isolation is off.
func ShareFile(path string) error {
	if !Enabled() {
		return nil
	}
	return os.Chmod(path, 0o640)
}

// claudeConfigPaths are the host-mounted Claude CLI files the agent user's
// claude reads at startup (relative to the shared home). The same host
// ~/.claude and ~/.claude.json are mounted at both /home/node and /home/agent.
var claudeConfigPaths = []string{
	".claude",
	".claude/settings.json",
	".claude/settings.local.json",
	".claude/.credentials.json",
	".claude.json",
}

// ShareClaudeConfig re-grants the agent user (via the shared group) read/write
// on the Claude CLI's config files under home (the server's HOME, which holds
// the same host-mounted files as the agent's). The entrypoint does this once at
// start, but the host's own Claude Code, or dev.sh/run.sh's keychain sync,
// rewrite these files owner-only (0600) while the container runs; the agent's
// claude then fails with "Settings file could not be read: EACCES". Called
// before each claude spawn. Best effort and a no-op when isolation is off.
func ShareClaudeConfig(home string) {
	if !Enabled() || home == "" {
		return
	}
	shareGroupRW(home, claudeConfigPaths)
}

// shareGroupRW adds group read/write (and search, for directories) to each
// existing rel path under home that lacks it. Symlinks are skipped (the bind
// mounts are real files) and errors, e.g. a file the agent itself created and
// so the server does not own, are logged and ignored.
func shareGroupRW(home string, rels []string) {
	for _, rel := range rels {
		p := home + "/" + rel
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		mode := fi.Mode().Perm()
		want := mode | 0o060
		if fi.IsDir() {
			want |= 0o010
		} else if !fi.Mode().IsRegular() {
			continue
		}
		if want == mode {
			continue
		}
		if err := os.Chmod(p, want); err != nil {
			slog.Debug("could not share claude config with the agent user", "path", p, "err", err)
		}
	}
}

// Init resolves the configuration, verifies it with a live probe and logs the
// outcome. It never fails: on any problem agents keep running as the server
// user, with a warning.
func Init() Config {
	c := Decide(Detect())
	if c.Enabled {
		if err := probe(c); err != nil {
			slog.Error("agent user isolation self-check failed; agents will run as the SERVER user (no isolation)", "err", err)
			c = Config{Reason: "self-check failed: " + err.Error()}
		} else {
			slog.Info("agent subprocesses run as a separate unprivileged user", "uid", c.UID, "gid", c.GID, "home", c.Home)
		}
	} else {
		slog.Warn("agent user isolation is OFF: agents run as the same user as the server and can read its database, environment secrets and gh credentials", "reason", c.Reason)
	}
	current.Store(&c)
	return c
}

// ParseProbe validates the output of the self-check command: the first line is
// the uid, followed by /proc/self/status. Every capability set except the
// bounding set must be zero.
func ParseProbe(out string, wantUID uint32) error {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != strconv.FormatUint(uint64(wantUID), 10) {
		return fmt.Errorf("child uid mismatch (want %d, output %q)", wantUID, firstLine(out))
	}
	seen := 0
	for _, l := range lines[1:] {
		for _, set := range []string{"CapInh", "CapPrm", "CapEff", "CapAmb"} {
			if v, ok := strings.CutPrefix(l, set+":"); ok {
				seen++
				if n, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64); err != nil || n != 0 {
					return fmt.Errorf("child retains capabilities (%s=%s)", set, strings.TrimSpace(v))
				}
			}
		}
	}
	if seen < 4 {
		return fmt.Errorf("could not read the child's capability sets (%d of 4)", seen)
	}
	return nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

func probe(c Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "id -u; cat /proc/self/status")
	apply(c, cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("probe command failed: %w", err)
	}
	return ParseProbe(string(out), c.UID)
}
