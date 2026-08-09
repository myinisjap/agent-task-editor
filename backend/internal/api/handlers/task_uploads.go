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

// saveUploadedAttachments validates and saves every file under the
// "attachments" multipart field to disk under h.uploadDir/<taskID>/, returning
// the stored relative paths ("<task_id>/<filename>"). Images whose pixel
// dimensions exceed maxImageWidth/maxImageHeight (see image_resize.go) are
// downscaled (preserving aspect ratio) before being written; images that
// already fit, or that fail to decode/re-encode, are stored byte-for-byte
// unchanged. On any validation or I/O error it writes the error response
// itself (matching the exact status codes the inline version used to return)
// and returns ok=false; callers should return immediately in that case
// without writing any further response.
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
		if !strings.HasPrefix(detectedType, "image/") {
			Err(w, http.StatusBadRequest, fmt.Sprintf("file %q is not an image (detected: %s)", fh.Filename, detectedType))
			return nil, false
		}

		// Downscale oversized images (preserving aspect ratio) before
		// storing; fall back to the original bytes on any failure so a
		// resize bug never turns into a hard upload failure.
		ext := filepath.Ext(fh.Filename)
		if ext == "" {
			ext = ".bin"
		}
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
