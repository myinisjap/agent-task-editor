package providers

import "testing"

// TestShouldFallBackToColdStart verifies the resume-failure heuristic: an
// explicit session-not-found signal, or an error exit before any stream
// output, retries cold; a normal failure mid-conversation does not.
func TestShouldFallBackToColdStart(t *testing.T) {
	cases := []struct {
		name string
		info attemptInfo
		want bool
	}{
		{"explicit resume error", attemptInfo{resumeError: true}, true},
		{"error exit before any stream output", attemptInfo{exitedWithError: true}, true},
		{"error exit mid-conversation", attemptInfo{exitedWithError: true, sawStream: true}, false},
		{"clean run", attemptInfo{sawStream: true}, false},
	}
	for _, tc := range cases {
		if got := shouldFallBackToColdStart(tc.info); got != tc.want {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, got)
		}
	}
}

func TestIsResumeErrorLine(t *testing.T) {
	if !isResumeErrorLine("No conversation found with session ID: sess-1") {
		t.Error("expected session-not-found line to match")
	}
	if !isResumeErrorLine("Error: session sess-1 not found") {
		t.Error("expected session/not-found combination to match")
	}
	if isResumeErrorLine("File not found: main.go") {
		t.Error("unrelated not-found line must not match")
	}
}
