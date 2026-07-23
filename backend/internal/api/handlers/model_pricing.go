package handlers

import (
	"database/sql"
	"net/http"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ModelPricingHandler owns the user-editable per-model USD pricing table
// (model_pricing, see migration 042) used to estimate anthropic/llm run
// costs (internal/agent/providers/pricing.go's DBPriceResolver). Any model
// not present here falls back to the hardcoded map in pricing.go; a run
// whose model matches neither has its cost flagged unknown (AgentRun.
// cost_unknown) rather than silently reported as a genuine $0.
type ModelPricingHandler struct {
	q  *gen.Queries
	db *sql.DB
}

func NewModelPricingHandler(q *gen.Queries, db *sql.DB) *ModelPricingHandler {
	return &ModelPricingHandler{q: q, db: db}
}

// modelPricingRow is the wire shape for one model_pricing row.
type modelPricingRow struct {
	Model       string  `json:"model"`
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
}

// List returns every user-defined pricing row, ordered by model.
//
// GET /api/v1/settings/pricing
func (h *ModelPricingHandler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListModelPricing(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []gen.ModelPricing{}
	}
	JSON(w, http.StatusOK, rows)
}

// Update replaces the entire pricing table with the submitted rows
// (add/remove/edit are all expressed client-side as a new full list),
// inside a transaction so a validation failure or mid-write error can't
// leave the table partially cleared. Rejects empty model names, duplicate
// models, and negative prices.
//
// PUT /api/v1/settings/pricing
func (h *ModelPricingHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body []modelPricingRow
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}

	seen := make(map[string]bool, len(body))
	for _, row := range body {
		if row.Model == "" {
			Err(w, http.StatusBadRequest, "model must not be empty")
			return
		}
		if seen[row.Model] {
			Err(w, http.StatusBadRequest, "duplicate model \""+row.Model+"\"")
			return
		}
		seen[row.Model] = true
		if row.InputPer1M < 0 || row.OutputPer1M < 0 {
			Err(w, http.StatusBadRequest, "prices must not be negative")
			return
		}
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)

	if err := tq.DeleteAllModelPricing(ctx); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, row := range body {
		if _, err := tq.UpsertModelPricing(ctx, gen.UpsertModelPricingParams{
			Model:       row.Model,
			InputPer1m:  row.InputPer1M,
			OutputPer1m: row.OutputPer1M,
		}); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows, err := h.q.ListModelPricing(ctx)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []gen.ModelPricing{}
	}
	JSON(w, http.StatusOK, rows)
}
