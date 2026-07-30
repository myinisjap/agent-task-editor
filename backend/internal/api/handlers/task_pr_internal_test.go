package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDirExists covers dirExists' true/false branches: it backs the
// worktree-vs-repo-path fallback used by Diff/PRURL, so a nonexistent or
// non-directory path (e.g. a torn-down worktree) must be correctly reported.
func TestDirExists(t *testing.T) {
	dir := t.TempDir()
	if !dirExists(dir) {
		t.Errorf("expected dirExists(%q) = true for an existing directory", dir)
	}

	nonexistent := filepath.Join(dir, "does-not-exist")
	if dirExists(nonexistent) {
		t.Errorf("expected dirExists(%q) = false for a nonexistent path", nonexistent)
	}

	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if dirExists(file) {
		t.Errorf("expected dirExists(%q) = false for a plain file", file)
	}
}
