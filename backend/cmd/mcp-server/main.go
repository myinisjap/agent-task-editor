package main

import (
	"log/slog"
	"os"
)

// MCP sidecar server — started per agent run.
// Implements the stdio MCP protocol exposing signal_complete and request_human tools.
// Full implementation in Phase 5 (Agent Runtime).
func main() {
	runID := os.Getenv("RUN_ID")
	if runID == "" {
		slog.Error("RUN_ID env var required")
		os.Exit(1)
	}

	slog.Info("mcp sidecar started", "run_id", runID)

	// Phase 5: stdio MCP protocol loop
	select {}
}
