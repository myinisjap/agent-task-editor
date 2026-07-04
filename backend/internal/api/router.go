// Package api wires together the HTTP router, middleware, and handlers.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

// NewRouter builds and returns the application router.
func NewRouter(db *storage.DB, engine *workflow.Engine, hub *ws.Hub, corsOrigins string, bearerToken string, repoBaseDir string, uploadDir string) http.Handler {
	q := gen.New(db.SQL())

	tasksH := handlers.NewTasksHandler(q, engine, uploadDir)
	workflowsH := handlers.NewWorkflowsHandler(q, db.SQL())
	agentsH := handlers.NewAgentsHandler(q)
	reposH := handlers.NewReposHandler(q, repoBaseDir)
	reviewH := handlers.NewReviewCommentsHandler(q)
	templatesH := handlers.NewTemplatesHandler(q)
	dashH := handlers.NewDashboardHandler(q)
	uploadsH := handlers.NewUploadsHandler(uploadDir)

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.Recover)
	r.Use(middleware.Logger)
	r.Use(middleware.CORS(corsOrigins))
	r.Use(middleware.BearerAuth(bearerToken))

	r.Get("/healthz", handlers.Health)

	// WebSocket endpoint — auth via ?token= query param (browsers can't set headers)
	r.Get("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws.ServeWS(hub, w, req, bearerToken, corsOrigins, q)
	})

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

		// GitHub auth status (used by the frontend to warn when gh credentials are absent)
		r.Get("/github/auth-status", handlers.GitHubAuthStatus)

		// Agent runs
		r.Get("/tasks/{id}/runs", tasksH.ListRuns)
		r.Get("/tasks/{id}/runs/{run_id}", tasksH.GetRun)
		r.Get("/tasks/{id}/runs/{run_id}/logs", tasksH.GetRunLogs)

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
	})

	return r
}
