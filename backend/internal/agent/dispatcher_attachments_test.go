package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyFile_CopiesContentByteForByte verifies copyFile (dispatcher.go)
// duplicates the source file's exact bytes to the destination path.
func TestCopyFile_CopiesContentByteForByte(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	src := filepath.Join(srcDir, "photo.png")
	want := []byte{0x89, 0x50, 0x4e, 0x47, 0x01, 0x02, 0x03}
	if err := os.WriteFile(src, want, 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dstDir, "photo.png")

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading copied file: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("copied content = %v, want %v", got, want)
	}
}

// TestCopyFile_NonexistentSourceErrors verifies copyFile surfaces an error
// (rather than silently creating an empty destination) when the source file
// doesn't exist.
func TestCopyFile_NonexistentSourceErrors(t *testing.T) {
	dstDir := t.TempDir()
	err := copyFile(filepath.Join(t.TempDir(), "does-not-exist.png"), filepath.Join(dstDir, "out.png"))
	if err == nil {
		t.Fatal("expected an error for a nonexistent source file")
	}
	if _, statErr := os.Stat(filepath.Join(dstDir, "out.png")); statErr == nil {
		t.Error("copyFile should not have created a destination file on source-open failure")
	}
}

// TestCopyAttachmentsToWorktree_CopiesAllIntoTaskAttachmentsDir verifies
// copyAttachmentsToWorktree (dispatcher.go) creates .task_attachments/ under
// the worktree path and copies every attachment into it, preserving
// filenames. User uploads reaching the agent's worktree were previously
// entirely untested (see #251 §4).
func TestCopyAttachmentsToWorktree_CopiesAllIntoTaskAttachmentsDir(t *testing.T) {
	uploadsDir := t.TempDir()
	worktree := t.TempDir()

	a := filepath.Join(uploadsDir, "one.png")
	b := filepath.Join(uploadsDir, "two.jpg")
	if err := os.WriteFile(a, []byte("AAA"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("BBB"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := copyAttachmentsToWorktree(worktree, []string{a, b}); err != nil {
		t.Fatalf("copyAttachmentsToWorktree: %v", err)
	}

	dst := filepath.Join(worktree, ".task_attachments")
	gotA, err := os.ReadFile(filepath.Join(dst, "one.png"))
	if err != nil || string(gotA) != "AAA" {
		t.Errorf("one.png = %q, %v, want AAA, nil", gotA, err)
	}
	gotB, err := os.ReadFile(filepath.Join(dst, "two.jpg"))
	if err != nil || string(gotB) != "BBB" {
		t.Errorf("two.jpg = %q, %v, want BBB, nil", gotB, err)
	}
}

// TestCopyAttachmentsToWorktree_SkipsMissingFilesWithoutFailingOthers
// verifies that a missing/unreadable source file is skipped (logged, not
// fatal) rather than aborting the whole batch — later attachments must still
// be copied.
func TestCopyAttachmentsToWorktree_SkipsMissingFilesWithoutFailingOthers(t *testing.T) {
	uploadsDir := t.TempDir()
	worktree := t.TempDir()

	ok := filepath.Join(uploadsDir, "exists.png")
	if err := os.WriteFile(ok, []byte("OK"), 0644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(uploadsDir, "missing.png")

	if err := copyAttachmentsToWorktree(worktree, []string{missing, ok}); err != nil {
		t.Fatalf("copyAttachmentsToWorktree should not fail the whole batch on one missing file: %v", err)
	}

	dst := filepath.Join(worktree, ".task_attachments")
	got, err := os.ReadFile(filepath.Join(dst, "exists.png"))
	if err != nil || string(got) != "OK" {
		t.Errorf("exists.png = %q, %v, want OK, nil", got, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "missing.png")); err == nil {
		t.Error("expected no file to be created for the missing source")
	}
}

// TestCopyAttachmentsToWorktree_EmptyListCreatesDirOnly verifies calling
// with an empty attachment list still creates .task_attachments/ (idempotent
// setup) without error.
func TestCopyAttachmentsToWorktree_EmptyListCreatesDirOnly(t *testing.T) {
	worktree := t.TempDir()
	if err := copyAttachmentsToWorktree(worktree, nil); err != nil {
		t.Fatalf("copyAttachmentsToWorktree(nil): %v", err)
	}
	info, err := os.Stat(filepath.Join(worktree, ".task_attachments"))
	if err != nil || !info.IsDir() {
		t.Errorf("expected .task_attachments dir to exist, err=%v", err)
	}
}
