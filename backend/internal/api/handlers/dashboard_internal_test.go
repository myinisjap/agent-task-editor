package handlers

import "testing"

// TestPercentile90 covers percentile90's nearest-rank interpolation across
// the empty/single/multi-element cases, since SQLite has no percentile
// aggregate and this hand-rolled math backs the dashboard's p90 duration
// stat (see agentConfigStats).
func TestPercentile90(t *testing.T) {
	cases := []struct {
		name   string
		sorted []float64
		want   float64
	}{
		{"empty", nil, 0},
		{"single value", []float64{42}, 42},
		{"two values", []float64{1, 2}, 2},
		{"ten values picks 9th (index 8)", []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 9},
		{"already-sorted ascending, uneven count", []float64{1, 2, 3, 4, 5, 6, 7}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentile90(tc.sorted); got != tc.want {
				t.Errorf("percentile90(%v) = %v, want %v", tc.sorted, got, tc.want)
			}
		})
	}
}
