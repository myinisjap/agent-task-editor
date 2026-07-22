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

// TestLogRetentionSettingsHandler_GetDefaults verifies the seeded migration
// row surfaces the documented out-of-the-box defaults (disabled / hourly)
// before any PUT has ever been made.
func TestLogRetentionSettingsHandler_GetDefaults(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewLogRetentionSettingsHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/log-retention/settings", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Days            int   `json:"days"`
		IntervalSeconds int64 `json:"interval_seconds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Days != 0 {
		t.Errorf("expected default days=0 (disabled), got %d", resp.Days)
	}
	if resp.IntervalSeconds != 3600 {
		t.Errorf("expected default interval_seconds=3600 (hourly), got %d", resp.IntervalSeconds)
	}
}

// TestLogRetentionSettingsHandler_UpdateRejectsNegativeDays verifies days
// must be 0 or greater (0 remains a valid "disabled" value).
func TestLogRetentionSettingsHandler_UpdateRejectsNegativeDays(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewLogRetentionSettingsHandler(q)

	body := strings.NewReader(`{"days": -1, "interval_seconds": 3600}`)
	req := httptest.NewRequest(http.MethodPut, "/log-retention/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestLogRetentionSettingsHandler_UpdateRejectsBelowMinimumInterval verifies
// the 1-minute (60s) floor is enforced.
func TestLogRetentionSettingsHandler_UpdateRejectsBelowMinimumInterval(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewLogRetentionSettingsHandler(q)

	body := strings.NewReader(`{"days": 30, "interval_seconds": 59}`)
	req := httptest.NewRequest(http.MethodPut, "/log-retention/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "1 minute") {
		t.Errorf("expected error message to mention the 1 minute floor, got %q", w.Body.String())
	}
}

// TestLogRetentionSettingsHandler_UpdateAllowsZeroDays verifies days=0 is
// accepted (it means "disabled", not invalid).
func TestLogRetentionSettingsHandler_UpdateAllowsZeroDays(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewLogRetentionSettingsHandler(q)

	body := strings.NewReader(`{"days": 0, "interval_seconds": 3600}`)
	req := httptest.NewRequest(http.MethodPut, "/log-retention/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestLogRetentionSettingsHandler_UpdatePersists verifies a valid update is
// persisted and reflected back by a subsequent Get.
func TestLogRetentionSettingsHandler_UpdatePersists(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewLogRetentionSettingsHandler(q)

	body := strings.NewReader(`{"days": 45, "interval_seconds": 7200}`)
	req := httptest.NewRequest(http.MethodPut, "/log-retention/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/log-retention/settings", nil)
	getW := httptest.NewRecorder()
	h.Get(getW, getReq)

	var resp struct {
		Days            int   `json:"days"`
		IntervalSeconds int64 `json:"interval_seconds"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Days != 45 {
		t.Errorf("expected days=45 after update, got %d", resp.Days)
	}
	if resp.IntervalSeconds != 7200 {
		t.Errorf("expected interval_seconds=7200 after update, got %d", resp.IntervalSeconds)
	}
}

// TestLogRetentionSettingsHandler_UpdateRejectsMalformedBody verifies a
// non-JSON body is rejected with 400 rather than panicking.
func TestLogRetentionSettingsHandler_UpdateRejectsMalformedBody(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewLogRetentionSettingsHandler(q)

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPut, "/log-retention/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
