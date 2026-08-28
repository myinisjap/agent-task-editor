package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// InstallTimeout bounds how long a single `mise install` run prep step may
// take before it's treated as a failure. Toolchain downloads are one-time
// per (language, version) — the shared mise data dir cache (a compose named
// volume in production) makes every install after the first on a given host
// near-instant — but a cold pull of a large SDK (e.g. a JDK) over a slow
// network can legitimately take minutes.
const InstallTimeout = 10 * time.Minute

// outputTailBytes caps how much of mise's combined stdout+stderr is kept for
// a failure message — enough to show the real error without flooding the
// run's error text with a full download log.
const outputTailBytes = 2048

// MiseDataDir returns the mise data dir mise subprocess calls should use: the
// backend's own MISE_DATA_DIR env var if set (a compose named volume in
// production — see backend/Dockerfile's ENV MISE_DATA_DIR), otherwise mise's
// own default of $HOME/.local/share/mise. Exported so providers.applyRuntime
// can inject the exact same value into a provider's `mise x` child env
// (allowlistEnv strips the backend's own ENV, so without this the child
// process could resolve a *different* data dir than the one prep just
// installed into) — this is "the same resolution prep.go uses" the two
// callers must never drift apart on.
func MiseDataDir() string {
	if v := os.Getenv("MISE_DATA_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "mise")
}

// UvCacheDir returns the shared uv package cache directory mise/uv
// subprocess calls should use: the backend's own UV_CACHE_DIR env var if set
// (a compose named volume in production — see backend/Dockerfile's ENV
// UV_CACHE_DIR), otherwise uv's own default of $HOME/.cache/uv. Exported so
// providers.applyRuntime can inject the exact same value a provider's `mise
// x` child env — without this, prep's `uv venv` call and the agent run's own
// `pip install`/`uv pip install` could use two different caches (allowlistEnv
// strips the backend's own ENV, including any operator-set UV_CACHE_DIR
// override, from the child env). Returns "" if $HOME can't be resolved
// (matches os.UserHomeDir's failure mode); callers must skip emitting the
// env var entirely on "" rather than set a bogus empty-valued UV_CACHE_DIR=.
func UvCacheDir() string {
	if v := os.Getenv("UV_CACHE_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "uv")
}

// miseEnv returns the environment for mise/uv subprocess calls at prep time:
// the current process's PATH and HOME (so mise resolves its own binary and
// finds its data dir under $HOME), MISE_DATA_DIR (see MiseDataDir),
// UV_CACHE_DIR (see UvCacheDir — shared with the agent run's own env so a
// python pin's prep and run never disagree on which cache to use), and
// MISE_YES=1 to suppress any interactive confirmation prompt in a
// non-interactive dispatcher context.
func miseEnv() []string {
	env := []string{"MISE_YES=1"}
	// Proxy and CA-trust vars must reach mise/uv or cold toolchain downloads
	// fail behind corporate proxies (SSL_CA_CERT_PATH / HTTP_PROXY setups).
	// The v != "" guard also drops set-but-empty values, which mise treats as
	// an explicit empty CA override and fails on (see entrypoint.sh).
	for _, k := range []string{"PATH", "HOME",
		"SSL_CERT_FILE", "SSL_CERT_DIR",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	if dir := MiseDataDir(); dir != "" {
		env = append(env, "MISE_DATA_DIR="+dir)
	}
	if dir := UvCacheDir(); dir != "" {
		env = append(env, "UV_CACHE_DIR="+dir)
	}
	return env
}

// Install runs `mise install <id>@<version>...` for every pin, installing
// (or confirming already-cached) each toolchain. Returns the tail of mise's
// combined output on failure, for surfacing in a run's error message.
func Install(ctx context.Context, pins []Pin) error {
	if len(pins) == 0 {
		return nil
	}

	args := []string{"install"}
	for _, p := range pins {
		args = append(args, fmt.Sprintf("%s@%s", p.ID, p.Version))
	}

	ctx, cancel := context.WithTimeout(ctx, InstallTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "mise", args...)
	cmd.Env = miseEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mise install failed: %w: %s", err, tail(out, outputTailBytes))
	}
	return nil
}

// pyvenvVersionPattern matches pyvenv.cfg's interpreter-version line, for
// detecting whether an existing .venv already matches the current python pin
// (see venvMatchesVersion). Different uv versions have written this under
// different keys — `version = X.Y.Z` (older uv, mirroring CPython's own venv
// module) and `version_info = X.Y.Z` (newer uv) have both been observed —
// so this matches either.
var pyvenvVersionPattern = regexp.MustCompile(`(?m)^version(?:_info)?\s*=\s*(\S+)`)

// EnsureVenv creates worktreeDir/.venv via `uv venv --python <pythonPath>`
// for a python pin. If a .venv already exists, its recorded interpreter
// version (pyvenv.cfg's `version =` line) is compared against pinVersion: a
// match reuses the existing venv (skips the uv call — a re-run on the same
// worktree, e.g. a feedback loop, is fast); a mismatch (or an unreadable/
// missing pyvenv.cfg) removes the stale venv and recreates it from the
// pinned interpreter, so bumping the repo's python pin can never silently
// leave a re-run on the old interpreter.
func EnsureVenv(ctx context.Context, worktreeDir, pythonPath, pinVersion string) error {
	venvDir := filepath.Join(worktreeDir, ".venv")
	if fi, err := os.Stat(venvDir); err == nil && fi.IsDir() {
		if venvMatchesVersion(venvDir, pinVersion) {
			return nil
		}
		if err := os.RemoveAll(venvDir); err != nil {
			return fmt.Errorf("remove stale .venv (python pin changed): %w", err)
		}
	}

	// Keep .venv out of `git status`/`git add -A` in this worktree — see
	// excludeVenv's doc comment. Best-effort: a failure here must not block
	// venv creation (worst case the venv shows up as untracked, same as
	// before this fix).
	excludeVenv(worktreeDir)

	ctx, cancel := context.WithTimeout(ctx, InstallTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "uv", "venv", "--python", pythonPath, venvDir)
	cmd.Env = miseEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uv venv failed: %w: %s", err, tail(out, outputTailBytes))
	}
	return nil
}

// venvMatchesVersion reports whether venvDir/pyvenv.cfg records the given
// interpreter version. Missing/unreadable/unparseable pyvenv.cfg is treated
// as a mismatch (recreate) rather than a match (reuse) — fail closed toward
// the correct toolchain rather than silently keeping a possibly-stale one.
func venvMatchesVersion(venvDir, pinVersion string) bool {
	data, err := os.ReadFile(filepath.Join(venvDir, "pyvenv.cfg"))
	if err != nil {
		return false
	}
	m := pyvenvVersionPattern.FindSubmatch(data)
	if m == nil {
		return false
	}
	got := strings.TrimSpace(string(m[1]))
	// pyvenv.cfg records the resolved interpreter's full version
	// (e.g. "3.11.7"), which may be more specific than the repo's pin
	// (e.g. "3.11"). A match on either the exact string or as a prefix
	// (pin is a version-family prefix of the resolved version) counts as
	// "still the pinned toolchain" — only a genuine change (a different
	// major.minor, or an unrelated string) triggers a recreate.
	return got == pinVersion || strings.HasPrefix(got, pinVersion+".")
}

// excludeVenv appends ".venv/" to the git exclude file that actually governs
// `git status`/`git add -A` for worktreeDir. For a linked worktree (the
// normal case — every task worktree is one), that is the *common* repo's
// info/exclude ($GIT_COMMON_DIR/info/exclude), NOT a per-worktree
// info/exclude under .git/worktrees/<id>/info/ — verified against a real
// linked worktree: unlike per-worktree files (HEAD, index, ...), info/
// exclude is not one of the files git links per-worktree, so `git rev-parse
// --git-path info/exclude` resolves through the worktree's commondir link to
// the main repo's .git/info/exclude, and that is the file git status
// actually reads; writing to a hand-built .git/worktrees/<id>/info/exclude
// path (which does exist on disk) has no effect. Asking git for the path
// (rather than hardcoding one) is what worktree.go's excludeWorktreeDir does
// too, just via a literal filepath.Join since the main-repo path is already
// known there; this package only ever has worktreeDir, so it asks git
// instead. Best-effort: any failure (git not on PATH, unreadable/unwritable
// file) leaves the venv visible to git status rather than blocking venv
// creation.
func excludeVenv(worktreeDir string) {
	out, err := exec.Command("git", "-C", worktreeDir, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude").Output()
	if err != nil {
		return
	}
	excludePath := strings.TrimSpace(string(out))
	if excludePath == "" {
		return
	}
	if data, rerr := os.ReadFile(excludePath); rerr == nil && strings.Contains(string(data), ".venv/") {
		return
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString("\n.venv/\n")
}

// ResolvePythonPath shells out to `mise where python@<version>` to find the
// filesystem path of the mise-installed python interpreter, for EnsureVenv's
// `uv venv --python`.
func ResolvePythonPath(ctx context.Context, version string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, InstallTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "mise", "where", "python@"+version)
	cmd.Env = miseEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("mise where python@%s failed: %w", version, err)
	}
	dir := strings.TrimSpace(string(out))
	return filepath.Join(dir, "bin", "python"), nil
}

// Prep installs every pin via mise, then — if a python pin is present —
// resolves the mise-installed interpreter and creates worktreeDir/.venv from
// it. This is the single entry point both the dispatcher's per-run prep step
// and the repo-save pre-warm goroutine call, so the two never drift.
// worktreeDir may be empty when pre-warming (no task worktree yet exists) —
// EnsureVenv is skipped in that case, since a repo-level pre-warm only needs
// to hit mise's install cache, not create a venv nobody will use yet.
func Prep(ctx context.Context, pins []Pin, worktreeDir string) error {
	if err := Install(ctx, pins); err != nil {
		return err
	}

	for _, p := range pins {
		if p.ID != "python" {
			continue
		}
		if worktreeDir == "" {
			return nil
		}
		pythonPath, err := ResolvePythonPath(ctx, p.Version)
		if err != nil {
			return err
		}
		return EnsureVenv(ctx, worktreeDir, pythonPath, p.Version)
	}
	return nil
}

// tail returns at most the last n bytes of b, trimmed of surrounding
// whitespace.
func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.TrimSpace(string(bytes.TrimSpace(b)))
}
