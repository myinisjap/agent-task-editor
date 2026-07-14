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
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

// NewRouter builds and returns the application router.
//
// backupDir/backupInterval/backupKeep are only used to render the
// auto_backup health check (informational) — the actual scheduler is
// started separately in cmd/server/main.go. version/checkForUpdates
// configure the /healthz and /health/providers "Version"/"Update available"
// rows — see internal/health and cmd/server/main.go's ldflags-stamped
// Version var.
func NewRouter(db *storage.DB, engine *workflow.Engine, hub *ws.Hub, corsOrigins string, bearerToken string, namedTokens map[string]string, repoBaseDir string, uploadDir string, mcpBinary string, llmBaseURL string, llmAPIKey string, backupDir string, backupInterval time.Duration, backupKeep int, canceller handlers.RunCanceller, replyDispatcher handlers.ReplyDispatcher, metricsToken string, version string, checkForUpdates bool, chatSender handlers.ChatSender) http.Handler {
	q := gen.New(db.SQL())

	tasksH := handlers.NewTasksHandler(q, engine, uploadDir, canceller, replyDispatcher)
	depsH := handlers.NewDependenciesHandler(q, db.SQL(), hub)
	subtasksH := handlers.NewSubtasksHandler(q, db.SQL(), hub)
	workflowsH := handlers.NewWorkflowsHandler(q, db.SQL())
	agentsH := handlers.NewAgentsHandler(q)
	reposH := handlers.NewReposHandler(q, repoBaseDir, hub)
	reviewH := handlers.NewReviewCommentsHandler(q)
	templatesH := handlers.NewTemplatesHandler(q)
	dashH := handlers.NewDashboardHandler(q)
	uploadsH := handlers.NewUploadsHandler(uploadDir)
	healthH := handlers.NewHealthHandler(q, db, mcpBinary, repoBaseDir, llmBaseURL, llmAPIKey, backupDir, backupInterval, backupKeep, version, checkForUpdates)
	backupH := handlers.NewBackupHandler(db)
	wsTicketH := handlers.NewWSTicketHandler(hub)
	chatH := handlers.NewChatHandler(q, chatSender)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.Recover)
	r.Use(middleware.Logger)
	r.Use(middleware.CORS(corsOrigins))

	// WebSocket endpoint — mounted outside BearerAuth because browsers can't set
	// request headers on a WS handshake. ServeWS performs its own auth check via
	// a single-use ?ticket= (minted by the bearer-gated POST /ws-ticket below)
	// or, as a deprecated fallback, a constant-time compare against ?token=, so
	// it is not left unauthenticated. Note: this only supports the single
	// legacy bearerToken — it does not resolve named actors from namedTokens
	// (out of scope; WS auth is not a human-triggered REST transition, so it
	// doesn't need to record an actor).
	r.Get("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws.ServeWS(hub, w, req, bearerToken, corsOrigins, q)
	})

	// Prometheus scrape endpoint — mounted outside the main API's BearerAuth
	// group (and outside /api/v1) so self-hosters don't need to hand the
	// primary API_TOKEN to their Prometheus scrape config. Gated by its own,
	// independent METRICS_TOKEN (empty by default, i.e. unauthenticated,
	// matching most Prometheus setups) via the same BearerAuth middleware.
	r.With(middleware.BearerAuth(metricsToken, nil)).Get("/metrics", metrics.Handler().ServeHTTP)

	// Liveness probe — mounted outside BearerAuth so container orchestrators
	// (docker/k8s) can healthcheck without needing API_TOKEN. Returns only a
	// static {status, version}; leaks nothing sensitive. See docs/api.md.
	r.Get("/healthz", healthH.Healthz)

	// Everything below requires the Bearer token (when one is configured).
	r.Group(func(r chi.Router) {
		r.Use(middleware.BearerAuth(bearerToken, namedTokens))

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
			r.Get("/tasks/{id}/pr-url", tasksH.PRURL)
			r.Post("/tasks/{id}/pr", tasksH.CreatePR)
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

			// Inline diff review comments — persisted, injected into agent prompts while open
			r.Get("/tasks/{id}/review-comments", reviewH.List)
			r.Post("/tasks/{id}/review-comments", reviewH.Create)
			r.Patch("/tasks/{id}/review-comments/{comment_id}", reviewH.Update)
			r.Delete("/tasks/{id}/review-comments/{comment_id}", reviewH.Delete)

			// Interactive chat sessions — free-form conversations against a repo,
			// separate from the task/workflow state machine (see agent.ChatRunner).
			// Turns stream over WebSocket (chat.message / chat.turn_done events).
			r.Get("/chat/sessions", chatH.List)
			r.Post("/chat/sessions", chatH.Create)
			r.Get("/chat/sessions/{id}", chatH.Get)
			r.Delete("/chat/sessions/{id}", chatH.Delete)
			r.Post("/chat/sessions/{id}/messages", chatH.SendMessage)
			r.Post("/chat/sessions/{id}/cancel", chatH.Cancel)

			// GitHub auth status (used by the frontend to warn when gh credentials are absent)
			r.Get("/github/auth-status", handlers.GitHubAuthStatus)

			// Provider / onboarding health — checks CLI binaries, API keys, MCP
			// sidecar, gh auth, REPO_BASE_DIR, and automatic-backup config so
			// first-run misconfiguration is visible at a glance instead of
			// surfacing as failed agent runs.
			r.Get("/health/providers", healthH.Providers)

			// Streams a consistent point-in-time database snapshot (VACUUM INTO)
			// as application/octet-stream. Plain bearer-gated; see docs/backup.md.
			r.Get("/backup", backupH.Backup)

			// Label history — audit trail of transitions (who/what triggered them)
			r.Get("/tasks/{id}/label-history", tasksH.ListLabelHistory)

			// Mints a short-lived, single-use ticket for authenticating the
			// WebSocket upgrade (GET /ws?ticket=...) without putting the
			// long-lived API token in the URL. Bearer-gated: minting a ticket
			// requires already holding the token. See docs/websocket.md.
			r.Post("/ws-ticket", wsTicketH.IssueTicket)

			// Agent runs
			r.Get("/tasks/{id}/runs", tasksH.ListRuns)
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
			r.Delete("/agents/{id}", agentsH.Delete)
			r.Get("/agents/models", agentsH.GetModels)
			r.Get("/agents/claude-options", agentsH.GetClaudeOptions)

			// Repos
			r.Get("/repos", reposH.List)
			r.Post("/repos", reposH.Create)
			r.Get("/repos/{id}", reposH.Get)
			r.Patch("/repos/{id}", reposH.Update)
			r.Delete("/repos/{id}", reposH.Delete)
			r.Get("/repos/{id}/tree", reposH.Tree)

			// Dashboard
			r.Get("/dashboard", dashH.Get)
			r.Get("/dashboard/cost-by-task", dashH.CostByTask)
		})
	})

	return r
}
