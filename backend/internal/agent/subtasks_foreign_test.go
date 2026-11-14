package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestForeignHelpers(t *testing.T) {
	pid := "p1"
	parent := gen.Task{ID: pid, RepoID: "A"}
	mk := func(repo string, parentID *string) gen.Task {
		return gen.Task{ID: "c", RepoID: repo, ParentTaskID: parentID}
	}
	if !IsForeignChild(parent, mk("B", &pid)) {
		t.Error("different repo should be foreign")
	}
	if IsForeignChild(parent, mk("A", &pid)) {
		t.Error("same repo is a home child")
	}
	if IsForeignChild(parent, mk("B", nil)) {
		t.Error("no parent is never foreign")
	}
	other := "other"
	if IsForeignChild(parent, mk("B", &other)) {
		t.Error("child of a different parent is not foreign to this one")
	}
	if IsForeignRepo("A", "") || IsForeignRepo("A", "A") || !IsForeignRepo("A", "B") {
		t.Error("IsForeignRepo")
	}
	for in, want := range map[string]string{"": "pending", "pushed": "pending", "pr_open": "pr_open", "pr_merged": "merged", "pr_closed": "pr_closed"} {
		if got := ForeignChildMergeStatus(in); got != want {
			t.Errorf("ForeignChildMergeStatus(%q)=%q want %q", in, got, want)
		}
	}
}

// setupForeignChild adds a second repo and a foreign child (with a committed
// file on its own worktree/branch) under the env's parent.
func setupForeignChild(t *testing.T, q *gen.Queries, parent gen.Task) (gen.Task, gen.Repo) {
	t.Helper()
	ctx := context.Background()
	repoB := initRepo(t)
	wfID := parent.WorkflowID
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: uuid.NewString(), Name: "b", Path: repoB, WorkflowID: &wfID})
	if err != nil {
		t.Fatal(err)
	}
	child, err := q.CreateSubtask(ctx, gen.CreateSubtaskParams{
		ID: uuid.NewString(), Title: "fchild", Type: "feature", Label: "done", RepoID: repo.ID, WorkflowID: wfID, ParentTaskID: &parent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	wt, br, base, err := provisionWorktree(ctx, repoB, child.ID, child.Title)
	if err != nil {
		t.Fatal(err)
	}
	_ = q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: br, WorktreePath: wt, BaseRef: base, ID: child.ID})
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "f")
	child, _ = q.GetTask(ctx, child.ID)
	return child, repo
}

func TestCoordinator_ForeignOpenPRErrorRetries(t *testing.T) {
	coord, q, repoA, parent, _ := setupCoordEnv(t)
	ctx := context.Background()
	child, repoB := setupForeignChild(t, q, parent)

	calls := 0
	coord.OpenPR = func(ctx context.Context, task gen.Task, r gen.Repo) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("forge down")
		}
		_, err := q.SetTaskPR(ctx, gen.SetTaskPRParams{GitState: "pr_open", PrUrl: "u", ID: task.ID})
		return "pr_open", err
	}

	if err := coord.OnChildTerminal(ctx, child, repoB); err == nil {
		t.Fatal("expected OpenPR error to be returned for outbox retry")
	}
	got, _ := q.GetTask(ctx, child.ID)
	if got.MergeStatus != "pending" {
		t.Fatalf("merge_status=%q want pending", got.MergeStatus)
	}
	if got.WorktreePath == "" {
		t.Fatal("worktree must be kept on failure")
	}
	if err := coord.OnChildTerminal(ctx, got, repoB); err != nil {
		t.Fatalf("retry: %v", err)
	}
	got, _ = q.GetTask(ctx, child.ID)
	if got.MergeStatus != "pr_open" || got.WorktreePath != "" {
		t.Fatalf("after retry merge=%q wt=%q", got.MergeStatus, got.WorktreePath)
	}
	// Parent repo untouched: no merge of the foreign file.
	if sliceHas(lsTree(t, repoA, parent.Branch), "f.txt") {
		t.Fatal("foreign file landed on the parent branch")
	}
}

func TestCoordinator_AfterParentRunSkipsForeign(t *testing.T) {
	coord, q, repoA, parent, _ := setupCoordEnv(t)
	ctx := context.Background()
	child, _ := setupForeignChild(t, q, parent)
	coord.setMerge(ctx, child.ID, "pending")

	coord.AfterParentRun(ctx, parent.ID, repoA, true, true)

	got, _ := q.GetTask(ctx, child.ID)
	if got.MergeStatus != "pending" {
		t.Fatalf("foreign child merge_status=%q, want untouched pending", got.MergeStatus)
	}
	if sliceHas(lsTree(t, repoA, parent.Branch), "f.txt") {
		t.Fatal("foreign file merged into parent branch")
	}
}

func TestDispatcher_ParentBranchBaseForeign(t *testing.T) {
	coord, q, _, parent, home := setupCoordEnv(t)
	foreign, _ := setupForeignChild(t, q, parent)
	d := &Dispatcher{q: coord.q}
	ctx := context.Background()
	if got := d.parentBranchBase(ctx, foreign); got != "" {
		t.Fatalf("foreign base = %q, want empty", got)
	}
	if got := d.parentBranchBase(ctx, home); got != parent.Branch {
		t.Fatalf("home base = %q, want %q", got, parent.Branch)
	}
	if !d.isForeignChild(ctx, foreign) || d.isForeignChild(ctx, home) {
		t.Fatal("isForeignChild")
	}
}
