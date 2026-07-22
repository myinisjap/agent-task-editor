package handlers

import (
	"net/http"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/logretention"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// minLogRetentionIntervalSeconds mirrors logretention.MinInterval as a
// whole-seconds floor for request validation below — kept in lockstep with
// the pruner's own floor so a value that passes validation here is never
// silently clamped higher by the pruner.
const minLogRetentionIntervalSeconds = int64(logretention.MinInterval / time.Second)

// LogRetentionSettingsHandler manages the DB-backed settings (retention
// days / run interval) for the automatic agent-log retention pruner
// (internal/logretention). Unlike backup (which has a separate deploy-time
// enable gate, BACKUP_DIR), log retention has no filesystem dependency, so
// this settings row fully controls enable/disable via days (0 = disabled).
type LogRetentionSettingsHandler struct {
	q *gen.Queries
}

func NewLogRetentionSettingsHandler(q *gen.Queries) *LogRetentionSettingsHandler {
	return &LogRetentionSettingsHandler{q: q}
}

// logRetentionSettingsResponse is the shape returned by GET/PUT.
type logRetentionSettingsResponse struct {
	Days            int       `json:"days"`
	IntervalSeconds int64     `json:"interval_seconds"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Get returns the current log retention settings.
//
// GET /api/v1/log-retention/settings
func (h *LogRetentionSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	row, err := h.q.GetLogRetentionSettings(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, logRetentionSettingsResponse{
		Days:            int(row.Days),
		IntervalSeconds: row.IntervalSeconds,
		UpdatedAt:       row.UpdatedAt,
	})
}

// logRetentionSettingsBody is the update request payload.
type logRetentionSettingsBody struct {
	Days            int   `json:"days"`
	IntervalSeconds int64 `json:"interval_seconds"`
}

// Update validates and persists new log retention settings. The pruner
// (internal/logretention.Pruner, when NewWithSettingsFunc is used) re-reads
// these on its own timer, so a change here takes effect on the next
// scheduled run without a process restart.
//
// PUT /api/v1/log-retention/settings
func (h *LogRetentionSettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body logRetentionSettingsBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Days < 0 {
		Err(w, http.StatusBadRequest, "days must be 0 (disabled) or greater")
		return
	}
	if body.IntervalSeconds < minLogRetentionIntervalSeconds {
		Err(w, http.StatusBadRequest, "interval must be at least 1 minute")
		return
	}

	row, err := h.q.UpsertLogRetentionSettings(r.Context(), gen.UpsertLogRetentionSettingsParams{
		Days:            int64(body.Days),
		IntervalSeconds: body.IntervalSeconds,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, logRetentionSettingsResponse{
		Days:            int(row.Days),
		IntervalSeconds: row.IntervalSeconds,
		UpdatedAt:       row.UpdatedAt,
	})
}
