package providers

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
)

// sanitizeArgs strips NUL bytes from each CLI argument. A NUL byte anywhere
// in an argv element makes the Linux execve syscall fail with EINVAL
// ("fork/exec ...: invalid argument"), so any NUL that leaks in from
// task/prompt content (e.g. an agent or human writing a literal \x00 into
// task notes, a description, or synced issue comments) would make every
// launch fail before the CLI ever starts — with no logs, silently. Stripping
// is always safe: a NUL can never legally appear in a process argument.
func sanitizeArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.IndexByte(a, 0) >= 0 {
			a = strings.ReplaceAll(a, "\x00", "")
		}
		out[i] = a
	}
	return out
}

// dangerousEnvKeys blocks user-supplied agent env vars from hijacking process execution.
var dangerousEnvKeys = map[string]bool{
	"PATH": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true,
	"HOME": true, "SHELL": true, "IFS": true,
	"DYLD_INSERT_LIBRARIES": true, "DYLD_LIBRARY_PATH": true,
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := make([]string, len(base))
	copy(out, base)
	for k, v := range extra {
		if dangerousEnvKeys[strings.ToUpper(k)] {
			slog.Warn("agent env: blocked dangerous key", "key", k)
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

// commonBaseEnvKeys are the env vars every CLI provider subprocess needs
// regardless of which LLM backend it talks to: PATH so the subprocess can
// resolve its own binary (all providers' binary() return a bare name
// resolved via PATH), HOME so the CLI can find its credentials/config
// (~/.claude, ~/.codex, ~/.qwen, opencode's XDG/HOME-based auth store), and
// a handful of locale/proxy/TLS-trust vars that CLIs or their Node/Python
// runtimes commonly consult. This is intentionally NOT os.Environ() — see
// #321: the full backend process environment (including LLM_API_KEY,
// API_TOKEN, DB credentials, etc.) must never reach an agent subprocess.
var commonBaseEnvKeys = map[string]bool{
	"PATH": true,
	"HOME": true,

	"USER":    true,
	"LOGNAME": true,

	"LANG":     true,
	"LC_ALL":   true,
	"LC_CTYPE": true,
	"TERM":     true,
	"TMPDIR":   true,

	"XDG_CONFIG_HOME": true,
	"XDG_DATA_HOME":   true,
	"XDG_CACHE_HOME":  true,
	"XDG_STATE_HOME":  true,

	"SSL_CERT_FILE":       true,
	"SSL_CERT_DIR":        true,
	"NODE_EXTRA_CA_CERTS": true,
	"GIT_SSL_CAINFO":      true,

	// INSECURE_SKIP_SSL_VERIFY's computed vars (see docker-compose.yml) —
	// without these, a corporate-proxy user who opts into the bypass gets it
	// for the backend's own git/npm calls but not for agent subprocesses,
	// which then fail TLS verification against the same proxy.
	"GIT_SSL_NO_VERIFY":            true,
	"NPM_CONFIG_STRICT_SSL":        true,
	"NODE_TLS_REJECT_UNAUTHORIZED": true,

	"HTTP_PROXY":  true,
	"HTTPS_PROXY": true,
	"NO_PROXY":    true,
	"http_proxy":  true,
	"https_proxy": true,
	"no_proxy":    true,

	// GITHUB_TOKEN / GH_TOKEN authenticate the agent's own git operations
	// (git push, gh) against GitHub over HTTPS: the container wires git's
	// credential.helper to `gh auth git-credential`, which reads the token
	// from the environment. Without these in the allowlist, an agent that
	// commits its work can never push it — git fails with "could not read
	// Username for 'https://github.com'" — even though the backend's own
	// post-run PushBranch (which runs with the full os.Environ()) succeeds,
	// masking the gap. Unlike the secrets #321 deliberately withholds
	// (API_TOKEN, LLM_API_KEY, DB creds), this is a credential the agent is
	// meant to use, so it belongs in the allowlist.
	"GITHUB_TOKEN": true,
	"GH_TOKEN":     true,
}

// withCommonBase returns a new allowlist set containing commonBaseEnvKeys
// plus the given provider-specific extra keys.
func withCommonBase(extra ...string) map[string]bool {
	out := make(map[string]bool, len(commonBaseEnvKeys)+len(extra))
	for k := range commonBaseEnvKeys {
		out[k] = true
	}
	for _, k := range extra {
		out[k] = true
	}
	return out
}

// claudeEnvAllowlist is the set of env var names passed through from the
// backend's own environment to the claude CLI subprocess, in addition to
// operator-supplied AgentConfig.Env. ANTHROPIC_AUTH_TOKEN is also injected
// explicitly at run time from ~/.claude/.credentials.json (see claude.go);
// keeping it here too lets an operator-set value work the same way.
var claudeEnvAllowlist = withCommonBase(
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_MODEL",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"DISABLE_TELEMETRY",
	"DISABLE_AUTOUPDATER",
)

// codexEnvAllowlist is the set of env var names passed through from the
// backend's own environment to the codex CLI subprocess, in addition to
// operator-supplied AgentConfig.Env. CODEX_HOME is also injected explicitly
// per-run (see codex.go).
var codexEnvAllowlist = withCommonBase(
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"OPENAI_ORG",
	"OPENAI_ORGANIZATION",
	"CODEX_HOME",
)

// qwenEnvAllowlist is the set of env var names passed through from the
// backend's own environment to the qwen CLI subprocess, in addition to
// operator-supplied AgentConfig.Env. QWEN_CODE_SUPPRESS_YOLO_WARNING is also
// injected explicitly at run time (see qwen.go).
var qwenEnvAllowlist = withCommonBase(
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"OPENAI_MODEL",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"QWEN_CODE_SUPPRESS_YOLO_WARNING",
)

// opencodeEnvAllowlist is the set of env var names passed through from the
// backend's own environment to the opencode CLI subprocess, in addition to
// operator-supplied AgentConfig.Env. Opencode manages its own auth store
// under HOME/XDG dirs (already in commonBaseEnvKeys); these keys cover the
// common provider API keys opencode's own config can reference directly.
var opencodeEnvAllowlist = withCommonBase(
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
)

// allowlistEnv returns the subset of os.Environ() whose keys (the part
// before '=') are present in allow. Used to build the base env for CLI agent
// subprocesses instead of passing the full backend process environment
// through — see #321.
func allowlistEnv(allow map[string]bool) []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if allow[k] {
			out = append(out, kv)
		}
	}
	return out
}

// providerEnvAllowlists maps each CLI provider string to its allowlist, for
// callers (e.g. the interactive terminal manager, which launches the same
// CLI binaries as the headless runners) that only have the provider string,
// not a *Runner. An unrecognized provider gets commonBaseEnvKeys only, which
// is a safe default (PATH/HOME + locale/proxy/TLS vars, no API keys) rather
// than falling back to no allowlist (which would either panic or, worse,
// silently pass everything).
var providerEnvAllowlists = map[string]map[string]bool{
	"claude":    claudeEnvAllowlist,
	"codex_cli": codexEnvAllowlist,
	"qwen_code": qwenEnvAllowlist,
	"opencode":  opencodeEnvAllowlist,
}

// EnvAllowlistFor returns the allowlisted subset of the backend's own
// environment for the given provider string — the same allowlist the
// headless CLI runners (ClaudeRunner, CodexRunner, QwenRunner,
// OpencodeRunner) use, exposed for other callers that launch the same CLI
// binaries (currently the interactive chat terminal; see
// agent.TerminalManager.EnvAllowlist in cmd/server's wiring). Never returns
// the full os.Environ() — an unrecognized provider string falls back to
// commonBaseEnvKeys only (PATH/HOME/locale/proxy/TLS vars; no provider API
// keys), never to "no filtering at all".
func EnvAllowlistFor(provider string) []string {
	allow, ok := providerEnvAllowlists[provider]
	if !ok {
		allow = commonBaseEnvKeys
	}
	return allowlistEnv(allow)
}

// rawDump is a dev-only tee of raw stdout stream-json lines, gated by the
// AGENT_RAW_LOG_DIR env var. When unset, all methods are no-ops on a nil
// receiver so the hot path stays clean. Used to review provider output and
// improve our stream parsing — not a product feature.
type rawDump struct {
	f *os.File
}

func openRawDump(runID string) *rawDump {
	dir := os.Getenv("AGENT_RAW_LOG_DIR")
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("raw log capture: mkdir failed", "dir", dir, "err", err)
		return nil
	}
	f, err := os.Create(filepath.Join(dir, runID+".jsonl"))
	if err != nil {
		slog.Warn("raw log capture: create failed", "err", err)
		return nil
	}
	return &rawDump{f: f}
}

func (d *rawDump) WriteLine(line string) {
	if d == nil {
		return
	}
	_, _ = d.f.WriteString(line + "\n")
}

func (d *rawDump) Close() {
	if d == nil {
		return
	}
	_ = d.f.Close()
}

// applyRuntime wraps a provider's spawn command with `mise x` when the task's
// repo has toolchain pins configured (agent.RuntimeSpec, set by the
// dispatcher's runtime-prep step). A nil or pin-less spec returns binary,
// args, and env completely unchanged — this is the §4.1 byte-identical
// guarantee: a repo with no runtime_languages configured must spawn exactly
// as it did before this feature existed.
//
// When pins are present, the returned command is
// `mise x <id>@<version>... -- <binary> <args...>`, one pin per language in
// the order given (RuntimeSpec.Pins is built from ParsePins' JSON-array
// order, so this is deterministic for a given repo config). python is
// deliberately never passed to `mise x`: mise x's PATH prepend for python
// would shadow the per-worktree venv runtime prep creates instead (see
// dispatcher's runtime-prep step) — so a python pin is skipped in the mise
// x args, and env instead gets `<worktree>/.venv/bin` prepended to PATH
// (so `python`/`pip` resolve to the venv's wrapped interpreter) plus
// UV_CACHE_DIR pointing at the shared uv cache.
//
// A node pin gets special handling too, for a different reason than python:
// `mise x node@<pin> -- <binary> ...` puts the pinned node first on PATH for
// the whole child process tree — and claude/qwen's CLI binaries are
// themselves node scripts (`#!/usr/bin/env node`), so the CLI process itself
// would run (and likely crash) on the pinned node instead of the image's own
// bundled one. codex (Rust) and opencode (Go) are native binaries and
// unaffected. So: when a node pin is present and binary resolves to a node
// script (see isNodeScript), the spawn becomes
// `mise x <pins> -- <systemNode> <absoluteBinaryPath> <args...>` — the CLI
// process itself runs explicitly on the backend's own bundled node (resolved
// via exec.LookPath BEFORE any PATH manipulation below), while any node/npm/
// npx the agent's Bash tool invokes still resolves through mise x's PATH to
// the pinned version. Fails closed: if a node pin is present, binary is a
// node script, but the system node or the binary's own absolute path can't
// be resolved, this returns an error rather than ever launching the CLI on
// the wrong interpreter — callers must propagate it as a spawn failure
// (never fall back to the unwrapped command).
//
// The child's env is built via allowlistEnv (see EnvAllowlistFor), which
// strips the backend's own ENV — including MISE_DATA_DIR, so without
// injecting it here `mise x` could resolve a *different* data dir than the
// one runtime.Prep just installed into — and there is no allowlist entry for
// MISE_YES/MISE_TRUSTED_CONFIG_PATHS at all, so a repo that ships its own
// mise.toml would otherwise hit mise's untrusted-config confirmation prompt
// in this non-TTY child and fail outright. All three are always injected
// whenever pins are present (not just for a python pin), since any pinned
// repo can ship a mise.toml.
func applyRuntime(spec *agent.RuntimeSpec, binary string, args []string, env []string) (string, []string, []string, error) {
	if spec == nil || len(spec.Pins) == 0 {
		return binary, args, env, nil
	}

	// No computed capacity: len arithmetic in an allocation trips CodeQL's
	// go/allocation-size-overflow, and the slice is tiny (≤6 pins) anyway.
	miseArgs := []string{"x"}
	hasPython, hasNode := false, false
	for _, p := range spec.Pins {
		switch p.ID {
		case "python":
			hasPython = true
			continue
		case "node":
			hasNode = true
		}
		miseArgs = append(miseArgs, fmt.Sprintf("%s@%s", p.ID, p.Version))
	}

	// A node pin whose CLI is a node script must run explicitly on the
	// system's own node — see the doc comment above.
	miseArgs = append(miseArgs, "--")
	if hasNode {
		nodePath, binPath, err := explicitInterpreterForNodeScript(binary)
		if err != nil {
			return "", nil, nil, fmt.Errorf("resolve system interpreter for node-pinned CLI %q: %w", binary, err)
		}
		if nodePath != "" {
			miseArgs = append(miseArgs, nodePath, binPath)
		} else {
			miseArgs = append(miseArgs, binary)
		}
	} else {
		miseArgs = append(miseArgs, binary)
	}
	miseArgs = append(miseArgs, args...)

	newEnv := append([]string(nil), env...)
	newEnv = append(newEnv, "MISE_YES=1")
	if dir := runtime.MiseDataDir(); dir != "" {
		newEnv = append(newEnv, "MISE_DATA_DIR="+dir)
	}
	if spec.WorktreeDir != "" {
		newEnv = append(newEnv, "MISE_TRUSTED_CONFIG_PATHS="+spec.WorktreeDir)
	}
	if hasPython && spec.WorktreeDir != "" {
		venvBin := filepath.Join(spec.WorktreeDir, ".venv", "bin")
		newEnv = prependPath(newEnv, venvBin)
		if dir := runtime.UvCacheDir(); dir != "" {
			newEnv = append(newEnv, "UV_CACHE_DIR="+dir)
		}
	}

	return "mise", miseArgs, newEnv, nil
}

// explicitInterpreterForNodeScript checks whether binary resolves to a node
// script (see isNodeScript) and, if so, returns the two argv elements that
// must replace it so it runs on the system's own node rather than whatever
// mise x puts first on PATH: the system node's absolute path (nodePath), and
// the script's own absolute path (binPath) — the caller places them as
// consecutive argv elements: `<nodePath> <binPath> <args...>`. Returns
// nodePath == "" (with a nil error) — not an error — for a native binary
// (codex, opencode): those spawn unwrapped, as before. Both exec.LookPath
// calls resolve against the current process's own PATH/env, which the caller
// (applyRuntime) invokes before building the mise-x-wrapped child env, so
// this always resolves the image's bundled node/binary, never a pinned one.
func explicitInterpreterForNodeScript(binary string) (nodePath, binPath string, err error) {
	binPath, err = exec.LookPath(binary)
	if err != nil {
		return "", "", fmt.Errorf("resolve %q: %w", binary, err)
	}
	isScript, err := isNodeScript(binPath)
	if err != nil {
		return "", "", fmt.Errorf("inspect %q: %w", binPath, err)
	}
	if !isScript {
		return "", "", nil
	}
	nodePath, err = exec.LookPath("node")
	if err != nil {
		return "", "", fmt.Errorf("resolve system node: %w", err)
	}
	return nodePath, binPath, nil
}

// isNodeScript reports whether the file at path is a node script — detected
// by its shebang line, not assumed from the provider: reads the first line
// and checks it references node, either as `#!/usr/bin/env node` (npm's
// standard global-install wrapper, what claude/qwen use) or a shebang path
// ending in "/node" (e.g. `#!/usr/local/bin/node`). A native binary (codex's
// Rust build, opencode's Go build) has no text shebang at all — its first
// line is binary/ELF data — so this returns false for those, never true by
// assumption.
func isNodeScript(path string) (bool, error) {
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
	// `#!/usr/bin/env node [args...]` — env's own target is the second field.
	if filepath.Base(interp) == "env" && len(fields) > 1 {
		interp = fields[1]
	}
	return filepath.Base(interp) == "node", nil
}

// prependPath returns a copy of env with dir prepended to the PATH entry
// (added fresh if env has none). Providers build env via allowlistEnv, which
// always includes PATH (see commonBaseEnvKeys), so the "no PATH key found"
// branch is a defensive fallback, not the expected case.
func prependPath(env []string, dir string) []string {
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
