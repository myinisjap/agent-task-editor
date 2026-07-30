package worktreesweep_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/worktreesweep"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "worktreesweep-test-*.db")
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

	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// initRepo creates a bare-bones git repo with one commit, mirroring
// internal/agent/worktree_test.go's helper.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "init")
	return dir
}

// addWorktree cuts a real git worktree at repo/.ate-worktrees/<id> on branch
// <id>, so the sweeper's git-aware removal path is exercised exactly as it
// would be against a genuine task/session worktree.
func addWorktree(t *testing.T, repo, id string) string {
	t.Helper()
	wtPath := filepath.Join(repo, ".ate-worktrees", id)
	cmd := exec.Command("git", "-C", repo, "worktree", "add", "-b", id, wtPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	return wtPath
}

func TestRunOnce_ReclaimsArchivedTaskWorktree(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()

	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("expected seeded workflow: %v", err)
	}
	wfID := wfs[0].ID

	repoPath := initRepo(t)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:         repoID,
		Name:       "repo-" + repoID,
		Path:       repoPath,
		WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	liveID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: liveID, Title: "live", WorkflowID: wfID, RepoID: repoID, Label: "plan",
	}); err != nil {
		t.Fatalf("create live task: %v", err)
	}
	archivedID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: archivedID, Title: "archived", WorkflowID: wfID, RepoID: repoID, Label: "plan",
	}); err != nil {
		t.Fatalf("create archived task: %v", err)
	}
	if _, err := q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: archivedID}); err != nil {
		t.Fatalf("archive task: %v", err)
	}

	liveWt := addWorktree(t, repoPath, liveID)
	archivedWt := addWorktree(t, repoPath, archivedID)

	s := worktreesweep.New(q, time.Hour)
	if err := s.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if _, err := os.Stat(liveWt); err != nil {
		t.Errorf("live task's worktree should survive: %v", err)
	}
	if _, err := os.Stat(archivedWt); !os.IsNotExist(err) {
		t.Errorf("archived task's worktree should be reclaimed, stat err=%v", err)
	}
}

func TestRunOnce_KeepsChatSessionWorktree(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()

	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("expected seeded workflow: %v", err)
	}
	wfID := wfs[0].ID

	repoPath := initRepo(t)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo-" + repoID, Path: repoPath, WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	pcID := uuid.NewString()
	if _, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
		ID: pcID, Name: "provider-" + pcID, Provider: "mock", Model: "none", Env: `{}`,
	}); err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	sess, err := q.CreateChatSession(ctx, gen.CreateChatSessionParams{
		ID: uuid.NewString(), RepoID: repoID, ProviderConfigID: pcID, Title: "chat",
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	sessWt := addWorktree(t, repoPath, sess.ID)
	orphanID := uuid.NewString()
	orphanWt := addWorktree(t, repoPath, orphanID) // no task/session owns this id

	s := worktreesweep.New(q, time.Hour)
	if err := s.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if _, err := os.Stat(sessWt); err != nil {
		t.Errorf("chat session's worktree should survive: %v", err)
	}
	if _, err := os.Stat(orphanWt); !os.IsNotExist(err) {
		t.Errorf("orphaned worktree should be reclaimed, stat err=%v", err)
	}
}

func TestRunOnce_RemovesCrashOrphanedDirNotRegisteredAsWorktree(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()

	repoPath := initRepo(t)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo-" + repoID, Path: repoPath,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	// A plain directory under .ate-worktrees that was never `git worktree add`-ed
	// (simulating a crash between mkdir and git registering it, or a manually
	// copied-in dir) — git worktree remove will fail on this, exercising the
	// os.RemoveAll fallback.
	orphanID := uuid.NewString()
	dir := filepath.Join(repoPath, ".ate-worktrees", orphanID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s := worktreesweep.New(q, time.Hour)
	if err := s.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("crash-orphaned dir should be removed via fallback, stat err=%v", err)
	}
}

func TestRunOnce_NoDeletionOnListError(t *testing.T) {
	db := openTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	q := gen.New(db.SQL()) // queries against a closed DB will error

	s := worktreesweep.New(q, time.Hour)
	if err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("expected an error from a closed DB, got nil")
	}
}

// TestSweeper_Run_ReturnsOnContextCancel verifies Run's ctx.Done() branch:
// with the context already cancelled, Run must return promptly without
// blocking on the (1-minute-floored) timer.
func TestSweeper_Run_ReturnsOnContextCancel(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	s := worktreesweep.New(q, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return promptly after context cancellation")
	}
}
