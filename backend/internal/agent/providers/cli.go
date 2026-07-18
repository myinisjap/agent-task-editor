package providers

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// dangerousEnvKeys blocks user-supplied agent env vars from hijacking process execution.
var dangerousEnvKeys = map[string]bool{
	"PATH": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true,
	"HOME": true, "SHELL": true, "IFS": true,
	"DYLD_INSERT_LIBRARIES": true, "DYLD_LIBRARY_PATH": true,
}

func mergeEnv(base []string, extra map[string]string) []string {
	out := make([]string, len(base))
	copy(out, base)
	for k, v := range extra {
		if dangerousEnvKeys[strings.ToUpper(k)] {
			slog.Warn("agent env: blocked dangerous key", "key", k)
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

// rawDump is a dev-only tee of raw stdout stream-json lines, gated by the
// AGENT_RAW_LOG_DIR env var. When unset, all methods are no-ops on a nil
// receiver so the hot path stays clean. Used to review provider output and
// improve our stream parsing — not a product feature.
type rawDump struct {
	f *os.File
}

func openRawDump(runID string) *rawDump {
	dir := os.Getenv("AGENT_RAW_LOG_DIR")
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("raw log capture: mkdir failed", "dir", dir, "err", err)
		return nil
	}
	f, err := os.Create(filepath.Join(dir, runID+".jsonl"))
	if err != nil {
		slog.Warn("raw log capture: create failed", "err", err)
		return nil
	}
	return &rawDump{f: f}
}

func (d *rawDump) WriteLine(line string) {
	if d == nil {
		return
	}
	_, _ = d.f.WriteString(line + "\n")
}

func (d *rawDump) Close() {
	if d == nil {
		return
	}
	_ = d.f.Close()
}
