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
func NewRouter(db *storage.DB, engine *workflow.Engine, hub *ws.Hub, corsOrigins string, bearerToken string) http.Handler {
	q := gen.New(db.SQL())

	tasksH := handlers.NewTasksHandler(q, engine)
	workflowsH := handlers.NewWorkflowsHandler(q)
	agentsH := handlers.NewAgentsHandler(q)
	reposH := handlers.NewReposHandler(q)
	dashH := handlers.NewDashboardHandler(q)

	r := chi.NewRouter()
	r.Use(middleware.Recover)
	r.Use(middleware.Logger)
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.CORS(corsOrigins))
	r.Use(middleware.BearerAuth(bearerToken))

	r.Get("/healthz", handlers.Health)

	// WebSocket endpoint
	r.Get("/ws", func(w http.ResponseWriter, req *http.Request) {
		ws.ServeWS(hub, w, req)
	})

	r.Route("/api/v1", func(r chi.Router) {
		// Tasks
		r.Get("/tasks", tasksH.List)
		r.Post("/tasks", tasksH.Create)
		r.Get("/tasks/{id}", tasksH.Get)
		r.Patch("/tasks/{id}", tasksH.Update)
		r.Delete("/tasks/{id}", tasksH.Delete)
		r.Patch("/tasks/{id}/label", tasksH.MoveLabel)
		r.Post("/tasks/{id}/approve", tasksH.Approve)
		r.Post("/tasks/{id}/reject", tasksH.Reject)

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
		r.Post("/workflows/import", workflowsH.ImportWorkflowYAML)

		// Agent configs
		r.Get("/agents", agentsH.List)
		r.Post("/agents", agentsH.Create)
		r.Get("/agents/{id}", agentsH.Get)
		r.Put("/agents/{id}", agentsH.Update)
		r.Delete("/agents/{id}", agentsH.Delete)

		// Repos
		r.Get("/repos", reposH.List)
		r.Post("/repos", reposH.Create)
		r.Get("/repos/{id}", reposH.Get)
		r.Delete("/repos/{id}", reposH.Delete)
		r.Get("/repos/{id}/tree", reposH.Tree)
		r.Get("/repos/{id}/diff", reposH.Diff)

		// Dashboard
		r.Get("/dashboard", dashH.Get)
	})

	return r
}
