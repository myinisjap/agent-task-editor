package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestHealth_ReturnsOKJSON(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false, nil)

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
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "", false, nil)

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

// fakeDispatcherLiveness lets tests control what /readyz sees as the
// dispatch loop's last-sweep heartbeat.
type fakeDispatcherLiveness struct {
	last time.Time
}

func (f fakeDispatcherLiveness) LastSweep() time.Time { return f.last }

// fakeDispatcherWithCost implements both DispatcherLiveness and
// GlobalCostReporter, letting tests control both the sweep heartbeat and the
// global cost-ceiling status /readyz and /health/providers surface.
type fakeDispatcherWithCost struct {
	last   time.Time
	status agent.GlobalCostStatus
}

func (f fakeDispatcherWithCost) LastSweep() time.Time                     { return f.last }
func (f fakeDispatcherWithCost) GlobalCostStatus() agent.GlobalCostStatus { return f.status }

// TestReadyz_OKWhenDBUpAndDispatcherFresh verifies /readyz reports 200 when
// the DB responds to a ping and the dispatcher's heartbeat is recent.
func TestReadyz_OKWhenDBUpAndDispatcherFresh(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false, fakeDispatcherLiveness{last: time.Now()})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.Readyz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", body["status"])
	}
}

// TestReadyz_UnhealthyWhenDispatcherStale verifies /readyz reports 503 when
// the dispatcher hasn't ticked recently (a wedged dispatch loop).
func TestReadyz_UnhealthyWhenDispatcherStale(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false, fakeDispatcherLiveness{last: time.Now().Add(-time.Hour)})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.Readyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] != "unhealthy" {
		t.Errorf("expected status 'unhealthy', got %q", body["status"])
	}
	if body["dispatcher"] != "stale" {
		t.Errorf("expected dispatcher 'stale', got %q", body["dispatcher"])
	}
}

// TestReadyz_UnhealthyWhenDispatcherNeverSwept verifies /readyz reports 503
// when the dispatcher reports a zero LastSweep (Run never started).
func TestReadyz_UnhealthyWhenDispatcherNeverSwept(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false, fakeDispatcherLiveness{})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.Readyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReadyz_OKWithNilDispatcher verifies /readyz skips the dispatcher check
// entirely when no DispatcherLiveness was wired in (e.g. alternate wiring
// or a test harness), reporting 200 based on DB health alone.
func TestReadyz_OKWithNilDispatcher(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false, nil)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.Readyz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReadyz_UnhealthyWhenDBClosed verifies /readyz reports 503 when the DB
// ping fails (e.g. the underlying connection is closed).
func TestReadyz_UnhealthyWhenDBClosed(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false, fakeDispatcherLiveness{last: time.Now()})

	if err := db.SQL().Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.Readyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["db"] != "error" {
		t.Errorf("expected db 'error', got %q", body["db"])
	}
}

// TestReadyz_SurfacesTrippedGlobalCostBudget verifies /readyz still reports
// 200 (a tripped cap is intentional backpressure, not a broken backend) but
// includes the global_cost_tripped fields so an operator/uptime check can
// see the dispatcher has stopped starting new work.
func TestReadyz_SurfacesTrippedGlobalCostBudget(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	disp := fakeDispatcherWithCost{
		last:   time.Now(),
		status: agent.GlobalCostStatus{Tripped: true, TrippedReason: "daily", DailySpentUSD: 12, DailyLimitUSD: 10},
	}
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false, disp)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.Readyz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (tripped cap is not itself unhealthy), got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if tripped, _ := body["global_cost_tripped"].(bool); !tripped {
		t.Errorf("expected global_cost_tripped=true, got %v", body["global_cost_tripped"])
	}
	if body["global_cost_tripped_reason"] != "daily" {
		t.Errorf("expected global_cost_tripped_reason 'daily', got %v", body["global_cost_tripped_reason"])
	}
}

// TestReadyz_OmitsGlobalCostFieldsWhenNotTripped verifies /readyz doesn't add
// the tripped fields when the global cap (if configured at all) hasn't
// tripped, keeping the common-case response unchanged.
func TestReadyz_OmitsGlobalCostFieldsWhenNotTripped(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	disp := fakeDispatcherWithCost{last: time.Now(), status: agent.GlobalCostStatus{}}
	h := handlers.NewHealthHandler(q, db, "", "", "", "", "", 24*time.Hour, 7, "v1.2.3", false, disp)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	h.Readyz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := body["global_cost_tripped"]; ok {
		t.Errorf("expected global_cost_tripped to be omitted when not tripped, got %v", body["global_cost_tripped"])
	}
}
