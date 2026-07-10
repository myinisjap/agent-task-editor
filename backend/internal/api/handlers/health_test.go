package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestHealth_ReturnsOKJSON(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.Healthz(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", body["status"])
	}
	if body["version"] != "v1.2.3" {
		t.Errorf("expected version 'v1.2.3', got %q", body["version"])
	}
}

// TestHealth_DefaultsVersionToDev verifies /healthz reports "dev" when no
// version was injected (matches cmd/server's Version = "dev" default for
// unstamped local builds).
func TestHealth_DefaultsVersionToDev(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "", false)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.Healthz(w, req)

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["version"] != "dev" {
		t.Errorf("expected version 'dev', got %q", body["version"])
	}
}
