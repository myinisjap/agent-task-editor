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
