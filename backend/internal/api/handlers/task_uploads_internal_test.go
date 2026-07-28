package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// multipartUploadRequest builds an httptest.NewRequest carrying one or more
// "attachments" multipart file parts with the given filename/content pairs,
// with r.MultipartForm already parsed (as chi's middleware chain would have
// done before saveUploadedAttachments runs).
func multipartUploadRequest(t *testing.T, files map[string][]byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, content := range files {
		fw, err := w.CreateFormFile("attachments", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/tasks", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if err := req.ParseMultipartForm(32 << 20); err != nil {
		t.Fatalf("ParseMultipartForm: %v", err)
	}
	return req
}

// tinyPNG is a minimal valid PNG file signature + a few bytes, enough for
// http.DetectContentType to sniff "image/png".
var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}

func TestSaveUploadedAttachments_NoMultipartForm_ReturnsEmptyOK(t *testing.T) {
	h := &TasksHandler{uploadDir: t.TempDir()}
	req := httptest.NewRequest(http.MethodPost, "/tasks", nil)
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true when there's no multipart form at all")
	}
	if len(paths) != 0 {
		t.Errorf("expected no attachment paths, got %v", paths)
	}
}

func TestSaveUploadedAttachments_SavesImageUnderTaskDir(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	req := multipartUploadRequest(t, map[string][]byte{"photo.png": tinyPNG})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}
	if len(paths) != 1 {
		t.Fatalf("expected exactly one saved attachment, got %v", paths)
	}
	if filepath.Dir(paths[0]) != "task-1" {
		t.Errorf("expected path under task-1/, got %q", paths[0])
	}
	if filepath.Ext(paths[0]) != ".png" {
		t.Errorf("expected .png extension preserved, got %q", paths[0])
	}

	full := filepath.Join(dir, paths[0])
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("saved file not found at %q: %v", full, err)
	}
	if !bytes.Equal(data, tinyPNG) {
		t.Errorf("saved file content mismatch")
	}
}

func TestSaveUploadedAttachments_RejectsOversizedFile(t *testing.T) {
	h := &TasksHandler{uploadDir: t.TempDir()}
	oversized := make([]byte, maxSingleFile+1)
	req := multipartUploadRequest(t, map[string][]byte{"big.png": oversized})
	w := httptest.NewRecorder()

	_, ok := h.saveUploadedAttachments(w, req, "task-1")
	if ok {
		t.Fatal("expected ok=false for a file exceeding maxSingleFile")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("10 MB limit")) {
		t.Errorf("expected a 10 MB limit message, got %s", w.Body.String())
	}
}

func TestSaveUploadedAttachments_RejectsNonImageContent(t *testing.T) {
	h := &TasksHandler{uploadDir: t.TempDir()}
	req := multipartUploadRequest(t, map[string][]byte{"notes.txt": []byte("just plain text, not an image")})
	w := httptest.NewRecorder()

	_, ok := h.saveUploadedAttachments(w, req, "task-1")
	if ok {
		t.Fatal("expected ok=false for a non-image file")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("not an image")) {
		t.Errorf("expected a 'not an image' message, got %s", w.Body.String())
	}
}

func TestSaveUploadedAttachments_MultipleFilesAllSaved(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	req := multipartUploadRequest(t, map[string][]byte{
		"a.png": tinyPNG,
		"b.png": tinyPNG,
	})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-2")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 saved attachments, got %v", paths)
	}
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected saved file at %q: %v", p, err)
		}
	}
}

// TestSaveUploadedAttachments_DefaultsUploadDirWhenEmpty verifies the
// "uploads" fallback directory is used when uploadDir is unset. Runs from a
// temp CWD so it doesn't pollute the real working directory / repo tree.
func TestSaveUploadedAttachments_DefaultsUploadDirWhenEmpty(t *testing.T) {
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	h := &TasksHandler{uploadDir: ""}
	req := multipartUploadRequest(t, map[string][]byte{"a.png": tinyPNG})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-3")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(tmp, "uploads", paths[0])); err != nil {
		t.Errorf("expected file under ./uploads/, got err: %v", err)
	}
}
