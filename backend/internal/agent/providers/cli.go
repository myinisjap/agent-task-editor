package providers

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

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

	"HTTP_PROXY":  true,
	"HTTPS_PROXY": true,
	"NO_PROXY":    true,
	"http_proxy":  true,
	"https_proxy": true,
	"no_proxy":    true,
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
