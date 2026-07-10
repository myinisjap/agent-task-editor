package handlers

import (
	"fmt"
	"io"
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
// the stored relative paths ("<task_id>/<filename>"). On any validation or I/O
// error it writes the error response itself (matching the exact status codes
// the inline version used to return) and returns ok=false; callers should
// return immediately in that case without writing any further response.
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

		// Read first 512 bytes for content sniffing
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		detectedType := http.DetectContentType(buf[:n])
		if !strings.HasPrefix(detectedType, "image/") {
			Err(w, http.StatusBadRequest, fmt.Sprintf("file %q is not an image (detected: %s)", fh.Filename, detectedType))
			return nil, false
		}
		// Seek back to start for full copy
		if _, err := f.(io.Seeker).Seek(0, io.SeekStart); err != nil {
			Err(w, http.StatusInternalServerError, "failed to seek uploaded file")
			return nil, false
		}

		// Build safe filename: UUID + original extension
		ext := filepath.Ext(fh.Filename)
		if ext == "" {
			ext = ".bin"
		}
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
		dst, err := os.Create(dstPath)
		if err != nil {
			Err(w, http.StatusInternalServerError, "failed to create upload file")
			return nil, false
		}
		if _, err := io.Copy(dst, f); err != nil {
			dst.Close() //nolint:errcheck
			Err(w, http.StatusInternalServerError, "failed to write upload file")
			return nil, false
		}
		dst.Close() //nolint:errcheck

		// Store as relative path: "<task_id>/<filename>"
		attachmentPaths = append(attachmentPaths, filepath.Join(taskID, safeFilename))
	}
	return attachmentPaths, true
}
