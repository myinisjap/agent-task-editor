// Package handlers implements the HTTP request handlers for all API routes.
package handlers

import (
	"encoding/json"
	"net/http"
	"runtime/debug"
)

// version reads the VCS revision Go embeds at build time. No -ldflags needed:
// `go build`/`go run` populate vcs.revision and vcs.modified automatically.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, modified := "unknown", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				modified = "-dirty"
			}
		}
	}
	return rev + modified
}

func Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": version(),
	})
}
