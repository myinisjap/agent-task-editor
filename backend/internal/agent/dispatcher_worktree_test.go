package agent

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestEnsureWorktree_StaleWorktreePath_Reprovisions is the direct regression
// test for the archive/unarchive stale-worktree_path bug: a task's
// worktree_path can point at a directory that no longer exists — because
// archiving reclaimed it (see api/handlers.reclaimWorktreeOnArchive, which
// now also clears worktree_path, but this must hold even if that clear were
// ever skipped/lost) or because the periodic sweeper reclaimed it out from
// under the task without ever touching this DB row (internal/worktreesweep).
// Previously ensureWorktree only reprovisioned when WorktreePath == "",
// so a stale-but-non-empty path was handed straight to the agent as its cwd
// — a directory that doesn't exist. ensureWorktree must instead detect the
// missing directory and reprovision a fresh worktree.
func TestEnsureWorktree_StaleWorktreePath_Reprovisions(t *testing.T) {
	repo := initRepo(t)

	f, err := os.CreateTemp("", "dispatcher-worktree-*.db")
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
	repoRow, err := q.GetRepo(ctx, repoID)
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}

	task, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Archived then redispatched", Type: "feature", Label: "plan", RepoID: repoID, WorkflowID: wfID, Attachments: "[]",
	})

	// Provision a worktree, then simulate archiving having reclaimed it
	// (directory removed) while a stale worktree_path lingers in the DB —
	// exactly the pre-fix state, and also exactly what the sweeper produces
	// on its own (it never touches this row at all).
	staleWtPath, branch, baseRef, err := provisionWorktree(ctx, repo, task.ID, task.Title)
	if err != nil {
		t.Fatalf("provision worktree: %v", err)
	}
	if err := q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: branch, WorktreePath: staleWtPath, BaseRef: baseRef, ID: task.ID}); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	if err := RemoveWorktree(ctx, repo, staleWtPath); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if _, statErr := os.Stat(staleWtPath); !os.IsNotExist(statErr) {
		t.Fatalf("precondition failed: worktree dir should be gone, stat err=%v", statErr)
	}

	task, err = q.GetTask(ctx, task.ID) // worktree_path still stale/non-empty at this point
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.WorktreePath != staleWtPath {
		t.Fatalf("precondition failed: expected stale worktree_path %q, got %q", staleWtPath, task.WorktreePath)
	}

	d := &Dispatcher{q: q}
	workDir, err := d.ensureWorktree(ctx, task, repoRow)
	if err != nil {
		t.Fatalf("ensureWorktree: %v", err)
	}
	if workDir == staleWtPath && !dirIsReal(t, workDir) {
		t.Fatalf("ensureWorktree returned the stale, deleted path instead of reprovisioning: %q", workDir)
	}
	if fi, statErr := os.Stat(workDir); statErr != nil || !fi.IsDir() {
		t.Fatalf("expected ensureWorktree's returned dir to exist: stat err=%v", statErr)
	}

	// The task's DB row must also be updated to the freshly reprovisioned path
	// so the next call (or a real dispatch) doesn't redo this work.
	reloaded, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("reload task after ensureWorktree: %v", err)
	}
	if reloaded.WorktreePath != workDir {
		t.Errorf("expected task.worktree_path updated to %q, got %q", workDir, reloaded.WorktreePath)
	}
}

// TestEnsureWorktree_ExistingWorktreePath_Reused verifies the normal,
// non-stale case is unaffected by the added stat check: when
// WorktreePath already points at a real directory, ensureWorktree returns it
// as-is without reprovisioning.
func TestEnsureWorktree_ExistingWorktreePath_Reused(t *testing.T) {
	repo := initRepo(t)

	f, err := os.CreateTemp("", "dispatcher-worktree-*.db")
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
	repoRow, err := q.GetRepo(ctx, repoID)
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}

	task, _ := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Live worktree", Type: "feature", Label: "plan", RepoID: repoID, WorkflowID: wfID, Attachments: "[]",
	})
	wtPath, branch, baseRef, err := provisionWorktree(ctx, repo, task.ID, task.Title)
	if err != nil {
		t.Fatalf("provision worktree: %v", err)
	}
	if err := q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: branch, WorktreePath: wtPath, BaseRef: baseRef, ID: task.ID}); err != nil {
		t.Fatalf("set worktree: %v", err)
	}
	task, _ = q.GetTask(ctx, task.ID)

	d := &Dispatcher{q: q}
	workDir, err := d.ensureWorktree(ctx, task, repoRow)
	if err != nil {
		t.Fatalf("ensureWorktree: %v", err)
	}
	if workDir != wtPath {
		t.Errorf("expected the existing worktree path %q reused as-is, got %q", wtPath, workDir)
	}
}

// dirIsReal reports whether p is a real, existing directory — used to
// disambiguate the (extremely unlikely but not impossible) case where
// reprovisioning happens to produce the exact same path string as the stale
// one, which is fine as long as the directory genuinely exists again.
func dirIsReal(t *testing.T, p string) bool {
	t.Helper()
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
