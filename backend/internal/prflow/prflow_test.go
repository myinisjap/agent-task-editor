package prflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestBuildBody(t *testing.T) {
	task := gen.Task{
		Title:       "Fix login redirect",
		Description: "Users land on a blank page after login.",
		AgentNotes:  "Reordered the redirect guard.",
	}
	body := BuildBody(task, []string{"Fix redirect order", "Add regression test"})

	for _, want := range []string{
		"Users land on a blank page",
		"### What changed",
		"Reordered the redirect guard.",
		"### Commits",
		"- Fix redirect order",
		"- Add regression test",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}

	// Empty sections are omitted, not rendered as empty headers.
	bare := BuildBody(gen.Task{Title: "x"}, nil)
	if strings.Contains(bare, "### What changed") || strings.Contains(bare, "### Commits") {
		t.Fatalf("bare body should omit empty sections:\n%q", bare)
	}
}

type fakeStore struct{ called bool }

func (f *fakeStore) SetTaskPR(_ context.Context, _ gen.SetTaskPRParams) (gen.Task, error) {
	f.called = true
	return gen.Task{}, nil
}

func TestCreateForTask_Preconditions(t *testing.T) {
	remote := "https://example.invalid/not/a/forge"
	cases := []struct {
		name string
		task gen.Task
		repo gen.Repo
	}{
		{"no branch", gen.Task{}, gen.Repo{}},
		{"no remote", gen.Task{Branch: "b"}, gen.Repo{}},
		{"unsupported forge", gen.Task{Branch: "b"}, gen.Repo{RemoteUrl: &remote}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			_, _, err := New(store, nil).CreateForTask(context.Background(), tc.task, tc.repo)
			if err == nil {
				t.Fatal("expected error")
			}
			if !IsPrecondition(err) || IsUpstream(err) {
				t.Errorf("expected precondition error, got %v", err)
			}
			if store.called {
				t.Error("store must not be touched on a precondition failure")
			}
		})
	}
}

func TestGitDir(t *testing.T) {
	dir := t.TempDir()
	repo := gen.Repo{Path: "/repo/main"}

	if got := GitDir(gen.Task{WorktreePath: dir}, repo); got != dir {
		t.Errorf("existing worktree: got %q, want %q", got, dir)
	}
	if got := GitDir(gen.Task{WorktreePath: filepath.Join(dir, "gone")}, repo); got != repo.Path {
		t.Errorf("missing worktree: got %q, want repo path", got)
	}
	if got := GitDir(gen.Task{}, repo); got != repo.Path {
		t.Errorf("no worktree: got %q, want repo path", got)
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if got := GitDir(gen.Task{WorktreePath: file}, repo); got != repo.Path {
		t.Errorf("plain file worktree: got %q, want repo path", got)
	}
}

func TestBranchCommits_InvalidRefs(t *testing.T) {
	if got := BranchCommits(context.Background(), t.TempDir(), "--no-index", "main"); got != nil {
		t.Errorf("invalid base ref: got %v, want nil", got)
	}
	if got := BranchCommits(context.Background(), t.TempDir(), "main", "a..b"); got != nil {
		t.Errorf("invalid branch: got %v, want nil", got)
	}
}
