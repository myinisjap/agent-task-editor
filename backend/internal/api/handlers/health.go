// Package handlers implements the HTTP request handlers for all API routes.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Healthz is a liveness probe: always 200 OK, reporting the running build's
// version ("dev" for unstamped local builds, or the release tag for images
// built with -ldflags "-X main.Version=..."). Deliberately simple/fast — it
// must never block on network or external processes (see HealthHandler.
// Providers for the richer, gh-shelling checks rendered on the Health page,
// and Readyz for the DB/dispatcher-backed readiness probe).
//
// GET /healthz
func (h *HealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	v := h.version
	if v == "" {
		v = "dev"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": v,
	})
}

// dispatcherStaleAfter is how long the dispatch loop can go without
// beginning a new sweep tick before Readyz considers it wedged. The sweep
// interval is 5s (see agent.NewDispatcher); this threshold is a comfortable
// multiple of that (well above what a single slow-but-healthy sweep would
// take) so a momentary slow sweep doesn't flap the readiness probe. If the
// sweep interval is ever made configurable, this should scale with it.
const dispatcherStaleAfter = 30 * time.Second

// readyzDBTimeout bounds how long Readyz waits on the DB ping so a wedged
// database can't hang the readiness probe indefinitely.
const readyzDBTimeout = 2 * time.Second

// Readyz is a readiness probe: unlike Healthz (a static liveness stub), it
// actually verifies the backend can do useful work — the DB is reachable and
// the dispatch loop is still ticking — before reporting 200. Docker/other
// orchestrators poll this (rather than /healthz) so a backend with a locked
// SQLite file or a wedged dispatch loop is reported unhealthy and restarted,
// instead of appearing healthy forever. Mounted outside BearerAuth, same as
// /healthz, so orchestrators can probe without API_TOKEN.
//
// GET /readyz
func (h *HealthHandler) Readyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), readyzDBTimeout)
		defer cancel()
		if err := h.db.SQL().PingContext(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "unhealthy",
				"db":     "error",
				"detail": err.Error(),
			})
			return
		}
	}

	if h.dispatcher != nil {
		last := h.dispatcher.LastSweep()
		if last.IsZero() || time.Since(last) > dispatcherStaleAfter {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":     "unhealthy",
				"dispatcher": "stale",
			})
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	v := h.version
	if v == "" {
		v = "dev"
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": v,
	})
}
