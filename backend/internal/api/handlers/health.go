// Package handlers implements the HTTP request handlers for all API routes.
package handlers

import (
	"encoding/json"
	"net/http"
)

// Healthz is a liveness probe: always 200 OK, reporting the running build's
// version ("dev" for unstamped local builds, or the release tag for images
// built with -ldflags "-X main.Version=..."). Deliberately simple/fast — it
// must never block on network or external processes (see HealthHandler.
// Providers for the richer, gh-shelling checks rendered on the Health page).
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
