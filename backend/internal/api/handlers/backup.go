package handlers

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
)

// BackupHandler serves a consistent point-in-time snapshot of the SQLite
// database via VACUUM INTO — safe to run under concurrent write load, unlike
// a raw copy of a WAL-mode database file.
type BackupHandler struct {
	db *storage.DB
}

// NewBackupHandler constructs a BackupHandler for the given database.
func NewBackupHandler(db *storage.DB) *BackupHandler {
	return &BackupHandler{db: db}
}

// Backup streams a fresh VACUUM INTO snapshot of the database to the
// response as application/octet-stream.
//
// GET /api/v1/backup
func (h *BackupHandler) Backup(w http.ResponseWriter, r *http.Request) {
	log := slog.With("component", "backup")

	// Target the same directory as the live DB file (not the OS temp dir) so
	// the snapshot lands on the same filesystem — this avoids slow/failing
	// cross-device operations in constrained containers, and /data (the DB's
	// directory) is guaranteed writable by the running user.
	dir := filepath.Dir(h.db.Path())
	tmpPath := filepath.Join(dir, fmt.Sprintf("agent-task-editor-backup-%s.db", uuid.NewString()))

	// VACUUM INTO refuses to overwrite an existing file — tmpPath is a
	// fresh, unique name so this is just a defensive cleanup, not expected
	// to remove anything.
	_ = os.Remove(tmpPath)
	defer func() { _ = os.Remove(tmpPath) }()

	if err := h.db.Backup(r.Context(), tmpPath); err != nil {
		log.Error("backup failed", "err", err)
		Err(w, http.StatusInternalServerError, "backup failed")
		return
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		log.Error("backup: open snapshot failed", "err", err)
		Err(w, http.StatusInternalServerError, "backup failed")
		return
	}
	defer func() { _ = f.Close() }()

	filename := fmt.Sprintf("agent-task-editor-backup-%s.db", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if fi, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size()))
	}
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, f); err != nil {
		log.Warn("backup: stream failed", "err", err)
	}
}
