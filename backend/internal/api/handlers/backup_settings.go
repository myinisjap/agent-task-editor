package handlers

import (
	"net/http"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/backup"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// minIntervalSeconds mirrors backup.MinInterval as a whole-seconds floor for
// request validation below — kept in lockstep with the scheduler's own
// floor so a value that passes validation here is never silently clamped
// higher by the scheduler.
const minIntervalSeconds = int64(backup.MinInterval / time.Second)

// BackupSettingsHandler manages the DB-backed settings (interval/retention
// count) for the automatic local-backup scheduler (internal/backup). This
// is separate from whether the scheduler is enabled at all, which remains a
// deploy-time choice (BACKUP_DIR — see docs/backup.md); this handler only
// controls how often it runs and how many snapshots it keeps once enabled.
type BackupSettingsHandler struct {
	q *gen.Queries
}

func NewBackupSettingsHandler(q *gen.Queries) *BackupSettingsHandler {
	return &BackupSettingsHandler{q: q}
}

// backupSettingsResponse is the shape returned by GET/PUT.
type backupSettingsResponse struct {
	IntervalSeconds int64     `json:"interval_seconds"`
	Keep            int       `json:"keep"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Get returns the current backup settings.
//
// GET /api/v1/backup/settings
func (h *BackupSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	row, err := h.q.GetBackupSettings(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, backupSettingsResponse{
		IntervalSeconds: row.IntervalSeconds,
		Keep:            int(row.Keep),
		UpdatedAt:       row.UpdatedAt,
	})
}

// backupSettingsBody is the update request payload.
type backupSettingsBody struct {
	IntervalSeconds int64 `json:"interval_seconds"`
	Keep            int   `json:"keep"`
}

// Update validates and persists new backup settings. The scheduler
// (internal/backup.Scheduler, when NewWithSettingsFunc is used) re-reads
// these on its own timer, so a change here takes effect on the next
// scheduled run without a process restart.
//
// PUT /api/v1/backup/settings
func (h *BackupSettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body backupSettingsBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.IntervalSeconds < minIntervalSeconds {
		Err(w, http.StatusBadRequest, "interval must be at least 10 minutes")
		return
	}
	if body.Keep < 1 {
		Err(w, http.StatusBadRequest, "keep must be at least 1")
		return
	}

	row, err := h.q.UpsertBackupSettings(r.Context(), gen.UpsertBackupSettingsParams{
		IntervalSeconds: body.IntervalSeconds,
		Keep:            int64(body.Keep),
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, backupSettingsResponse{
		IntervalSeconds: row.IntervalSeconds,
		Keep:            int(row.Keep),
		UpdatedAt:       row.UpdatedAt,
	})
}
