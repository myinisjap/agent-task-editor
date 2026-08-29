package handlers_test

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestUploadsServeFile_SecurityHeaders verifies ServeFile sets the
// hardening headers added for #142: nosniff, a non-attachment
// Content-Disposition, and an explicit trusted Content-Type derived from
// the stored (sniff-derived) extension rather than left to http.ServeFile's
// own extension-based inference.
func TestUploadsServeFile_SecurityHeaders(t *testing.T) {
	dir := t.TempDir()
	taskID := "task-1"
	if err := os.MkdirAll(filepath.Join(dir, taskID), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, taskID, "x.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	router := setupUploadsRouter(t, dir)
	req := httptest.NewRequest(http.MethodGet, "/uploads/"+taskID+"/x.png", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got %q", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != `inline; filename=""` {
		t.Errorf("expected Content-Disposition: inline; filename=\"\", got %q", got)
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("expected Content-Type: image/png, got %q", got)
	}
}

// TestUploadsServeFile_UnknownExtensionNotSniffedAsHTML simulates a file
// stored on disk before the #142 fix (extension controlled by the
// attacker, e.g. a stored ".php" containing HTML content) with an extension
// outside our trusted image map — contentTypeForStoredExt intentionally
// leaves Content-Type inference to http.ServeFile in that case (still safe
// for our own extension whitelist going forward), so what actually
// neutralizes this legacy scenario in the browser is X-Content-Type-Options:
// nosniff. Verify that header is present regardless of what Content-Type
// ServeFile ends up inferring.
func TestUploadsServeFile_UnknownExtensionNotSniffedAsHTML(t *testing.T) {
	dir := t.TempDir()
	taskID := "task-1"
	if err := os.MkdirAll(filepath.Join(dir, taskID), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := []byte("<html><body><script>alert(1)</script></body></html>")
	if err := os.WriteFile(filepath.Join(dir, taskID, "legacy.php"), content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	router := setupUploadsRouter(t, dir)
	req := httptest.NewRequest(http.MethodGet, "/uploads/"+taskID+"/legacy.php", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected X-Content-Type-Options: nosniff, got %q", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != `inline; filename=""` {
		t.Errorf("expected Content-Disposition: inline; filename=\"\", got %q", got)
	}
}

// TestUploadsServeFile_TrustedExtensionOverridesInference verifies that for
// extensions in our trusted image map, ServeFile sets an explicit
// Content-Type derived from the extension rather than leaving it to
// http.ServeFile's own (extension-based) inference — this is what actually
// stops a legacy ".png"-named file containing HTML from being served as
// text/html, since a bare ".png" extension is already whitelisted by
// net/http's own mime type table incorrectly matching content.
func TestUploadsServeFile_TrustedExtensionOverridesInference(t *testing.T) {
	dir := t.TempDir()
	taskID := "task-1"
	if err := os.MkdirAll(filepath.Join(dir, taskID), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Deliberately store HTML content under a trusted ".png" extension to
	// simulate the pre-#142 vulnerable state (client extension trusted at
	// upload time). ServeFile must still report image/png because
	// Content-Type is now derived from the extension, not sniffed.
	content := []byte("<html><body><script>alert(1)</script></body></html>")
	if err := os.WriteFile(filepath.Join(dir, taskID, "evil.png"), content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	router := setupUploadsRouter(t, dir)
	req := httptest.NewRequest(http.MethodGet, "/uploads/"+taskID+"/evil.png", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("expected Content-Type: image/png (from trusted extension map), got %q", got)
	}
	if strings.Contains(w.Header().Get("Content-Type"), "html") {
		t.Errorf("must never report an HTML content type for a .png-extensioned file")
	}
}
