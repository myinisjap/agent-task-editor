package gitcmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsValidRef(t *testing.T) {
	good := []string{"HEAD", "HEAD~2", "HEAD^1", "main", "feat/x-1.2", strings.Repeat("a", 40)}
	bad := []string{"", "--no-index", "a..b", "-x", "a b", "HEAD~", "a;b"}
	for _, r := range good {
		if !IsValidRef(r) {
			t.Errorf("IsValidRef(%q) = false, want true", r)
		}
	}
	for _, r := range bad {
		if IsValidRef(r) {
			t.Errorf("IsValidRef(%q) = true, want false", r)
		}
	}
}

func TestDirExists(t *testing.T) {
	d := t.TempDir()
	if !DirExists(d) {
		t.Error("temp dir should exist")
	}
	if DirExists(filepath.Join(d, "nope")) {
		t.Error("missing path should not exist")
	}
	f := filepath.Join(d, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if DirExists(f) {
		t.Error("plain file should not count as a directory")
	}
}

func TestRunOutputCombined(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	d := t.TempDir()
	if err := Run(ctx, "", "init", "-q", d); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, err := Output(ctx, d, "rev-parse", "--git-dir")
	if err != nil || strings.TrimSpace(string(out)) != ".git" {
		t.Fatalf("Output = %q, %v", out, err)
	}
	if err := Run(ctx, d, "rev-parse", "--verify", "nope"); err == nil {
		t.Error("expected failure for missing ref")
	}
	cout, err := CombinedNoPrompt(ctx, d, "rev-parse", "--verify", "nope")
	if err == nil || len(cout) == 0 {
		t.Errorf("CombinedNoPrompt = %q, %v; want stderr text and error", cout, err)
	}
	if cout, err := Combined(ctx, d, "status", "--porcelain"); err != nil {
		t.Errorf("Combined: %q %v", cout, err)
	}
}
