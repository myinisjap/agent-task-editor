package providers

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
