package handlers

import (
	"net/http"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/health"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// HealthHandler serves provider/onboarding readiness checks.
type HealthHandler struct {
	q               *gen.Queries
	db              *storage.DB
	mcpBinary       string
	repoBaseDir     string
	llmBaseURL      string
	llmAPIKey       string
	backupDir       string
	backupInterval  time.Duration
	backupKeep      int
	version         string
	checkForUpdates bool
}

// NewHealthHandler constructs a HealthHandler from the relevant server config.
// db is used only to read the on-disk database file size for the dbSizeCheck
// (informational; see internal/health.dbSizeCheck). version is the running
// build's version string (see cmd/server's ldflags-stamped Version var);
// checkForUpdates opts into the best-effort "update available" check (see
// internal/health.updateCheck), gated by UPDATE_CHECK_ENABLED so the health
// endpoint never phones home by default.
func NewHealthHandler(q *gen.Queries, db *storage.DB, mcpBinary, repoBaseDir, llmBaseURL, llmAPIKey, backupDir string, backupInterval time.Duration, backupKeep int, version string, checkForUpdates bool) *HealthHandler {
	return &HealthHandler{
		q:               q,
		db:              db,
		mcpBinary:       mcpBinary,
		repoBaseDir:     repoBaseDir,
		llmBaseURL:      llmBaseURL,
		llmAPIKey:       llmAPIKey,
		backupDir:       backupDir,
		backupInterval:  backupInterval,
		backupKeep:      backupKeep,
		version:         version,
		checkForUpdates: checkForUpdates,
	}
}

// Providers reports the readiness of each agent provider and supporting piece
// of infrastructure (claude/qwen/opencode binaries, API keys, MCP sidecar, gh
// auth, REPO_BASE_DIR). Provider-specific checks are only emitted for
// providers actually referenced by an *enabled* agent config or by a chat
// session (via their Provider Config) — not every Provider Config that
// happens to exist — so a disabled agent config or an unused Provider Config
// doesn't produce a noisy false-positive readiness warning (e.g. a missing
// API key for a provider nothing currently runs).
//
// GET /api/v1/health/providers
func (h *HealthHandler) Providers(w http.ResponseWriter, r *http.Request) {
	providers := map[string]bool{}
	if names, err := h.q.ListInUseProviders(r.Context()); err == nil {
		for _, p := range names {
			providers[p] = true
		}
	}

	// DB size + agent_logs row count require a live read, so they're gathered
	// here (best-effort; a failure to stat the file just yields 0, surfaced
	// as a warn by dbSizeCheck) rather than inside health.Checks itself.
	var dbSize int64
	if h.db != nil {
		if sz, err := h.db.Size(); err == nil {
			dbSize = sz
		}
	}
	var logCount int64
	if n, err := h.q.CountAgentLogsTotal(r.Context()); err == nil {
		logCount = n
	}

	// Prefer the DB-backed settings (editable at runtime via
	// PUT /api/v1/backup/settings) over the deploy-time config defaults, so
	// this check reflects what the scheduler will actually do on its next
	// run rather than going stale after a settings change. Falls back to the
	// config defaults if no settings row exists yet or the read fails.
	backupInterval, backupKeep := h.backupInterval, h.backupKeep
	if row, err := h.q.GetBackupSettings(r.Context()); err == nil {
		backupInterval = time.Duration(row.IntervalSeconds) * time.Second
		backupKeep = int(row.Keep)
	}

	checks := health.Checks(health.Input{
		MCPBinary:       h.mcpBinary,
		RepoBaseDir:     h.repoBaseDir,
		LLMBaseURL:      h.llmBaseURL,
		LLMAPIKey:       h.llmAPIKey,
		Providers:       providers,
		BackupDir:       h.backupDir,
		BackupInterval:  backupInterval,
		BackupKeep:      backupKeep,
		DBSizeBytes:     dbSize,
		AgentLogsCount:  logCount,
		Version:         h.version,
		CheckForUpdates: h.checkForUpdates,
	}, nil)

	JSON(w, http.StatusOK, map[string]any{"checks": checks})
}
