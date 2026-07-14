package cronexpr

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) Schedule {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q) error: %v", expr, err)
	}
	return s
}

func TestParse_Presets(t *testing.T) {
	cases := []string{
		"0 * * * *",    // hourly
		"0 6 * * *",    // daily at 06:00
		"0 6 * * 1",    // weekly Monday 06:00
		"*/15 * * * *", // every 15 minutes
	}
	for _, expr := range cases {
		if _, err := Parse(expr); err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", expr, err)
		}
	}
}

func TestParse_Invalid(t *testing.T) {
	cases := []string{
		"* * * *",     // too few fields
		"* * * * * *", // too many fields
		"60 * * * *",  // minute out of range
		"* 24 * * *",  // hour out of range
		"* * 32 * *",  // dom out of range
		"* * * 13 *",  // month out of range
		"* * * * 7",   // dow out of range
		"abc * * * *", // non-numeric
		"*/0 * * * *", // zero step
	}
	for _, expr := range cases {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", expr)
		}
	}
}

func TestNext_Hourly(t *testing.T) {
	s := mustParse(t, "0 * * * *")
	after := time.Date(2026, 7, 13, 10, 15, 0, 0, time.UTC)
	got := s.Next(after)
	want := time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}
}

func TestNext_DailyAt0600(t *testing.T) {
	s := mustParse(t, "0 6 * * *")

	// Before 06:00 same day -> fires today.
	after := time.Date(2026, 7, 13, 3, 0, 0, 0, time.UTC)
	got := s.Next(after)
	want := time.Date(2026, 7, 13, 6, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}

	// After 06:00 same day -> fires next day.
	after2 := time.Date(2026, 7, 13, 7, 0, 0, 0, time.UTC)
	got2 := s.Next(after2)
	want2 := time.Date(2026, 7, 14, 6, 0, 0, 0, time.UTC)
	if !got2.Equal(want2) {
		t.Errorf("Next(%v) = %v, want %v", after2, got2, want2)
	}
}

func TestNext_WeeklyOnMonday0600(t *testing.T) {
	s := mustParse(t, "0 6 * * 1")
	// 2026-07-13 is a Monday; after 06:00 that Monday should roll to the next Monday.
	after := time.Date(2026, 7, 13, 7, 0, 0, 0, time.UTC)
	got := s.Next(after)
	want := time.Date(2026, 7, 20, 6, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}

	// Before 06:00 Monday -> fires that same Monday.
	after2 := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	got2 := s.Next(after2)
	want2 := time.Date(2026, 7, 13, 6, 0, 0, 0, time.UTC)
	if !got2.Equal(want2) {
		t.Errorf("Next(%v) = %v, want %v", after2, got2, want2)
	}
}

func TestNext_EveryFifteenMinutes(t *testing.T) {
	s := mustParse(t, "*/15 * * * *")
	after := time.Date(2026, 7, 13, 10, 16, 0, 0, time.UTC)
	got := s.Next(after)
	want := time.Date(2026, 7, 13, 10, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", after, got, want)
	}
}

func TestString_ReturnsTrimmedExpr(t *testing.T) {
	s := mustParse(t, "  0 6 * * 1  ")
	if s.String() != "0 6 * * 1" {
		t.Errorf("String() = %q, want %q", s.String(), "0 6 * * 1")
	}
}
