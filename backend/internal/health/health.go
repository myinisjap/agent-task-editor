// Package health produces provider/onboarding health checks for the
// GET /api/v1/health/providers endpoint. Each check reports whether a piece of
// the agent runtime (a CLI binary, an API key, an auth credential, a config
// value) is ready, along with a one-line hint for how to fix it when it isn't.
//
// The checks are deliberately cheap and side-effect free: binary presence via
// PATH lookup, credential/config-file existence, and env/config values. No real
// agent invocation is performed (that could cost money or hang), so results are
// a best-effort readiness signal rather than a guaranteed live probe.
//
// The one exception is updateCheck, which shells out to `gh` to look up the
// latest GitHub release tag. It is opt-in only (Input.CheckForUpdates, gated
// by the operator-controlled UPDATE_CHECK_ENABLED setting) so the endpoint
// never phones home by default, and it is bounded by a short timeout and
// degrades to StatusWarn (never StatusError) on any failure so an offline
// deployment doesn't look "broken".
package health

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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

	// DBSizeBytes/AgentLogsCount are live values read by the caller (this
	// package never touches the filesystem or DB) used only to render the
	// db_size check below, so bloat is observable on the Health page before
	// it becomes a problem (see internal/logretention for the opt-in pruner
	// that keeps this number bounded).
	DBSizeBytes    int64
	AgentLogsCount int64

	// Version is the running build's version string (e.g. "dev" for local
	// builds, or a release tag like "v1.4.0" for GHCR images stamped via
	// -ldflags at build time). Used only to render the version check below.
	Version string

	// CheckForUpdates, when true, opts into an additional check that shells
	// out to `gh` to compare Version against the latest GitHub release tag
	// (see Deps.LatestGitHubRelease). Disabled by default so the health
	// endpoint never "phones home" without the operator explicitly enabling
	// it (UPDATE_CHECK_ENABLED). Best-effort: network/gh failures degrade to
	// StatusWarn, never StatusError.
	CheckForUpdates bool
}

// Deps are the environment interactions the checks depend on, injectable so the
// logic can be unit-tested without touching the real filesystem, PATH, or gh.
type Deps struct {
	LookPath     func(string) (string, error)
	FileExists   func(string) bool
	Getenv       func(string) string
	HomeDir      func() (string, error)
	GHAuthStatus func() (bool, string)

	// LatestGitHubRelease returns the latest published release's tag name
	// (e.g. "v1.4.0") and whether the lookup succeeded. Only invoked when
	// Input.CheckForUpdates is true. The default implementation shells out to
	// `gh release view` with a short timeout so an offline/hung `gh` can't
	// stall the health-providers endpoint.
	LatestGitHubRelease func() (tag string, ok bool)
}

// DefaultDeps wires the real environment implementations.
func DefaultDeps() Deps {
	return Deps{
		LookPath: exec.LookPath,
		FileExists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		Getenv:              os.Getenv,
		HomeDir:             os.UserHomeDir,
		GHAuthStatus:        ghclient.GHAuthStatus,
		LatestGitHubRelease: latestGitHubReleaseViaGH,
	}
}

// latestGitHubReleaseRepo is the repo whose releases are checked for updates.
const latestGitHubReleaseRepo = "myinisjap/agent-task-editor"

// latestGitHubReleaseViaGH shells out to `gh release view` to fetch the
// latest release tag for latestGitHubReleaseRepo, bounded by a short timeout
// so an offline/hung gh invocation can't stall the caller.
func latestGitHubReleaseViaGH() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "release", "view",
		"--repo", latestGitHubReleaseRepo,
		"--json", "tagName",
		"-q", ".tagName",
	).Output()
	if err != nil {
		return "", false
	}
	tag := strings.TrimSpace(string(out))
	if tag == "" {
		return "", false
	}
	return tag, true
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
	checks = append(checks, dbSizeCheck(in))
	checks = append(checks, versionCheck(in))
	if in.CheckForUpdates {
		checks = append(checks, updateCheck(in, deps))
	}

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

// dbSizeCheck is purely informational: it surfaces the SQLite file size and
// the agent_logs row count so bloat is visible before it slows down backups
// or log-list queries (see internal/logretention for the opt-in pruner).
// Never StatusError - a large DB isn't a failure state on its own. Status is
// StatusWarn only when the size couldn't be read at all (e.g. DBSizeBytes is
// 0 because the caller's os.Stat failed), so the row still communicates
// something is off instead of silently showing "0 B".
func dbSizeCheck(in Input) Check {
	c := Check{ID: "db_size", Name: "Database size"}
	if in.DBSizeBytes <= 0 {
		c.Status = StatusWarn
		c.Detail = "could not read database file size"
		return c
	}
	c.Status = StatusOK
	c.Detail = fmt.Sprintf("%s, %s agent_logs rows", formatBytes(in.DBSizeBytes), formatCount(in.AgentLogsCount))
	return c
}

// versionCheck is purely informational: it surfaces the running build's
// version so operators can tell at a glance which image/commit is deployed.
// Always StatusOK — an unstamped "dev" build isn't a failure state, just the
// expected result of a local (non-release) build.
func versionCheck(in Input) Check {
	c := Check{ID: "version", Name: "Version", Status: StatusOK}
	v := in.Version
	if v == "" {
		v = "dev"
	}
	if v == "dev" {
		c.Detail = "running dev (unreleased build)"
	} else {
		c.Detail = "running " + v
	}
	return c
}

// updateCheck compares the running version against the latest published
// GitHub release tag. Only called when Input.CheckForUpdates is true (see
// Checks). Never returns StatusError — network/gh failures degrade to
// StatusWarn so a self-hosted, offline deployment doesn't show a false
// "broken" state just because it can't reach GitHub.
func updateCheck(in Input, d Deps) Check {
	c := Check{ID: "update_check", Name: "Update available"}
	if d.LatestGitHubRelease == nil {
		c.Status = StatusWarn
		c.Detail = "could not check for updates (offline or gh unavailable)"
		return c
	}
	latest, ok := d.LatestGitHubRelease()
	if !ok || latest == "" {
		c.Status = StatusWarn
		c.Detail = "could not check for updates (offline or gh unavailable)"
		return c
	}

	current := in.Version
	if current == "" || current == "dev" {
		// A dev build has no meaningful version to compare — just surface the
		// latest release as informational rather than claiming "up to date"
		// or "update available".
		c.Status = StatusWarn
		c.Detail = "running a dev build; latest release is " + latest
		c.Hint = "https://github.com/" + latestGitHubReleaseRepo + "/releases"
		return c
	}

	if semverCompare(current, latest) < 0 {
		c.Status = StatusWarn
		c.Detail = "update available: " + latest + " (running " + current + ")"
		c.Hint = "https://github.com/" + latestGitHubReleaseRepo + "/releases"
		return c
	}

	c.Status = StatusOK
	c.Detail = "up to date (" + current + ")"
	return c
}

// semverCompare does a minimal semver-ish comparison of two version strings,
// each optionally prefixed with "v" (e.g. "v1.4.0", "1.4.0"). Returns -1 if a
// < b, 0 if equal, 1 if a > b. Non-numeric/malformed segments compare as 0
// (equal) rather than erroring, since this only feeds a best-effort,
// never-fatal health check.
func semverCompare(a, b string) int {
	pa := semverParts(a)
	pb := semverParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// semverParts parses "vX.Y.Z" (or "X.Y.Z", with any non-numeric suffix like
// "-rc1" ignored) into [X, Y, Z], defaulting missing/unparseable segments to 0.
func semverParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Drop any pre-release/build metadata suffix (e.g. "1.4.0-rc1+build").
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	segs := strings.SplitN(v, ".", 3)
	var out [3]int
	for i := 0; i < len(segs) && i < 3; i++ {
		if n, err := strconv.Atoi(segs[i]); err == nil {
			out[i] = n
		}
	}
	return out
}

// formatBytes renders n as a human-readable size using binary (1024) units,
// e.g. "42.3 MB". No existing helper for this in the codebase.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}

// formatCount renders n with thousands separators, e.g. "118,204".
func formatCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
