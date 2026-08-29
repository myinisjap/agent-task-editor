package handlers

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
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
// http.DetectContentType to sniff "image/png" but not enough to be a
// decodable image (no IHDR chunk etc.) — exercises the fallback path where
// shrinkImageToBounds fails to decode and the original bytes are stored as-is.
var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}

// encodeTestPNG builds a real, decodable w x h PNG for resize tests.
func encodeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

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

// TestSaveUploadedAttachments_DownscalesOversizedImage verifies an oversized,
// decodable PNG is downscaled (preserving aspect ratio) before being stored.
func TestSaveUploadedAttachments_DownscalesOversizedImage(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	big := encodeTestPNG(t, 2500, 1200)
	req := multipartUploadRequest(t, map[string][]byte{"big.png": big})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}
	if len(paths) != 1 {
		t.Fatalf("expected exactly one saved attachment, got %v", paths)
	}

	full := filepath.Join(dir, paths[0])
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("saved file not found at %q: %v", full, err)
	}
	if bytes.Equal(data, big) {
		t.Fatalf("expected downscaled bytes to differ from the original")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to decode stored image: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != 2000 || b.Dy() != 960 {
		t.Errorf("expected 2000x960, got %dx%d", b.Dx(), b.Dy())
	}
}

// TestSaveUploadedAttachments_StoresSmallImageUnchanged verifies a small,
// decodable image is stored byte-for-byte identical to the upload.
func TestSaveUploadedAttachments_StoresSmallImageUnchanged(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	small := encodeTestPNG(t, 50, 50)
	req := multipartUploadRequest(t, map[string][]byte{"small.png": small})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}

	full := filepath.Join(dir, paths[0])
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("saved file not found at %q: %v", full, err)
	}
	if !bytes.Equal(data, small) {
		t.Errorf("expected stored file to be byte-identical to the upload")
	}
}

// TestSaveUploadedAttachments_NonDecodableImageStoredAsIs verifies that a
// file which sniffs as an image but fails to decode (e.g. the tinyPNG
// fixture, which is only a signature) is still accepted and stored
// unchanged, rather than causing the resize step to fail the request.
func TestSaveUploadedAttachments_NonDecodableImageStoredAsIs(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	req := multipartUploadRequest(t, map[string][]byte{"photo.png": tinyPNG})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}

	full := filepath.Join(dir, paths[0])
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("saved file not found at %q: %v", full, err)
	}
	if !bytes.Equal(data, tinyPNG) {
		t.Errorf("expected non-decodable image to be stored unchanged")
	}
}

// TestSaveUploadedAttachments_HugeDeclaredDimensionsStoredAsIs verifies the
// end-to-end upload path for a file that sniffs as a valid image and reports
// (via its header) an enormous pixel count while being tiny on disk — the
// maxDecodePixels guard in shrinkImageToBounds must reject decoding it
// before an oversized in-memory allocation is attempted, falling back to
// storing the original bytes exactly like any other undecodable/oversized
// case, rather than the request hanging or the process running out of
// memory.
func TestSaveUploadedAttachments_HugeDeclaredDimensionsStoredAsIs(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	huge := buildHugeDimensionPNG(t, 15000, 15000)
	req := multipartUploadRequest(t, map[string][]byte{"huge.png": huge})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}
	if len(paths) != 1 {
		t.Fatalf("expected exactly one saved attachment, got %v", paths)
	}

	full := filepath.Join(dir, paths[0])
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("saved file not found at %q: %v", full, err)
	}
	if !bytes.Equal(data, huge) {
		t.Errorf("expected the huge-declared-dimension file to be stored unchanged, not decoded/re-encoded")
	}
}

// TestSaveUploadedAttachments_IgnoresClientExtension verifies that a real
// PNG uploaded with a misleading/dangerous client filename (".html") is
// stored with the extension derived from the sniffed content type, not the
// client-supplied name — the core regression test for #142.
func TestSaveUploadedAttachments_IgnoresClientExtension(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	small := encodeTestPNG(t, 10, 10)
	req := multipartUploadRequest(t, map[string][]byte{"evil.html": small})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}
	if len(paths) != 1 {
		t.Fatalf("expected exactly one saved attachment, got %v", paths)
	}
	if filepath.Ext(paths[0]) != ".png" {
		t.Errorf("expected stored extension .png (from sniff), got %q", paths[0])
	}

	entries, err := os.ReadDir(filepath.Join(dir, "task-1"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".html" {
			t.Errorf("expected no .html file on disk, found %q", e.Name())
		}
	}
}

// TestSaveUploadedAttachments_NoExtensionFilenameGetsSniffedExt verifies
// that a client filename with no extension at all gets the sniffed image
// extension rather than falling back to ".bin" (the pre-#142 behavior).
func TestSaveUploadedAttachments_NoExtensionFilenameGetsSniffedExt(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	small := encodeTestPNG(t, 10, 10)
	req := multipartUploadRequest(t, map[string][]byte{"screenshot": small})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}
	if got := filepath.Ext(paths[0]); got != ".png" {
		t.Errorf("expected .png extension (sniffed), got %q", got)
	}
}

// tinyGIF is a minimal valid GIF89a header, enough for http.DetectContentType
// to sniff "image/gif". Used to verify extension mapping for a non-PNG image
// type together with a dangerous client-supplied filename.
var tinyGIF = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff\x21\xf9\x04\x00\x00\x00\x00\x00\x2c\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02\x44\x01\x00\x3b")

// TestSaveUploadedAttachments_GIFStoredWithGifExtensionRegardlessOfClientName
// verifies a GIF uploaded with a dangerous client extension (".php") is
// stored with ".gif", derived from the sniff.
func TestSaveUploadedAttachments_GIFStoredWithGifExtensionRegardlessOfClientName(t *testing.T) {
	dir := t.TempDir()
	h := &TasksHandler{uploadDir: dir}
	req := multipartUploadRequest(t, map[string][]byte{"x.php": tinyGIF})
	w := httptest.NewRecorder()

	paths, ok := h.saveUploadedAttachments(w, req, "task-1")
	if !ok {
		t.Fatalf("expected ok=true, response: %s", w.Body.String())
	}
	if got := filepath.Ext(paths[0]); got != ".gif" {
		t.Errorf("expected .gif extension (sniffed), got %q", got)
	}
}
