package worktreesweep

import (
	"testing"
	"time"
)

// TestNew_WiresIntervalAndQueries verifies the trivial constructor stores
// what it's given.
func TestNew_WiresIntervalAndQueries(t *testing.T) {
	s := New(nil, 5*time.Minute)
	if s == nil {
		t.Fatal("expected New to return a non-nil Sweeper")
	}
	if s.interval != 5*time.Minute {
		t.Errorf("expected interval 5m, got %v", s.interval)
	}
}

// TestCurrentInterval covers currentInterval's floor-at-MinInterval branch
// (a configured interval below the 1-minute floor is clamped up) and the
// pass-through branch (anything at/above the floor is used as-is).
func TestCurrentInterval(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{"below floor is clamped up", 10 * time.Second, MinInterval},
		{"zero is clamped up", 0, MinInterval},
		{"exactly at floor is unchanged", MinInterval, MinInterval},
		{"above floor is unchanged", 10 * time.Minute, 10 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Sweeper{interval: tc.interval}
			if got := s.currentInterval(); got != tc.want {
				t.Errorf("currentInterval() with configured %v = %v, want %v", tc.interval, got, tc.want)
			}
		})
	}
}
