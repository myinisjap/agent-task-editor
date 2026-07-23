package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestModelPricingHandler_ListSeeded verifies migration 042 seeded the
// hardcoded pricing table into the DB, so a fresh install's GET already
// reflects the same estimates as before this feature (no behavior change on
// upgrade until a user edits a row).
func TestModelPricingHandler_ListSeeded(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewModelPricingHandler(q, db.SQL())

	req := httptest.NewRequest(http.MethodGet, "/settings/pricing", nil)
	w := httptest.NewRecorder()
	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rows []gen.ModelPricing
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected seeded pricing rows, got none")
	}
	found := false
	for _, r := range rows {
		if r.Model == "claude-sonnet-4-5" {
			found = true
			if r.InputPer1m != 3 || r.OutputPer1m != 15 {
				t.Errorf("unexpected seeded price for claude-sonnet-4-5: in=%v out=%v", r.InputPer1m, r.OutputPer1m)
			}
		}
	}
	if !found {
		t.Error("expected seeded row for claude-sonnet-4-5")
	}
}

// TestModelPricingHandler_UpdateReplacesTable verifies PUT fully replaces
// the table contents (add + remove expressed as a new full list) and the
// response reflects the new set.
func TestModelPricingHandler_UpdateReplacesTable(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewModelPricingHandler(q, db.SQL())

	body, _ := json.Marshal([]map[string]any{
		{"model": "my-custom-model", "input_per_1m": 1.5, "output_per_1m": 6},
	})
	req := httptest.NewRequest(http.MethodPut, "/settings/pricing", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var rows []gen.ModelPricing
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(rows) != 1 || rows[0].Model != "my-custom-model" {
		t.Fatalf("expected table replaced with exactly the submitted row, got %+v", rows)
	}
	if rows[0].InputPer1m != 1.5 || rows[0].OutputPer1m != 6 {
		t.Errorf("unexpected price: in=%v out=%v", rows[0].InputPer1m, rows[0].OutputPer1m)
	}

	// Old seeded rows should be gone.
	got, err := q.GetModelPricing(req.Context(), "claude-sonnet-4-5")
	if err == nil {
		t.Errorf("expected old seeded row to be replaced away, still found: %+v", got)
	}
}

// TestModelPricingHandler_UpdateRejectsInvalid verifies validation: empty
// model, duplicate model, and negative prices are all rejected with 400 and
// leave the table untouched (transactional).
func TestModelPricingHandler_UpdateRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty model", `[{"model": "", "input_per_1m": 1, "output_per_1m": 1}]`},
		{"duplicate model", `[{"model": "x", "input_per_1m": 1, "output_per_1m": 1}, {"model": "x", "input_per_1m": 2, "output_per_1m": 2}]`},
		{"negative input price", `[{"model": "x", "input_per_1m": -1, "output_per_1m": 1}]`},
		{"negative output price", `[{"model": "x", "input_per_1m": 1, "output_per_1m": -1}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			q := gen.New(db.SQL())
			h := handlers.NewModelPricingHandler(q, db.SQL())

			req := httptest.NewRequest(http.MethodPut, "/settings/pricing", bytes.NewReader([]byte(tc.body)))
			w := httptest.NewRecorder()
			h.Update(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}

			// Table should be untouched — seeded rows still present.
			rows, err := q.ListModelPricing(req.Context())
			if err != nil {
				t.Fatalf("list after rejected update: %v", err)
			}
			if len(rows) == 0 {
				t.Error("expected seeded rows to remain after a rejected update")
			}
		})
	}
}
