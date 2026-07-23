-- User-editable per-model USD pricing (see 042_model_pricing and
-- internal/agent/providers/pricing.go's dbPriceResolver). Read fresh on
-- every run completion (not cached at startup), so an edit here takes
-- effect on the very next run.

-- name: ListModelPricing :many
SELECT model, input_per_1m, output_per_1m, updated_at FROM model_pricing ORDER BY model;

-- name: GetModelPricing :one
SELECT model, input_per_1m, output_per_1m, updated_at FROM model_pricing WHERE model = ?;

-- name: UpsertModelPricing :one
INSERT INTO model_pricing (model, input_per_1m, output_per_1m, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (model) DO UPDATE SET
    input_per_1m  = excluded.input_per_1m,
    output_per_1m = excluded.output_per_1m,
    updated_at    = CURRENT_TIMESTAMP
RETURNING model, input_per_1m, output_per_1m, updated_at;

-- name: DeleteModelPricing :exec
DELETE FROM model_pricing WHERE model = ?;

-- name: DeleteAllModelPricing :exec
-- Used by the PUT /api/v1/settings/pricing replace-all handler to clear the
-- table before re-inserting the submitted rows, inside a transaction so a
-- mid-write failure can't leave the table partially cleared.
DELETE FROM model_pricing;
