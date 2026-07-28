package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWithinBaseDir_SymlinkEscapeRejected verifies withinBaseDir's
// symlink-resolution branch rejects a path that is lexically inside base but
// whose symlink target actually resolves outside it. The by-name traversal
// case (e.g. "../../etc") is covered by repos_test.go's HTTP-level tests;
// this complements it with the harder-to-catch symlink case (see #247/#248).
func TestWithinBaseDir_SymlinkEscapeRejected(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir() // a sibling temp dir, definitely outside base

	link := filepath.Join(base, "escape-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	if withinBaseDir(link, base) {
		t.Errorf("withinBaseDir(%q, %q) = true, want false (symlink escapes base)", link, base)
	}
}

// TestWithinBaseDir_SymlinkWithinBaseAccepted verifies a symlink that stays
// inside base (points at another location still under base) is accepted.
func TestWithinBaseDir_SymlinkWithinBaseAccepted(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real-dir")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link-to-real")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	if !withinBaseDir(link, base) {
		t.Errorf("withinBaseDir(%q, %q) = false, want true (symlink stays within base)", link, base)
	}
}

// TestWithinBaseDir_PlainPathTraversalRejected is a lower-level sanity check
// alongside repos_test.go's HTTP-level traversal tests: a lexical ".." climb
// with no symlink involved must also be rejected.
func TestWithinBaseDir_PlainPathTraversalRejected(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(filepath.Dir(base), "not-base-at-all")

	if withinBaseDir(outside, base) {
		t.Errorf("withinBaseDir(%q, %q) = true, want false", outside, base)
	}
}

// TestWithinBaseDir_ExactBaseAccepted verifies base itself is considered
// "within" base (the equality branch).
func TestWithinBaseDir_ExactBaseAccepted(t *testing.T) {
	base := t.TempDir()
	if !withinBaseDir(base, base) {
		t.Errorf("withinBaseDir(base, base) = false, want true")
	}
}

// TestWithinBaseDir_NonexistentPathFallsBackToLexicalClean verifies that a
// path which doesn't exist on disk (so filepath.EvalSymlinks fails) still
// gets a sane lexical-clean comparison rather than erroring out.
func TestWithinBaseDir_NonexistentPathFallsBackToLexicalClean(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "does", "not", "exist")
	if !withinBaseDir(inside, base) {
		t.Errorf("withinBaseDir(%q, %q) = false, want true (nonexistent path lexically under base)", inside, base)
	}

	outside := filepath.Join(filepath.Dir(base), "also-does-not-exist")
	if withinBaseDir(outside, base) {
		t.Errorf("withinBaseDir(%q, %q) = true, want false", outside, base)
	}
}
