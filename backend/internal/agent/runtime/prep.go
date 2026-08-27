package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// miseEnv returns the environment for mise subprocess calls: the current
// process's PATH and HOME (so mise resolves its own binary and finds its
// data dir under $HOME), plus MISE_YES=1 to suppress any interactive
// confirmation prompt in a non-interactive dispatcher context.
func miseEnv() []string {
	env := []string{"MISE_YES=1"}
	for _, k := range []string{"PATH", "HOME", "MISE_DATA_DIR"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
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

// EnsureVenv creates worktreeDir/.venv via `uv venv --python <pythonPath>`
// for a python pin, skipping the uv call entirely if .venv already exists
// (a re-run on the same worktree, e.g. a feedback loop, reuses it). pythonPath
// is the interpreter mise installed for the pinned version (see
// ResolvePythonPath).
func EnsureVenv(ctx context.Context, worktreeDir, pythonPath string) error {
	venvDir := filepath.Join(worktreeDir, ".venv")
	if fi, err := os.Stat(venvDir); err == nil && fi.IsDir() {
		return nil
	}

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
		return EnsureVenv(ctx, worktreeDir, pythonPath)
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
