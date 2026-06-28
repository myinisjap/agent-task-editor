package handlers

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// UploadsHandler serves task attachment files.
type UploadsHandler struct {
	uploadDir string
}

// NewUploadsHandler creates an UploadsHandler that serves files from uploadDir.
func NewUploadsHandler(uploadDir string) *UploadsHandler {
	return &UploadsHandler{uploadDir: uploadDir}
}

// ServeFile serves a single attachment file for a task.
// Route: GET /api/v1/uploads/{task_id}/{filename}
func (h *UploadsHandler) ServeFile(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "task_id")
	filename := chi.URLParam(r, "filename")

	// Validate path components to prevent directory traversal.
	if !isSafePathComponent(taskID) || !isSafePathComponent(filename) {
		Err(w, http.StatusBadRequest, "invalid path")
		return
	}

	fullPath := filepath.Join(h.uploadDir, taskID, filename)
	http.ServeFile(w, r, fullPath)
}

// isSafePathComponent returns true if the component contains no path separators,
// dots-only sequences, or null bytes.
func isSafePathComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, "/\\\x00") {
		return false
	}
	return true
}
