package handlers

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// maxUploadSize is the maximum total multipart body size (50 MB).
const maxUploadSize = 50 << 20

// maxSingleFile is the maximum size per image file (10 MB).
const maxSingleFile = 10 << 20

// extForSniffedImageType maps the MIME types http.DetectContentType can
// return for images to the extension we store them under. The stored
// extension must never come from the client-supplied filename:
// UploadsHandler.ServeFile derives Content-Type from the extension, so a
// GIF/HTML polyglot uploaded as "evil.html" would pass the image/* sniff and
// later be served as text/html from the API origin (stored XSS — issue
// #142). Any sniffed type not in this map (including image/* types we don't
// support, e.g. image/bmp) is rejected outright.
var extForSniffedImageType = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// saveUploadedAttachments validates and saves every file under the
// "attachments" multipart field to disk under h.uploadDir/<taskID>/, returning
// the stored relative paths ("<task_id>/<filename>"). The stored file
// extension is derived from the sniffed MIME type (see
// extForSniffedImageType), never from the client-supplied filename — this is
// a deliberate security control (#142), not an incidental detail. Images
// whose pixel dimensions exceed maxImageWidth/maxImageHeight (see
// image_resize.go) are downscaled (preserving aspect ratio) before being
// written; images that already fit, or that fail to decode/re-encode, are
// stored byte-for-byte unchanged. On any validation or I/O error it writes
// the error response itself (matching the exact status codes the inline
// version used to return) and returns ok=false; callers should return
// immediately in that case without writing any further response.
func (h *TasksHandler) saveUploadedAttachments(w http.ResponseWriter, r *http.Request, taskID string) (attachmentPaths []string, ok bool) {
	if r.MultipartForm == nil || r.MultipartForm.File == nil {
		return nil, true
	}
	files := r.MultipartForm.File["attachments"]
	for _, fh := range files {
		// Validate size
		if fh.Size > maxSingleFile {
			Err(w, http.StatusBadRequest, fmt.Sprintf("file %q exceeds 10 MB limit", fh.Filename))
			return nil, false
		}
		// Validate MIME type
		f, err := fh.Open()
		if err != nil {
			Err(w, http.StatusInternalServerError, "failed to open uploaded file")
			return nil, false
		}
		defer f.Close() //nolint:errcheck

		data, err := io.ReadAll(f)
		if err != nil {
			Err(w, http.StatusInternalServerError, "failed to read uploaded file")
			return nil, false
		}

		detectedType := http.DetectContentType(data)
		// DetectContentType can append parameters (e.g. "text/plain;
		// charset=utf-8"); normalize before matching so we don't miss a
		// legitimate image/* type that happens to carry one.
		normalizedType := strings.TrimSpace(strings.SplitN(detectedType, ";", 2)[0])
		if !strings.HasPrefix(normalizedType, "image/") {
			Err(w, http.StatusBadRequest, fmt.Sprintf("file %q is not an image (detected: %s)", fh.Filename, detectedType))
			return nil, false
		}

		// The stored extension is derived solely from the sniffed MIME
		// type — never from the client-supplied filename — so a
		// polyglot upload can't smuggle an executable extension (e.g.
		// ".html") onto disk. See extForSniffedImageType (#142).
		ext, supported := extForSniffedImageType[normalizedType]
		if !supported {
			Err(w, http.StatusBadRequest, fmt.Sprintf("file %q has an unsupported image type: %s", fh.Filename, detectedType))
			return nil, false
		}

		// Downscale oversized images (preserving aspect ratio) before
		// storing; fall back to the original bytes on any failure so a
		// resize bug never turns into a hard upload failure.
		payload := data
		if res, err := shrinkImageToBounds(data); err != nil {
			slog.Warn("saveUploadedAttachments: resize failed, storing original", "file", fh.Filename, "err", err)
		} else if res.Resized {
			payload = res.Data
			ext = res.Ext
		}

		// Build safe filename: UUID + extension (matching the stored encoding)
		safeFilename := uuid.NewString() + ext

		// Ensure upload directory exists
		uploadDir := h.uploadDir
		if uploadDir == "" {
			uploadDir = "uploads"
		}
		taskUploadDir := filepath.Join(uploadDir, taskID)
		if err := os.MkdirAll(taskUploadDir, 0o755); err != nil {
			Err(w, http.StatusInternalServerError, "failed to create upload directory")
			return nil, false
		}

		dstPath := filepath.Join(taskUploadDir, safeFilename)
		if err := os.WriteFile(dstPath, payload, 0o644); err != nil {
			Err(w, http.StatusInternalServerError, "failed to write upload file")
			return nil, false
		}

		// Store as relative path: "<task_id>/<filename>"
		attachmentPaths = append(attachmentPaths, filepath.Join(taskID, safeFilename))
	}
	return attachmentPaths, true
}
