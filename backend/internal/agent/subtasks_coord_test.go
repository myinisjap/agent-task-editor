package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type noopPub struct{}

func (noopPub) Publish(string, map[string]any) {}

// gitIn runs a git command in dir, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// setupCoordEnv builds a temp git repo + storage DB with a parent task on
// "work" and one child task, both provisioned with worktrees (child branched
// off the parent's branch). It returns the coordinator, queries, repo path,
// parent and child tasks.
func setupCoordEnv(t *testing.T) (*SubtaskCoordinator, *gen.Queries, string, gen.Task, gen.Task) {
	t.Helper()
	repo := initRepo(t)

	f, err := os.CreateTemp("", "coord-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := storage.SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(ctx)
	wfID := wfs[0].ID
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: repo, WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// Parent task on "work" with a provisioned worktree/branch.
	parent, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "parent", Type: "feature", Label: "work", RepoID: repoID, WorkflowID: wfID, Attachments: "[]",
	})
	pWt, pBranch, pBase, err := provisionWorktree(ctx, repo, parent.ID, parent.Title)
	if err != nil {
		t.Fatalf("provision parent: %v", err)
	}
	_ = q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: pBranch, WorktreePath: pWt, BaseRef: pBase, ID: parent.ID})
	parent, _ = q.GetTask(ctx, parent.ID)

	// Child task branched off the parent's branch.
	child, _ := q.CreateSubtask(ctx, gen.CreateSubtaskParams{
		ID: uuid.NewString(), Title: "child", Description: "", Type: "feature", Label: "done", RepoID: repoID, WorkflowID: wfID, ParentTaskID: &parent.ID,
	})
	cWt, cBranch, cBase, err := provisionWorktreeFrom(ctx, repo, child.ID, child.Title, pBranch)
	if err != nil {
		t.Fatalf("provision child: %v", err)
	}
	_ = q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: cBranch, WorktreePath: cWt, BaseRef: cBase, ID: child.ID})
	child, _ = q.GetTask(ctx, child.ID)

	engine := workflow.New(db.SQL(), noopPub{})
	// Wire OnTerminal so the coordinator can be exercised via the engine too, but
	// the tests call OnChildTerminal directly.
	coord := NewSubtaskCoordinator(q, engine, noopPub{}, "test", "t@example.com")
	return coord, q, repo, parent, child
}

// TestCoordinator_CleanMergeAndAutoAdvance verifies a clean child merge-back
// lands the child's work on the parent's branch, marks the child merged, tears
// down the child worktree, and auto-advances the parent (work → testing).
func TestCoordinator_CleanMergeAndAutoAdvance(t *testing.T) {
	coord, q, repo, parent, child := setupCoordEnv(t)
	ctx := context.Background()

	// Child does some work and commits on its branch.
	if err := os.WriteFile(filepath.Join(child.WorktreePath, "child.txt"), []byte("child work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, child.WorktreePath, "add", "-A")
	gitIn(t, child.WorktreePath, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "child work")

	coord.OnChildTerminal(ctx, child, repo)

	// Child marked merged.
	child, _ = q.GetTask(ctx, child.ID)
	if child.MergeStatus != "merged" {
		t.Fatalf("child merge_status = %q, want merged", child.MergeStatus)
	}
	// The child's file is now on the parent's branch (its worktree).
	if _, err := os.Stat(filepath.Join(parent.WorktreePath, "child.txt")); err != nil {
		t.Fatalf("child work did not land on parent branch: %v", err)
	}
	// Child worktree torn down.
	if _, err := os.Stat(child.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("child worktree should be removed")
	}
	// Parent auto-advanced work → testing (its agent-success transition).
	parent, _ = q.GetTask(ctx, parent.ID)
	if parent.Label != "testing" {
		t.Fatalf("parent should auto-advance to testing, got %q", parent.Label)
	}
}

// TestCoordinator_ConflictFlagsChildNoAdvance verifies a conflicting merge-back
// flags the child merge_conflict, leaves the parent on its label (no
// auto-advance), and surfaces conflict context for the parent's run.
func TestCoordinator_ConflictFlagsChildNoAdvance(t *testing.T) {
	coord, q, repo, parent, child := setupCoordEnv(t)
	ctx := context.Background()

	// Parent branch and child branch both change the same file differently →
	// conflict on merge-back.
	if err := os.WriteFile(filepath.Join(parent.WorktreePath, "clash.txt"), []byte("parent side\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, parent.WorktreePath, "add", "-A")
	gitIn(t, parent.WorktreePath, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "parent change")

	if err := os.WriteFile(filepath.Join(child.WorktreePath, "clash.txt"), []byte("child side\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, child.WorktreePath, "add", "-A")
	gitIn(t, child.WorktreePath, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "child change")

	coord.OnChildTerminal(ctx, child, repo)

	child, _ = q.GetTask(ctx, child.ID)
	if child.MergeStatus != "merge_conflict" {
		t.Fatalf("child merge_status = %q, want merge_conflict", child.MergeStatus)
	}
	parent, _ = q.GetTask(ctx, parent.ID)
	if parent.Label != "work" {
		t.Fatalf("parent should stay on work when a child conflicts, got %q", parent.Label)
	}
	// The parent's worktree must be clean (merge aborted), not mid-merge.
	if _, err := os.Stat(filepath.Join(parent.WorktreePath, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		// .git here is a file (worktree) — check via git status instead.
		out, _ := exec.Command("git", "-C", parent.WorktreePath, "status", "--porcelain").CombinedOutput()
		if len(out) != 0 {
			t.Fatalf("parent worktree left dirty after aborted merge: %s", out)
		}
	}
	// Conflict context is available for the parent's resolution run.
	if cc := coord.BuildConflictContext(ctx, parent.ID); cc == nil || *cc == "" {
		t.Fatalf("expected conflict context for parent")
	}
}

// TestCoordinator_DeferredMergeFlushedAfterParentRun verifies that when the
// parent has a run in flight at the time a child goes terminal, the
// merge-back is deferred (merge_status=pending, nothing lands on the parent
// branch yet) and is flushed by AfterParentRun once the parent's run clears.
func TestCoordinator_DeferredMergeFlushedAfterParentRun(t *testing.T) {
	coord, q, repo, parent, child := setupCoordEnv(t)
	ctx := context.Background()

	// Child does some work and commits on its branch.
	if err := os.WriteFile(filepath.Join(child.WorktreePath, "child.txt"), []byte("child work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, child.WorktreePath, "add", "-A")
	gitIn(t, child.WorktreePath, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "child work")

	// Simulate the parent having a run in flight.
	runID := uuid.NewString()
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{ActiveAgentRunID: &runID, ID: parent.ID}); err != nil {
		t.Fatalf("set parent active run: %v", err)
	}

	coord.OnChildTerminal(ctx, child, repo)

	// Merge-back deferred: child pending, nothing landed on the parent branch.
	child, _ = q.GetTask(ctx, child.ID)
	if child.MergeStatus != "pending" {
		t.Fatalf("child merge_status = %q, want pending", child.MergeStatus)
	}
	if _, err := os.Stat(filepath.Join(parent.WorktreePath, "child.txt")); !os.IsNotExist(err) {
		t.Fatalf("child work should not have landed on parent branch yet")
	}
	parent, _ = q.GetTask(ctx, parent.ID)
	if parent.Label != "work" {
		t.Fatalf("parent should not have auto-advanced while merge is deferred, got %q", parent.Label)
	}

	// Parent's run completes; clear the active run and flush.
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{ActiveAgentRunID: nil, ID: parent.ID}); err != nil {
		t.Fatalf("clear parent active run: %v", err)
	}

	changed := coord.AfterParentRun(ctx, parent.ID, repo, true)
	if !changed {
		t.Fatalf("AfterParentRun should report a change when flushing a deferred merge")
	}

	child, _ = q.GetTask(ctx, child.ID)
	if child.MergeStatus != "merged" {
		t.Fatalf("child merge_status = %q, want merged", child.MergeStatus)
	}
	if _, err := os.Stat(filepath.Join(parent.WorktreePath, "child.txt")); err != nil {
		t.Fatalf("child work did not land on parent branch after flush: %v", err)
	}
	if _, err := os.Stat(child.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("child worktree should be removed after flush")
	}
}

// TestCoordinator_AfterParentRunResolvesConflict verifies that a child left
// in merge_conflict is resolved (marked merged, worktree/branch torn down)
// once the parent's own run succeeds — the run is assumed to have committed
// the conflict resolution directly on the parent branch — and that a failed
// run leaves the child untouched.
func TestCoordinator_AfterParentRunResolvesConflict(t *testing.T) {
	coord, q, repo, parent, child := setupCoordEnv(t)
	ctx := context.Background()

	// Parent branch and child branch both change the same file differently ->
	// conflict on merge-back.
	if err := os.WriteFile(filepath.Join(parent.WorktreePath, "clash.txt"), []byte("parent side\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, parent.WorktreePath, "add", "-A")
	gitIn(t, parent.WorktreePath, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "parent change")

	if err := os.WriteFile(filepath.Join(child.WorktreePath, "clash.txt"), []byte("child side\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, child.WorktreePath, "add", "-A")
	gitIn(t, child.WorktreePath, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "child change")

	coord.OnChildTerminal(ctx, child, repo)

	child, _ = q.GetTask(ctx, child.ID)
	if child.MergeStatus != "merge_conflict" {
		t.Fatalf("child merge_status = %q, want merge_conflict", child.MergeStatus)
	}
	childBranch := child.Branch

	t.Run("failed run leaves child untouched", func(t *testing.T) {
		changed := coord.AfterParentRun(ctx, parent.ID, repo, false)
		if changed {
			t.Fatalf("AfterParentRun should not report a change for a failed run against a conflicted child")
		}
		got, _ := q.GetTask(ctx, child.ID)
		if got.MergeStatus != "merge_conflict" {
			t.Fatalf("child merge_status = %q, want merge_conflict (unchanged)", got.MergeStatus)
		}
	})

	t.Run("successful run resolves the conflict", func(t *testing.T) {
		changed := coord.AfterParentRun(ctx, parent.ID, repo, true)
		if !changed {
			t.Fatalf("AfterParentRun should report a change when resolving a conflicted child")
		}
		got, _ := q.GetTask(ctx, child.ID)
		if got.MergeStatus != "merged" {
			t.Fatalf("child merge_status = %q, want merged", got.MergeStatus)
		}
		if _, err := os.Stat(child.WorktreePath); !os.IsNotExist(err) {
			t.Fatalf("child worktree should be removed after conflict resolution")
		}
		if branchExists(t, repo, childBranch) {
			t.Fatalf("child local branch %q should be deleted after conflict resolution", childBranch)
		}
	})
}

// TestCoordinator_AfterParentRunNoOpForNonParent verifies AfterParentRun is a
// no-op (returns false) both for a task with no children and for an unknown
// task id (the GetTask error branch).
func TestCoordinator_AfterParentRunNoOpForNonParent(t *testing.T) {
	coord, _, repo, _, child := setupCoordEnv(t)
	ctx := context.Background()

	// The child itself has no subtasks of its own.
	if changed := coord.AfterParentRun(ctx, child.ID, repo, true); changed {
		t.Fatalf("AfterParentRun on a task with no children should return false")
	}

	// Unknown task id: GetTask errors out.
	if changed := coord.AfterParentRun(ctx, uuid.NewString(), repo, true); changed {
		t.Fatalf("AfterParentRun on an unknown task id should return false")
	}
}
