package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
)

func setupUploadsRouter(t *testing.T, uploadDir string) http.Handler {
	t.Helper()
	h := handlers.NewUploadsHandler(uploadDir)
	r := chi.NewRouter()
	r.Get("/uploads/{task_id}/{filename}", h.ServeFile)
	return r
}

func TestUploadsServeFile_OK(t *testing.T) {
	dir := t.TempDir()
	taskID := "task-1"
	if err := os.MkdirAll(filepath.Join(dir, taskID), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := []byte("hello world")
	if err := os.WriteFile(filepath.Join(dir, taskID, "photo.png"), content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	router := setupUploadsRouter(t, dir)
	req := httptest.NewRequest(http.MethodGet, "/uploads/"+taskID+"/photo.png", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != string(content) {
		t.Errorf("expected body %q, got %q", content, w.Body.String())
	}
}

func TestUploadsServeFile_UnknownFile(t *testing.T) {
	dir := t.TempDir()
	router := setupUploadsRouter(t, dir)

	req := httptest.NewRequest(http.MethodGet, "/uploads/task-1/does-not-exist.png", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestUploadsServeFile_PathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	router := setupUploadsRouter(t, dir)

	// chi normalizes ".." segments in the path before routing, so this
	// exercises isSafePathComponent's rejection of an embedded ".." that
	// chi's own cleanup doesn't strip (e.g. a literal ".." filename).
	req := httptest.NewRequest(http.MethodGet, "/uploads/task-1/..", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound && w.Code != http.StatusMovedPermanently {
		t.Fatalf("expected traversal attempt to be rejected (400/404/301), got %d: %s", w.Code, w.Body.String())
	}
}
