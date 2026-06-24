package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
)

// NewRouter builds and returns the application router.
func NewRouter(db *storage.DB, corsOrigins string) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Recover)
	r.Use(middleware.Logger)
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.CORS(corsOrigins))

	r.Get("/healthz", handlers.Health)

	r.Route("/api/v1", func(r chi.Router) {
		// Placeholder routes — handlers added in Phase 2/3
		r.Get("/tasks", handlers.NotImplemented)
		r.Get("/workflows", handlers.NotImplemented)
		r.Get("/agents", handlers.NotImplemented)
		r.Get("/repos", handlers.NotImplemented)
		r.Get("/dashboard", handlers.NotImplemented)
	})

	return r
}
