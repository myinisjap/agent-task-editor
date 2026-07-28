package handlers

import (
	"net/http"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// CostWarningSettingsHandler manages the DB-backed global warn-ratio setting
// consulted by Dispatcher.checkCostBudget and the provider-side mid-run cost
// watchdog (providers/cost_watchdog.go) — see migration 050_cost_warning.
// warn_ratio is the fraction (0..1] of a task's effective cost budget at
// which a task.cost_warning event fires, ahead of the hard kill/exhaustion at
// 1.0.
type CostWarningSettingsHandler struct {
	q *gen.Queries
}

func NewCostWarningSettingsHandler(q *gen.Queries) *CostWarningSettingsHandler {
	return &CostWarningSettingsHandler{q: q}
}

// costWarningSettingsResponse is the shape returned by GET/PUT.
type costWarningSettingsResponse struct {
	WarnRatio float64   `json:"warn_ratio"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Get returns the current cost-warning threshold settings.
//
// GET /api/v1/settings/cost-warning
func (h *CostWarningSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	row, err := h.q.GetCostWarningSettings(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, costWarningSettingsResponse{
		WarnRatio: row.WarnRatio,
		UpdatedAt: row.UpdatedAt,
	})
}

// costWarningSettingsBody is the update request payload.
type costWarningSettingsBody struct {
	WarnRatio float64 `json:"warn_ratio"`
}

// Update validates and persists a new global cost-warning threshold. Both
// the dispatcher's pre-dispatch warning check and the provider-side mid-run
// watchdog read this fresh on every relevant check (see
// Dispatcher.resolveCostWarnRatio), so a change here takes effect on the
// very next dispatch/run without a process restart.
//
// PUT /api/v1/settings/cost-warning
func (h *CostWarningSettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body costWarningSettingsBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.WarnRatio <= 0 || body.WarnRatio > 1 {
		Err(w, http.StatusBadRequest, "warn_ratio must be greater than 0 and at most 1")
		return
	}

	row, err := h.q.UpsertCostWarningSettings(r.Context(), body.WarnRatio)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, costWarningSettingsResponse{
		WarnRatio: row.WarnRatio,
		UpdatedAt: row.UpdatedAt,
	})
}
