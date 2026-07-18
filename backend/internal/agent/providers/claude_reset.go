package providers

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	// Embeds the IANA time zone database into the compiled binary so
	// time.LoadLocation works regardless of whether the host OS ships
	// /usr/share/zoneinfo (the production container, node:26-alpine, does
	// not by default). Without this, parseClaudeResetTime would silently
	// fall back to UTC for every message on that platform even though the
	// CLI reliably reports an IANA zone name (e.g. "America/Chicago").
	_ "time/tzdata"
)

// resetBuffer is added to the parsed reset time so the agent retries a
// little after the limit actually resets, rather than racing the exact
// moment (which may not have propagated to the API yet).
const resetBuffer = 1 * time.Minute

// claudeResetPattern matches the reset clue in Claude's session/usage-limit
// messages, e.g.:
//
//	"You've hit your session limit · resets 6pm (America/Chicago)"
//	"...resets 6:30pm (America/Chicago)"
//	"resets 6pm" (no timezone — falls back to UTC, see below)
var claudeResetPattern = regexp.MustCompile(`(?i)resets\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)\s*(?:\(([^)]+)\))?`)

// parseClaudeResetTime extracts a reset clock time (and optional IANA time
// zone) from a Claude CLI result message and resolves it to an absolute
// time.Time relative to now, plus a small resetBuffer so retries happen
// slightly after the reset rather than racing it. Returns the zero
// time.Time if resultText carries no recognizable reset clue — callers
// should treat that as "unknown; fall back to exponential backoff".
//
// If the message carries no timezone (or an unrecognized one), this falls
// back to UTC. Anthropic's messages observed in practice always include an
// IANA zone name in parentheses, so this fallback is a defensive default
// rather than the expected path.
func parseClaudeResetTime(resultText string, now time.Time) time.Time {
	m := claudeResetPattern.FindStringSubmatch(resultText)
	if m == nil {
		return time.Time{}
	}

	hour, err := strconv.Atoi(m[1])
	if err != nil || hour < 1 || hour > 12 {
		return time.Time{}
	}
	minute := 0
	if m[2] != "" {
		minute, err = strconv.Atoi(m[2])
		if err != nil || minute < 0 || minute > 59 {
			return time.Time{}
		}
	}
	meridiem := strings.ToLower(m[3])
	hour24 := hour % 12
	if meridiem == "pm" {
		hour24 += 12
	}

	loc := time.UTC
	if tzName := m[4]; tzName != "" {
		if l, err := time.LoadLocation(tzName); err == nil {
			loc = l
		}
	}

	nowInLoc := now.In(loc)
	candidate := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), hour24, minute, 0, 0, loc)
	// Claude's reset message always refers to the *next* occurrence of that
	// wall-clock time. If today's occurrence has already passed (or is
	// exactly now), it must mean tomorrow.
	if !candidate.After(nowInLoc) {
		candidate = candidate.Add(24 * time.Hour)
	}

	return candidate.Add(resetBuffer)
}
