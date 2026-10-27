// Package api wires together the HTTP router, middleware, and handlers.
package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/skills"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

// RouterDeps carries everything NewRouter needs. Named fields replace a long
// positional parameter list in which adjacent string/bool/int values were easy
// to transpose.
//
// BackupDir/BackupInterval/BackupKeep are only used to render the auto_backup
// health check (informational) — the actual scheduler is started separately in
// cmd/server/main.go. Version/CheckForUpdates configure the /healthz and
// /health/providers "Version"/"Update available" rows — see internal/health
// and cmd/server/main.go's ldflags-stamped Version var. MaxWorkers is the
// global MAX_WORKERS setting (informational only here) — the dashboard uses it
// as the effective per-repo concurrency limit fallback for repos with no
// repos.max_concurrent_runs override.
type RouterDeps struct {
	DB     *storage.DB
	Engine *workflow.Engine
	Hub    *ws.Hub

	CORSOrigins  string
	AllowedHosts string

	BearerToken  string
	NamedTokens  map[string]string
	MetricsToken string
	// MetricsPublic leaves /metrics unauthenticated even when API auth is on.
	MetricsPublic bool

	// GitHubWebhookSecret enables POST /api/v1/github/webhook (HMAC-SHA256
	// signature auth, outside bearer auth). Empty = the route returns 404.
	GitHubWebhookSecret string
	// PRRefresher receives matched webhook events (the ghsync Syncer).
	PRRefresher handlers.PRRefresher

	RepoBaseDir string
	// ListenAddr is the HTTP server's bind address (e.g. ":8080"); used only
	// for the api_auth health warning.
	ListenAddr string
	UploadDir  string
	// SkillsDir is where skill folders are stored (SKILLS_DIR).
	SkillsDir string
	MCPBinary string

	BackupDir      string
	BackupInterval time.Duration
	BackupKeep     int

	Version         string
	CheckForUpdates bool
	MaxWorkers      int

	// Canceller cancels in-flight runs (the worker pool).
	Canceller handlers.RunCanceller
	// ReplyDispatcher delivers human replies to waiting runs (the dispatcher).
	ReplyDispatcher handlers.ReplyDispatcher
	// DispatcherLiveness reports dispatch-loop liveness (the dispatcher). It
	// may also implement handlers.GlobalCostReporter.
	DispatcherLiveness handlers.DispatcherLiveness
	BlockReasons       handlers.BlockReasonResolver
	// Terminal backs the chat terminal WebSocket; nil makes it return 503.
	Terminal handlers.Terminal
}

// NewRouter builds and returns the application router.
func NewRouter(deps RouterDeps) http.Handler {
	db, engine, hub := deps.DB, deps.Engine, deps.Hub
	corsOrigins, allowedHosts := deps.CORSOrigins, deps.AllowedHosts
	bearerToken, namedTokens := deps.BearerToken, deps.NamedTokens
	metricsToken, metricsPublic := deps.MetricsToken, deps.MetricsPublic
	repoBaseDir, uploadDir := deps.RepoBaseDir, deps.UploadDir
	dispatcherLiveness := deps.DispatcherLiveness
	q := gen.New(db.SQL())
	briefCache := reviewbrief.NewCache()

	tasksH := handlers.NewTasksHandler(q, engine, uploadDir, deps.Canceller, deps.ReplyDispatcher, deps.BlockReasons)
	tasksH.SetPublisher(hub)
	depsH := handlers.NewDependenciesHandler(q, db.SQL(), hub)
	subtasksH := handlers.NewSubtasksHandler(q, db.SQL(), hub)
	workflowsH := handlers.NewWorkflowsHandler(q, db.SQL())
	agentsH := handlers.NewAgentsHandler(q, db.SQL())
	providersH := handlers.NewProviderConfigsHandler(q)
	reposH := handlers.NewReposHandler(q, repoBaseDir, hub)
	reviewH := handlers.NewReviewCommentsHandler(q)
	templatesH := handlers.NewTemplatesHandler(q)
	lessonsH := handlers.NewLessonsHandler(q)
	schedulesH := handlers.NewSchedulesHandler(q)
	intakeRulesH := handlers.NewIntakeRulesHandler(q)
	skillsH := handlers.NewSkillsHandler(q, db.SQL(), skills.Store{Dir: deps.SkillsDir})
	// dispatcherLiveness may also implement GlobalCostReporter (as
	// *agent.Dispatcher does) to supply the dashboard's global spend-ceiling
	// snapshot/forecast — same optional-capability pattern as HealthHandler.
	var globalCost handlers.GlobalCostReporter
	if gc, ok := dispatcherLiveness.(handlers.GlobalCostReporter); ok {
		globalCost = gc
	}
	dashH := handlers.NewDashboardHandler(q, deps.MaxWorkers, globalCost)
	outcomeQualityH := handlers.NewOutcomeQualityHandler(q)
	uploadsH := handlers.NewUploadsHandler(uploadDir)
	healthH := handlers.NewHealthHandler(q, db, deps.MCPBinary, repoBaseDir, deps.BackupDir, deps.BackupInterval, deps.BackupKeep, deps.Version, deps.CheckForUpdates, dispatcherLiveness)
	healthH.SetAuthInfo(deps.BearerToken != "" || len(deps.NamedTokens) > 0, deps.ListenAddr)
	backupH := handlers.NewBackupHandler(db)
	backupSettingsH := handlers.NewBackupSettingsHandler(q)
	logRetentionSettingsH := handlers.NewLogRetentionSettingsHandler(q)
	costWarningSettingsH := handlers.NewCostWarningSettingsHandler(q)
	modelPricingH := handlers.NewModelPricingHandler(q, db.SQL())
	wsTicketH := handlers.NewWSTicketHandler(hub)
	chatH := handlers.NewChatHandler(q, hub, deps.Terminal, bearerToken != "" || len(namedTokens) > 0, corsOrigins)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.Recover)
	r.Use(middleware.Logger)
	// Browser-facing request guards (see middleware/requestguard.go). Order
	// matters: the Host check runs first (DNS rebinding; applies to every
	// route incl. /ws, /healthz, /metrics, and only when no API token is
	// configured), then CORS so its headers — and the preflight 204 — are
	// produced for allowed origins, then the state-changing-request guards.
	// Both guards ignore OPTIONS, so preflight is never blocked by them.
	hostPolicy := middleware.NewHostPolicy(allowedHosts, corsOrigins, bearerToken != "" || len(namedTokens) > 0)
	r.Use(hostPolicy.Middleware())
	r.Use(middleware.CORS(corsOrigins))
	r.Use(middleware.RequireTrustedOrigin(corsOrigins))
	r.Use(middleware.RequireJSON)

	// WebSocket endpoint — mounted outside BearerAuth because browsers can't set
	// request headers on a WS handshake. ServeWS performs its own auth check via
	// a single-use ?ticket= (minted by the bearer-gated POST /ws-ticket below),
	// so it is not left unauthenticated. The old ?token= query-string fallback
	// has been removed.
	authRequired := bearerToken != "" || len(namedTokens) > 0
	r.Get("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws.ServeWS(hub, w, req, authRequired, corsOrigins, q)
	})

	// Chat terminal WebSocket — outside BearerAuth for the same reason as /ws:
	// browsers can't send an Authorization header on a WS upgrade, so the
	// handler enforces the single-use ?ticket= itself.
	r.Get("/api/v1/chat/sessions/{id}/terminal", chatH.Terminal)

	// GitHub webhook receiver — outside BearerAuth because GitHub cannot send
	// our token; authenticated by the HMAC-SHA256 X-Hub-Signature-256 header
	// against GITHUB_WEBHOOK_SECRET (404 when unset). It sits on a literal path
	// so it wins over the /api/v1 mount below.
	webhookH := handlers.NewGitHubWebhookHandler(deps.GitHubWebhookSecret, deps.PRRefresher)
	r.Post("/api/v1/github/webhook", webhookH.Handle)

	// Prometheus scrape endpoint — mounted outside the main API's BearerAuth
	// group (and outside /api/v1) so self-hosters don't need to hand the
	// primary API_TOKEN to their Prometheus scrape config. Gated by its own,
	// independent METRICS_TOKEN via the same BearerAuth middleware. When
	// METRICS_TOKEN is unset and API auth is configured, the API tokens gate
	// /metrics too (unless METRICS_PUBLIC opts out); with no auth configured
	// at all it stays open.
	metricsAuth := middleware.BearerAuth(metricsToken, nil)
	if metricsToken == "" && !metricsPublic {
		metricsAuth = middleware.BearerAuth(bearerToken, namedTokens)
	}
	r.With(metricsAuth).Get("/metrics", metrics.Handler().ServeHTTP)

	// Liveness probe — mounted outside BearerAuth so container orchestrators
	// (docker/k8s) can healthcheck without needing API_TOKEN. Returns only a
	// static {status, version}; leaks nothing sensitive. See docs/api.md.
	r.Get("/healthz", healthH.Healthz)

	// Readiness probe — like /healthz but also verifies DB connectivity and
	// that the dispatch loop is still ticking, so a wedged backend reports
	// unhealthy to Docker. Unauthenticated for the same reason as /healthz.
	r.Get("/readyz", healthH.Readyz)

	// Everything below requires the Bearer token (when one is configured).
	r.Group(func(r chi.Router) {
		r.Use(middleware.BearerAuthTrusted(bearerToken, namedTokens))

		r.Route("/api/v1", func(r chi.Router) {
			// Limit request bodies to 1 MB to prevent memory exhaustion.
			// The task create endpoint (POST /tasks) uses multipart and handles its own
			// limit via ParseMultipartForm — we exclude it from the 1 MB cap.
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if req.Method == http.MethodPost && req.URL.Path == "/api/v1/tasks" {
						// Task create allows up to 50 MB for image uploads
						req.Body = http.MaxBytesReader(w, req.Body, 50<<20)
					} else {
						req.Body = http.MaxBytesReader(w, req.Body, 1<<20)
					}
					next.ServeHTTP(w, req)
				})
			})
			// Tasks
			r.Get("/tasks", tasksH.List)
			r.Post("/tasks", tasksH.Create)
			r.Post("/tasks/bulk", tasksH.Bulk)

			// Uploads — serve attachment images
			r.Get("/uploads/{task_id}/{filename}", uploadsH.ServeFile)
			r.Get("/tasks/{id}", tasksH.Get)
			r.Patch("/tasks/{id}", tasksH.Update)
			r.Delete("/tasks/{id}", tasksH.Delete)
			r.Patch("/tasks/{id}/label", tasksH.MoveLabel)
			r.Post("/tasks/{id}/approve", tasksH.Approve)
			r.Post("/tasks/{id}/reject", tasksH.Reject)
			r.Patch("/tasks/{id}/notes", tasksH.UpdateNotes)
			r.Post("/tasks/{id}/rerun", tasksH.Rerun)
			r.Get("/tasks/{id}/diff", tasksH.Diff)
			r.Get("/tasks/{id}/review-brief", handlers.NewReviewBriefHandler(q, briefCache).Get)
			r.Get("/tasks/{id}/pr-url", tasksH.PRURL)
			r.Post("/tasks/{id}/pr", tasksH.CreatePR)
			r.Post("/tasks/{id}/side-effects/retry", tasksH.RetrySideEffects)
			r.Delete("/tasks/{id}/side-effects", tasksH.DismissSideEffects)
			r.Post("/tasks/{id}/source-update/apply", tasksH.ApplySourceUpdate)
			r.Delete("/tasks/{id}/source-update", tasksH.DiscardSourceUpdate)
			r.Get("/tasks/{id}/github-status", tasksH.GitHubStatus)
			r.Patch("/tasks/{id}/git-state", tasksH.UpdateGitState)
			r.Patch("/tasks/{id}/pause", tasksH.SetPaused)
			r.Patch("/tasks/{id}/archive", tasksH.SetArchived)

			// Peer task dependencies (dispatch gate)
			r.Get("/tasks/{id}/dependencies", depsH.List)
			r.Post("/tasks/{id}/dependencies", depsH.Add)
			r.Delete("/tasks/{id}/dependencies/{dep_id}", depsH.Remove)

			// Agent-driven subtasks (create_subtask MCP tool posts here live)
			r.Post("/tasks/{id}/subtasks", subtasksH.Create)

			// Task templates — pre-filled title/description/type for recurring work
			r.Get("/templates", templatesH.List)
			r.Post("/templates", templatesH.Create)
			r.Get("/templates/{id}", templatesH.Get)
			r.Put("/templates/{id}", templatesH.Update)
			r.Delete("/templates/{id}", templatesH.Delete)

			// Repo lessons: curated cross-task knowledge injected into agent prompts
			r.Get("/repos/{id}/lessons", lessonsH.ListForRepo)
			r.Post("/repos/{id}/lessons", lessonsH.CreateForRepo)
			r.Get("/lessons", lessonsH.ListGlobal)
			r.Post("/lessons", lessonsH.CreateGlobal)
			r.Patch("/lessons/{id}", lessonsH.Update)
			r.Delete("/lessons/{id}", lessonsH.Delete)

			// Task schedules — fire a template on a cron expression against a repo
			r.Get("/schedules", schedulesH.List)
			r.Post("/schedules", schedulesH.Create)
			r.Get("/schedules/{id}", schedulesH.Get)
			r.Put("/schedules/{id}", schedulesH.Update)
			r.Delete("/schedules/{id}", schedulesH.Delete)

			// Intake routing rules — match->apply rules evaluated at
			// task-creation time for the issue-import and schedule sources
			// (see internal/intake and migration 051)
			r.Get("/intake-rules", intakeRulesH.List)
			r.Post("/intake-rules", intakeRulesH.Create)
			r.Post("/intake-rules/preview", intakeRulesH.Preview)
			r.Get("/intake-rules/{id}", intakeRulesH.Get)
			r.Put("/intake-rules/{id}", intakeRulesH.Update)
			r.Delete("/intake-rules/{id}", intakeRulesH.Delete)

			// Inline diff review comments — persisted, injected into agent prompts while open
			r.Get("/tasks/{id}/review-comments", reviewH.List)
			r.Post("/tasks/{id}/review-comments", reviewH.Create)
			r.Patch("/tasks/{id}/review-comments/{comment_id}", reviewH.Update)
			r.Delete("/tasks/{id}/review-comments/{comment_id}", reviewH.Delete)

			// Interactive chat sessions — free-form conversations against a repo,
			// separate from the task/workflow state machine (see agent.TerminalManager).
			// The session's interactive CLI runs in a PTY streamed over the
			// /terminal WebSocket (raw bytes) is mounted above, outside this
			// group, with ?ticket= auth like /ws.
			r.Get("/chat/sessions", chatH.List)
			r.Post("/chat/sessions", chatH.Create)
			r.Get("/chat/sessions/{id}", chatH.Get)
			r.Delete("/chat/sessions/{id}", chatH.Delete)

			// GitHub auth status (used by the frontend to warn when gh credentials are absent)
			r.Get("/github/auth-status", handlers.GitHubAuthStatus)

			// Provider / onboarding health — checks CLI binaries, API keys, MCP
			// sidecar, gh auth, REPO_BASE_DIR, and automatic-backup config so
			// first-run misconfiguration is visible at a glance instead of
			// surfacing as failed agent runs.
			r.Get("/health/providers", healthH.Providers)
			r.Post("/health/providers/{provider}/resume", healthH.ResumeProvider)

			// Streams a consistent point-in-time database snapshot (VACUUM INTO)
			// as application/octet-stream. Plain bearer-gated; see docs/backup.md.
			r.Get("/backup", backupH.Backup)

			// Automatic local-backup scheduler settings (interval/retention
			// count) — see docs/backup.md. Whether the scheduler is enabled at
			// all remains a deploy-time choice (BACKUP_DIR).
			r.Get("/backup/settings", backupSettingsH.Get)
			r.Put("/backup/settings", backupSettingsH.Update)

			// Agent-log retention pruner settings (retention days / run
			// interval) — see docs/backup.md#agent-log-retention. Unlike
			// backup, there's no separate deploy-time enable gate: days=0
			// fully disables cleanup via this settings row alone.
			r.Get("/log-retention/settings", logRetentionSettingsH.Get)
			r.Put("/log-retention/settings", logRetentionSettingsH.Update)

			// User-editable per-model USD pricing table used to estimate
			// anthropic/llm run costs (see internal/agent/providers/pricing.go).
			// PUT replaces the whole table (add/remove rows are expressed
			// client-side as a new full list); unlisted models fall back to
			// the hardcoded map.
			r.Get("/settings/pricing", modelPricingH.List)
			r.Put("/settings/pricing", modelPricingH.Update)

			// Global cost-warning early-threshold (see
			// internal/agent/providers/cost_watchdog.go and
			// Dispatcher.resolveCostWarnRatio) — the fraction of a task's
			// effective cost budget at which task.cost_warning fires ahead of
			// the hard kill/exhaustion at 1.0. Read fresh on every relevant
			// check, so a change here takes effect without a restart.
			r.Get("/settings/cost-warning", costWarningSettingsH.Get)
			r.Put("/settings/cost-warning", costWarningSettingsH.Update)

			// Label history — audit trail of transitions (who/what triggered them)
			r.Get("/tasks/{id}/label-history", tasksH.ListLabelHistory)

			// Ingested source-issue comment thread (see tasksource.Importer)
			r.Get("/tasks/{id}/source-comments", tasksH.ListSourceComments)
			// Human replies to waiting_human runs (for "Remember for this repo").
			r.Get("/tasks/{id}/human-decisions", tasksH.ListHumanDecisions)

			// Mints a short-lived, single-use ticket for authenticating the
			// WebSocket upgrade (GET /ws?ticket=...) without putting the
			// long-lived API token in the URL. Bearer-gated: minting a ticket
			// requires already holding the token. See docs/websocket.md.
			r.Post("/ws-ticket", wsTicketH.IssueTicket)

			// Agent runs
			r.Get("/tasks/{id}/runs", tasksH.ListRuns)
			r.Get("/tasks/{id}/verifications", tasksH.ListVerifications)
			r.Get("/tasks/{id}/previews", tasksH.ListPreviews)
			r.Get("/tasks/{id}/runs/{run_id}", tasksH.GetRun)
			r.Get("/tasks/{id}/runs/{run_id}/logs", tasksH.GetRunLogs)
			r.Post("/tasks/{id}/runs/{run_id}/cancel", tasksH.CancelRun)
			r.Post("/tasks/{id}/runs/{run_id}/reply", tasksH.ReplyRun)

			// Workflows
			r.Get("/workflows", workflowsH.List)
			r.Post("/workflows", workflowsH.Create)
			r.Get("/workflows/{id}", workflowsH.Get)
			r.Put("/workflows/{id}", workflowsH.Update)
			r.Delete("/workflows/{id}", workflowsH.Delete)
			r.Get("/workflows/{id}/export.yaml", workflowsH.ExportWorkflowYAML)
			r.Put("/workflows/{id}/yaml", workflowsH.UpdateWorkflowYAML)
			r.Post("/workflows/import", workflowsH.ImportWorkflowYAML)

			// Agent configs
			r.Get("/agents", agentsH.List)
			r.Post("/agents", agentsH.Create)
			r.Get("/agents/{id}", agentsH.Get)
			r.Put("/agents/{id}", agentsH.Update)
			r.Get("/agents/{id}/versions", agentsH.ListVersions)
			r.Get("/agents/{id}/versions/{version}", agentsH.GetVersion)
			r.Post("/agents/{id}/versions/{version}/restore", agentsH.RestoreVersion)
			r.Delete("/agents/{id}", agentsH.Delete)
			r.Get("/agents/models", agentsH.GetModels)
			r.Get("/agents/claude-options", agentsH.GetClaudeOptions)

			// Provider configs
			r.Get("/provider-configs", providersH.List)
			r.Post("/provider-configs", providersH.Create)
			r.Get("/provider-configs/{id}", providersH.Get)
			r.Put("/provider-configs/{id}", providersH.Update)
			r.Delete("/provider-configs/{id}", providersH.Delete)

			// Repos
			r.Get("/repos", reposH.List)
			r.Post("/repos", reposH.Create)
			r.Get("/repos/{id}", reposH.Get)
			r.Patch("/repos/{id}", reposH.Update)
			r.Delete("/repos/{id}", reposH.Delete)
			r.Get("/repos/{id}/tree", reposH.Tree)
			r.Post("/repos/{id}/runtime/detect", reposH.Detect)

			// Skills library (admin config; deliberately absent from scopedtoken routes)
			r.Get("/skills", skillsH.List)
			r.Post("/skills", skillsH.Create)
			r.Get("/skills/{id}", skillsH.Get)
			r.Patch("/skills/{id}", skillsH.Update)
			r.Delete("/skills/{id}", skillsH.Delete)
			r.Get("/agents/{id}/skills", skillsH.ListForAgent)
			r.Put("/agents/{id}/skills", skillsH.SetForAgent)
			r.Get("/repos/{id}/skills", skillsH.ListForRepo)
			r.Put("/repos/{id}/skills", skillsH.SetForRepo)

			// Dashboard
			needsMeH := handlers.NewNeedsMeHandler(q)
			needsMeH.SetBriefCache(briefCache)
			r.Get("/needs-me", needsMeH.List)
			r.Get("/dashboard", dashH.Get)
			r.Get("/dashboard/cost-by-task", dashH.CostByTask)
			r.Get("/dashboard/outcome-quality", outcomeQualityH.Get)
		})
	})

	return r
}
