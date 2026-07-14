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

// TestBackupSettingsHandler_GetDefaults verifies the seeded migration row
// surfaces the documented out-of-the-box defaults (once a day / keep 7)
// before any PUT has ever been made.
func TestBackupSettingsHandler_GetDefaults(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewBackupSettingsHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/backup/settings", nil)
	w := httptest.NewRecorder()
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		IntervalSeconds int64 `json:"interval_seconds"`
		Keep            int   `json:"keep"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.IntervalSeconds != 86400 {
		t.Errorf("expected default interval_seconds=86400 (once a day), got %d", resp.IntervalSeconds)
	}
	if resp.Keep != 7 {
		t.Errorf("expected default keep=7, got %d", resp.Keep)
	}
}

// TestBackupSettingsHandler_UpdateRejectsBelowMinimum verifies the 10-minute
// (600s) floor is enforced.
func TestBackupSettingsHandler_UpdateRejectsBelowMinimum(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewBackupSettingsHandler(q)

	body := strings.NewReader(`{"interval_seconds": 599, "keep": 7}`)
	req := httptest.NewRequest(http.MethodPut, "/backup/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "10 minutes") {
		t.Errorf("expected error message to mention the 10 minute floor, got %q", w.Body.String())
	}
}

// TestBackupSettingsHandler_UpdateRejectsInvalidKeep verifies keep must be
// at least 1.
func TestBackupSettingsHandler_UpdateRejectsInvalidKeep(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewBackupSettingsHandler(q)

	body := strings.NewReader(`{"interval_seconds": 3600, "keep": 0}`)
	req := httptest.NewRequest(http.MethodPut, "/backup/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestBackupSettingsHandler_UpdatePersists verifies a valid update is
// persisted and reflected back by a subsequent Get.
func TestBackupSettingsHandler_UpdatePersists(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewBackupSettingsHandler(q)

	body := strings.NewReader(`{"interval_seconds": 3600, "keep": 3}`)
	req := httptest.NewRequest(http.MethodPut, "/backup/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/backup/settings", nil)
	getW := httptest.NewRecorder()
	h.Get(getW, getReq)

	var resp struct {
		IntervalSeconds int64 `json:"interval_seconds"`
		Keep            int   `json:"keep"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.IntervalSeconds != 3600 {
		t.Errorf("expected interval_seconds=3600 after update, got %d", resp.IntervalSeconds)
	}
	if resp.Keep != 3 {
		t.Errorf("expected keep=3 after update, got %d", resp.Keep)
	}
}

// TestBackupSettingsHandler_UpdateRejectsMalformedBody verifies a
// non-JSON body is rejected with 400 rather than panicking.
func TestBackupSettingsHandler_UpdateRejectsMalformedBody(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewBackupSettingsHandler(q)

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPut, "/backup/settings", body)
	w := httptest.NewRecorder()
	h.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
