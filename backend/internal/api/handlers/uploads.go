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

	// Defense in depth against stored-XSS via attachments (#142): even
	// though saveUploadedAttachments now derives the stored extension
	// from the sniffed MIME type (never the client filename), files
	// written before that fix may still carry an attacker-chosen
	// extension (e.g. ".html") on disk. http.ServeFile infers
	// Content-Type from the extension, so without these headers such a
	// legacy file would still be served as text/html from the API
	// origin. X-Content-Type-Options stops browsers from re-sniffing
	// around an explicit/inferred Content-Type, and Content-Disposition
	// discourages inline rendering of anything ServeFile still guesses
	// as an HTML-ish type. Note: on a 404/error path ServeFile calls
	// http.Error, which overwrites Content-Type to text/plain — these
	// headers are harmlessly still present on that response.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `inline; filename=""`)
	if ct := contentTypeForStoredExt(filepath.Ext(filename)); ct != "" {
		// Pre-setting Content-Type wins: http.ServeFile only sets it
		// when unset.
		w.Header().Set("Content-Type", ct)
	}
	http.ServeFile(w, r, fullPath)
}

// contentTypeForStoredExt returns the trusted Content-Type for a file
// extension produced by saveUploadedAttachments' extForSniffedImageType
// map. It returns "" for anything else (including the legacy ".bin"
// fallback and any pre-#142 attacker-chosen extension still on disk),
// leaving Content-Type to http.ServeFile's own inference — safe because
// nosniff is always set by the caller.
func contentTypeForStoredExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
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
