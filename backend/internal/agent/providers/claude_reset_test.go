package providers

import (
	"testing"
	"time"
)

// TestParseClaudeResetTime_SessionLimitSample verifies the exact sample
// message from the task, exercising both the same-day and next-day rollover
// cases and the +1 minute retry buffer.
func TestParseClaudeResetTime_SessionLimitSample(t *testing.T) {
	const msg = "You've hit your session limit · resets 6pm (America/Chicago)"
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}

	t.Run("before 6pm same day", func(t *testing.T) {
		now := time.Date(2026, 7, 10, 14, 0, 0, 0, chicago) // 2pm
		got := parseClaudeResetTime(msg, now)
		want := time.Date(2026, 7, 10, 18, 1, 0, 0, chicago)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("after 6pm rolls to next day", func(t *testing.T) {
		now := time.Date(2026, 7, 10, 19, 0, 0, 0, chicago) // 7pm
		got := parseClaudeResetTime(msg, now)
		want := time.Date(2026, 7, 11, 18, 1, 0, 0, chicago)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("exactly at 6pm rolls to next day", func(t *testing.T) {
		now := time.Date(2026, 7, 10, 18, 0, 0, 0, chicago) // exactly 6pm
		got := parseClaudeResetTime(msg, now)
		want := time.Date(2026, 7, 11, 18, 1, 0, 0, chicago)
		if !got.Equal(want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// TestParseClaudeResetTime_WithMinutes verifies a message that includes
// explicit minutes (not just the hour).
func TestParseClaudeResetTime_WithMinutes(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, chicago)
	got := parseClaudeResetTime("Usage limit reached · resets 6:30pm (America/Chicago)", now)
	want := time.Date(2026, 7, 10, 18, 31, 0, 0, chicago)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestParseClaudeResetTime_NoReset verifies a message with no parseable
// reset clue returns the zero time.
func TestParseClaudeResetTime_NoReset(t *testing.T) {
	got := parseClaudeResetTime("Internal error", time.Now())
	if !got.IsZero() {
		t.Errorf("want zero time, got %v", got)
	}
}

// TestParseClaudeResetTime_UnrecognizedTimezoneFallsBackToUTC verifies that
// an unrecognized/garbage timezone name falls back to UTC gracefully rather
// than failing to parse the reset time entirely.
func TestParseClaudeResetTime_UnrecognizedTimezoneFallsBackToUTC(t *testing.T) {
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	got := parseClaudeResetTime("resets 6pm (Not/ARealZone)", now)
	want := time.Date(2026, 7, 10, 18, 1, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestParseClaudeResetTime_NoTimezoneFallsBackToUTC verifies a message with
// no timezone in parentheses at all still parses, using UTC.
func TestParseClaudeResetTime_NoTimezoneFallsBackToUTC(t *testing.T) {
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	got := parseClaudeResetTime("resets 6pm", now)
	want := time.Date(2026, 7, 10, 18, 1, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestParseClaudeResetTime_CaseInsensitive verifies matching is
// case-insensitive for both "resets" and the am/pm marker.
func TestParseClaudeResetTime_CaseInsensitive(t *testing.T) {
	now := time.Date(2026, 7, 10, 10, 0, 0, 0, time.UTC)
	got := parseClaudeResetTime("RESETS 6PM", now)
	want := time.Date(2026, 7, 10, 18, 1, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
