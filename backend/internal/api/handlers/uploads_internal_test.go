package handlers

import "testing"

// TestIsSafePathComponent covers the directory-traversal guard used by
// UploadsHandler.ServeFile (see #142) — previously untested directly.
func TestIsSafePathComponent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty string rejected", "", false},
		{"single dot rejected", ".", false},
		{"double dot rejected", "..", false},
		{"normal filename accepted", "photo.png", true},
		{"uuid-style filename accepted", "3f9a1c2b-dead-beef.jpg", true},
		{"forward slash rejected", "a/b", false},
		{"leading slash (absolute) rejected", "/etc/passwd", false},
		{"backslash rejected", `a\b`, false},
		{"embedded null byte rejected", "a\x00b", false},
		{"dot in the middle of a filename is fine", "archive.tar.gz", true},
		{"traversal embedded mid-string is fine (no separator)", "..still-not-a-traversal", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSafePathComponent(tc.in)
			if got != tc.want {
				t.Errorf("isSafePathComponent(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
