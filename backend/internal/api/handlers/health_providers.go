package handlers

import (
	"net/http"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/health"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// DispatcherLiveness reports when the dispatch loop last began a sweep tick,
// for the /readyz readiness probe (see HealthHandler.Readyz). Defined as a
// narrow interface — rather than importing internal/agent's *Dispatcher
// directly — to avoid a handlers→agent import (mirrors the RunCanceller /
// ReplyDispatcher pattern used by TasksHandler). *agent.Dispatcher satisfies
// this via its LastSweep method.
type DispatcherLiveness interface {
	LastSweep() time.Time
}

// GlobalCostReporter exposes the dispatcher's cached global daily/monthly
// spend-ceiling snapshot (see agent.Dispatcher.GlobalCostStatus), surfaced
// on both /readyz and GET /api/v1/health/providers — a tripped cap means the
// whole system has stopped dispatching new work while otherwise appearing
// healthy, so it belongs on the health surface, not just in logs. Split from
// DispatcherLiveness (rather than folded into it) so a caller/test double
// that only has the liveness half doesn't also need to implement this.
// *agent.Dispatcher satisfies this via its GlobalCostStatus method.
type GlobalCostReporter interface {
	GlobalCostStatus() agent.GlobalCostStatus
}

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
	dispatcher      DispatcherLiveness
	// globalCost supplies the global daily/monthly spend-ceiling snapshot
	// (see GlobalCostReporter). Populated from the same concrete
	// *agent.Dispatcher passed as dispatcher above; kept as a separate field
	// (rather than folding GlobalCostStatus into DispatcherLiveness) so a
	// dispatcher stand-in that only implements LastSweep (e.g. in tests)
	// doesn't also need a GlobalCostStatus method. May be nil, in which case
	// both Readyz and Providers omit the global_cost block entirely.
	globalCost GlobalCostReporter
}

// NewHealthHandler constructs a HealthHandler from the relevant server config.
// db is used both to read the on-disk database file size for the dbSizeCheck
// (informational; see internal/health.dbSizeCheck) and, via Readyz, to ping
// the DB for readiness. version is the running build's version string (see
// cmd/server's ldflags-stamped Version var); checkForUpdates opts into the
// best-effort "update available" check (see internal/health.updateCheck),
// gated by UPDATE_CHECK_ENABLED so the health endpoint never phones home by
// default. dispatcher supplies the dispatch loop's heartbeat for Readyz and,
// if it also implements GlobalCostReporter (as *agent.Dispatcher does), the
// global cost-ceiling snapshot surfaced on Readyz/Providers; it may be nil
// (e.g. in tests), in which case those checks are skipped/omitted.
func NewHealthHandler(q *gen.Queries, db *storage.DB, mcpBinary, repoBaseDir, llmBaseURL, llmAPIKey, backupDir string, backupInterval time.Duration, backupKeep int, version string, checkForUpdates bool, dispatcher DispatcherLiveness) *HealthHandler {
	h := &HealthHandler{
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
		dispatcher:      dispatcher,
	}
	if gc, ok := dispatcher.(GlobalCostReporter); ok {
		h.globalCost = gc
	}
	return h
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

	resp := map[string]any{"checks": checks}
	// Surface the global spend-ceiling status loudly here too (not just
	// Readyz) since this is the page a human actually looks at — a tripped
	// cap is the one condition where the whole system has stopped
	// dispatching new work while every other check here can still be green.
	if h.globalCost != nil {
		resp["global_cost"] = h.globalCost.GlobalCostStatus()
	}

	JSON(w, http.StatusOK, resp)
}
