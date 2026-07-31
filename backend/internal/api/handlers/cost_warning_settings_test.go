package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestCostWarningSettingsHandler_GetDefaults verifies the seeded migration
// row surfaces the documented out-of-the-box default (0.8 / 80%) before any
// PUT has ever been made.
func TestCostWarningSettingsHandler_GetDefaults(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewCostWarningSettingsHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/settings/cost-warning", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		WarnRatio float64 `json:"warn_ratio"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.WarnRatio != 0.8 {
		t.Errorf("expected default warn_ratio=0.8, got %v", resp.WarnRatio)
	}
}

// TestCostWarningSettingsHandler_UpdateRejectsOutOfRange verifies warn_ratio
// must be in (0, 1].
func TestCostWarningSettingsHandler_UpdateRejectsOutOfRange(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewCostWarningSettingsHandler(q)

	for _, body := range []string{`{"warn_ratio": 0}`, `{"warn_ratio": -0.1}`, `{"warn_ratio": 1.5}`} {
		req := httptest.NewRequest(http.MethodPut, "/settings/cost-warning", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.Update(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: expected 400, got %d: %s", body, w.Code, w.Body.String())
		}
	}
}

// TestCostWarningSettingsHandler_UpdateAllowsBoundaryOne verifies
// warn_ratio=1 (warn exactly when the budget is fully exhausted) is accepted
// as the upper boundary.
func TestCostWarningSettingsHandler_UpdateAllowsBoundaryOne(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewCostWarningSettingsHandler(q)

	req := httptest.NewRequest(http.MethodPut, "/settings/cost-warning", strings.NewReader(`{"warn_ratio": 1}`))
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCostWarningSettingsHandler_UpdatePersists verifies a valid update is
// persisted and reflected back by a subsequent Get.
func TestCostWarningSettingsHandler_UpdatePersists(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewCostWarningSettingsHandler(q)

	body := strings.NewReader(`{"warn_ratio": 0.5}`)
	req := httptest.NewRequest(http.MethodPut, "/settings/cost-warning", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/settings/cost-warning", nil)
	getW := httptest.NewRecorder()
	h.Get(getW, getReq)

	var resp struct {
		WarnRatio float64 `json:"warn_ratio"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.WarnRatio != 0.5 {
		t.Errorf("expected warn_ratio=0.5 after update, got %v", resp.WarnRatio)
	}
}

// TestCostWarningSettingsHandler_UpdateRejectsMalformedBody verifies a
// non-JSON body is rejected with 400 rather than panicking.
func TestCostWarningSettingsHandler_UpdateRejectsMalformedBody(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewCostWarningSettingsHandler(q)

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPut, "/settings/cost-warning", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
