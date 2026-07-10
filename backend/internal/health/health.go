// Package health produces provider/onboarding health checks for the
// GET /api/v1/health/providers endpoint. Each check reports whether a piece of
// the agent runtime (a CLI binary, an API key, an auth credential, a config
// value) is ready, along with a one-line hint for how to fix it when it isn't.
//
// The checks are deliberately cheap and side-effect free: binary presence via
// PATH lookup, credential/config-file existence, and env/config values. No real
// agent invocation is performed (that could cost money or hang), so results are
// a best-effort readiness signal rather than a guaranteed live probe.
package health

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
)

// Status is the severity of a single check.
type Status string

const (
	// StatusOK — everything needed is present (green).
	StatusOK Status = "ok"
	// StatusWarn — an optional or heuristically-detected item is missing;
	// core agent runs may still work but a feature is degraded (yellow).
	StatusWarn Status = "warn"
	// StatusError — a required item is missing; runs using it will fail (red).
	StatusError Status = "error"
)

// Check is a single row on the provider health page.
type Check struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

// Input carries the server configuration and the set of providers actually
// referenced by enabled agent configs, so provider-specific checks are only
// emitted when they're relevant.
type Input struct {
	MCPBinary   string
	RepoBaseDir string
	LLMBaseURL  string
	LLMAPIKey   string
	// Providers is the set of provider strings used by enabled agent configs
	// (e.g. "claude", "anthropic", "llm", "qwen_code", "opencode", "gemini_cli",
	// "codex_cli").
	Providers map[string]bool

	// BackupDir/BackupInterval/BackupKeep mirror config.Config's automatic
	// local-backup scheduler settings, used only to render the auto_backup
	// check below — this package never touches the filesystem for backups.
	BackupDir      string
	BackupInterval time.Duration
	BackupKeep     int
}

// Deps are the environment interactions the checks depend on, injectable so the
// logic can be unit-tested without touching the real filesystem, PATH, or gh.
type Deps struct {
	LookPath     func(string) (string, error)
	FileExists   func(string) bool
	Getenv       func(string) string
	HomeDir      func() (string, error)
	GHAuthStatus func() (bool, string)
}

// DefaultDeps wires the real environment implementations.
func DefaultDeps() Deps {
	return Deps{
		LookPath: exec.LookPath,
		FileExists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		Getenv:       os.Getenv,
		HomeDir:      os.UserHomeDir,
		GHAuthStatus: ghclient.GHAuthStatus,
	}
}

// Checks runs all applicable health checks and returns them in a stable order.
// A nil Deps uses DefaultDeps.
func Checks(in Input, d *Deps) []Check {
	deps := DefaultDeps()
	if d != nil {
		deps = *d
	}

	var checks []Check

	checks = append(checks, claudeCheck(deps))

	if in.Providers["anthropic"] {
		checks = append(checks, anthropicCheck(in, deps))
	}
	if in.Providers["llm"] {
		checks = append(checks, llmCheck(in))
	}
	if in.Providers["qwen_code"] {
		checks = append(checks, binaryCheck(deps, "qwen", "Qwen CLI", "qwen_code provider",
			"Install the qwen CLI and ensure it's on the server's PATH."))
	}
	if in.Providers["opencode"] {
		checks = append(checks, binaryCheck(deps, "opencode", "opencode CLI", "opencode provider",
			"Install the opencode CLI and ensure it's on the server's PATH."))
	}
	if in.Providers["gemini_cli"] {
		checks = append(checks, geminiCheck(deps))
	}
	if in.Providers["codex_cli"] {
		checks = append(checks, codexCheck(deps))
	}

	checks = append(checks, mcpCheck(in, deps))
	checks = append(checks, ghCheck(deps))
	checks = append(checks, repoBaseDirCheck(in, deps))
	checks = append(checks, autoBackupCheck(in))

	return checks
}

// claudeCheck verifies the claude CLI is installed and appears authenticated.
// Authentication is detected heuristically (credential file or env var) rather
// than by invoking the CLI, so a green result means "credentials found", not a
// live token validation.
func claudeCheck(d Deps) Check {
	c := Check{ID: "claude_cli", Name: "Claude CLI"}
	if _, err := d.LookPath("claude"); err != nil {
		c.Status = StatusError
		c.Detail = "claude binary not found on PATH"
		c.Hint = "Install the Claude CLI (npm i -g @anthropic-ai/claude-code) so the claude provider can run."
		return c
	}
	if claudeAuthenticated(d) {
		c.Status = StatusOK
		c.Detail = "claude CLI installed and credentials found"
		return c
	}
	c.Status = StatusWarn
	c.Detail = "claude CLI installed but no credentials detected"
	c.Hint = "Run `claude` once to log in, or mount ~/.claude/.credentials.json / set ANTHROPIC_API_KEY. Runs may fail with \"Not logged in\"."
	return c
}

// claudeAuthenticated reports whether Claude credentials appear to be present:
// an ANTHROPIC_API_KEY env var, or a ~/.claude/.credentials.json file.
func claudeAuthenticated(d Deps) bool {
	if d.Getenv("ANTHROPIC_API_KEY") != "" {
		return true
	}
	if home, err := d.HomeDir(); err == nil {
		if d.FileExists(home + "/.claude/.credentials.json") {
			return true
		}
	}
	return false
}

// anthropicCheck verifies the direct Anthropic Messages API provider has a key.
func anthropicCheck(in Input, _ Deps) Check {
	c := Check{ID: "anthropic_api", Name: "Anthropic API key"}
	if in.LLMAPIKey == "" {
		c.Status = StatusError
		c.Detail = "LLM_API_KEY is not set"
		c.Hint = "Set LLM_API_KEY to an Anthropic API key; the anthropic provider bills per-token."
		return c
	}
	c.Status = StatusOK
	c.Detail = "LLM_API_KEY is set"
	return c
}

// llmCheck verifies the OpenAI-compatible provider has a key and base URL.
func llmCheck(in Input) Check {
	c := Check{ID: "llm_api", Name: "LLM API (OpenAI-compatible)"}
	if in.LLMAPIKey == "" {
		c.Status = StatusError
		c.Detail = "LLM_API_KEY is not set"
		c.Hint = "Set LLM_API_KEY for the llm provider's OpenAI-compatible endpoint."
		return c
	}
	if in.LLMBaseURL == "" {
		c.Status = StatusWarn
		c.Detail = "LLM_API_KEY set, but LLM_BASE_URL is empty"
		c.Hint = "Set LLM_BASE_URL to your provider's endpoint (defaults to https://api.openai.com/v1)."
		return c
	}
	c.Status = StatusOK
	c.Detail = "LLM_API_KEY set; base URL " + in.LLMBaseURL
	return c
}

// geminiCheck verifies the gemini CLI is installed and appears authenticated.
// Authentication is detected heuristically (env var or the OAuth cache file
// the CLI itself writes on `gemini` login) rather than by invoking the CLI.
func geminiCheck(d Deps) Check {
	c := Check{ID: "gemini_cli", Name: "Gemini CLI"}
	if _, err := d.LookPath("gemini"); err != nil {
		c.Status = StatusError
		c.Detail = "gemini binary not found on PATH"
		c.Hint = "Install the Gemini CLI (npm i -g @google/gemini-cli) so the gemini_cli provider can run."
		return c
	}
	if geminiAuthenticated(d) {
		c.Status = StatusOK
		c.Detail = "gemini CLI installed and credentials found"
		return c
	}
	c.Status = StatusWarn
	c.Detail = "gemini CLI installed but no credentials detected"
	c.Hint = "Run `gemini` once to log in with a Google account, or set GEMINI_API_KEY / GOOGLE_API_KEY. Runs may fail with an auth error."
	return c
}

// geminiAuthenticated reports whether Gemini CLI credentials appear to be
// present: a GEMINI_API_KEY/GOOGLE_API_KEY env var, or the OAuth credential
// cache the CLI writes to ~/.gemini/oauth_creds.json on `gemini` login.
func geminiAuthenticated(d Deps) bool {
	if d.Getenv("GEMINI_API_KEY") != "" || d.Getenv("GOOGLE_API_KEY") != "" {
		return true
	}
	if home, err := d.HomeDir(); err == nil {
		if d.FileExists(home + "/.gemini/oauth_creds.json") {
			return true
		}
	}
	return false
}

// codexCheck verifies the codex CLI is installed and appears authenticated.
// Authentication is detected heuristically (env var or the auth cache file
// the CLI itself writes on `codex login`) rather than by invoking the CLI.
func codexCheck(d Deps) Check {
	c := Check{ID: "codex_cli", Name: "Codex CLI"}
	if _, err := d.LookPath("codex"); err != nil {
		c.Status = StatusError
		c.Detail = "codex binary not found on PATH"
		c.Hint = "Install the Codex CLI (npm i -g @openai/codex) so the codex_cli provider can run."
		return c
	}
	if codexAuthenticated(d) {
		c.Status = StatusOK
		c.Detail = "codex CLI installed and credentials found"
		return c
	}
	c.Status = StatusWarn
	c.Detail = "codex CLI installed but no credentials detected"
	c.Hint = "Run `codex login` to sign in with ChatGPT, or set OPENAI_API_KEY. Runs may fail with a 401 auth error."
	return c
}

// codexAuthenticated reports whether Codex CLI credentials appear to be
// present: an OPENAI_API_KEY env var, or the auth cache file the CLI writes
// to ~/.codex/auth.json on `codex login`.
func codexAuthenticated(d Deps) bool {
	if d.Getenv("OPENAI_API_KEY") != "" {
		return true
	}
	if home, err := d.HomeDir(); err == nil {
		if d.FileExists(home + "/.codex/auth.json") {
			return true
		}
	}
	return false
}

// binaryCheck is the shared "is this CLI on PATH" check for qwen/opencode.
func binaryCheck(d Deps, bin, name, usedBy, hint string) Check {
	c := Check{ID: bin + "_cli", Name: name}
	if _, err := d.LookPath(bin); err != nil {
		c.Status = StatusError
		c.Detail = bin + " binary not found on PATH (required by " + usedBy + ")"
		c.Hint = hint
		return c
	}
	c.Status = StatusOK
	c.Detail = bin + " binary found on PATH"
	return c
}

// mcpCheck verifies the MCP sidecar binary is configured and present.
func mcpCheck(in Input, d Deps) Check {
	c := Check{ID: "mcp_sidecar", Name: "MCP sidecar"}
	if in.MCPBinary == "" {
		c.Status = StatusWarn
		c.Detail = "MCP_SERVER_PATH is not set"
		c.Hint = "Set MCP_SERVER_PATH to the mcp-server binary to enable signal_complete/request_human for claude/qwen agents."
		return c
	}
	if !d.FileExists(in.MCPBinary) {
		c.Status = StatusError
		c.Detail = "MCP_SERVER_PATH is set but the file does not exist: " + in.MCPBinary
		c.Hint = "Point MCP_SERVER_PATH at the built mcp-server binary."
		return c
	}
	c.Status = StatusOK
	c.Detail = "MCP sidecar configured: " + in.MCPBinary
	return c
}

// ghCheck folds in the existing gh auth status probe.
func ghCheck(d Deps) Check {
	c := Check{ID: "gh_auth", Name: "GitHub CLI auth"}
	authed, note := d.GHAuthStatus()
	if authed {
		c.Status = StatusOK
		c.Detail = "gh authenticated (" + note + ")"
		return c
	}
	c.Status = StatusWarn
	c.Detail = "gh credentials not found"
	c.Hint = "Mount ~/.config/gh or set GITHUB_TOKEN to enable PR creation and PR-state sync."
	return c
}

// repoBaseDirCheck surfaces the REPO_BASE_DIR sandbox setting (startup-only warning today).
func repoBaseDirCheck(in Input, d Deps) Check {
	c := Check{ID: "repo_base_dir", Name: "Repo base directory"}
	if in.RepoBaseDir == "" {
		c.Status = StatusWarn
		c.Detail = "REPO_BASE_DIR is not set — any host path may be registered as a repo"
		c.Hint = "Set REPO_BASE_DIR to restrict repo paths; recommended for production deployments."
		return c
	}
	if !d.FileExists(in.RepoBaseDir) {
		c.Status = StatusError
		c.Detail = "REPO_BASE_DIR is set but does not exist: " + in.RepoBaseDir
		c.Hint = "Create the directory or point REPO_BASE_DIR at an existing path."
		return c
	}
	c.Status = StatusOK
	c.Detail = "repo paths restricted to " + in.RepoBaseDir
	return c
}

// autoBackupCheck surfaces whether the built-in scheduled local-snapshot
// backup (BACKUP_DIR/BACKUP_INTERVAL/BACKUP_KEEP) is enabled. This is
// informational only — the on-demand GET /api/v1/backup endpoint and the
// Health page's "Download backup" button work regardless of this setting.
func autoBackupCheck(in Input) Check {
	c := Check{ID: "auto_backup", Name: "Automatic local backups"}
	if in.BackupDir == "" {
		c.Status = StatusWarn
		c.Detail = "BACKUP_DIR is not set — scheduled local snapshots are disabled"
		c.Hint = "Set BACKUP_DIR to enable scheduled snapshots; see docs/backup.md."
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("writing snapshots to %s every %s, keeping the newest %d", in.BackupDir, in.BackupInterval, in.BackupKeep)
	return c
}
