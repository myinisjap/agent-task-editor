package worktreesweep

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestNew_WiresIntervalAndQueries verifies the trivial constructor stores
// what it's given.
func TestNew_WiresIntervalAndQueries(t *testing.T) {
	s := New(nil, 5*time.Minute)
	if s == nil {
		t.Fatal("expected New to return a non-nil Sweeper")
	}
	if s.interval != 5*time.Minute {
		t.Errorf("expected interval 5m, got %v", s.interval)
	}
}

// TestCurrentInterval covers currentInterval's floor-at-MinInterval branch
// (a configured interval below the 1-minute floor is clamped up) and the
// pass-through branch (anything at/above the floor is used as-is).
func TestCurrentInterval(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{"below floor is clamped up", 10 * time.Second, MinInterval},
		{"zero is clamped up", 0, MinInterval},
		{"exactly at floor is unchanged", MinInterval, MinInterval},
		{"above floor is unchanged", 10 * time.Minute, 10 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Sweeper{interval: tc.interval}
			if got := s.currentInterval(); got != tc.want {
				t.Errorf("currentInterval() with configured %v = %v, want %v", tc.interval, got, tc.want)
			}
		})
	}
}

func openInternalDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "sweep.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// TestReconcileRepo_KeepsWorktreeOfTaskCreatedAfterSnapshot simulates a task
// row appearing after the keep-set snapshot but before removal: reconcileRepo
// is handed a stale (empty) keep-set, and the DB re-check must save the dir.
// An archived task's dir with the same stale keep-set is still reclaimed.
func TestReconcileRepo_KeepsWorktreeOfTaskCreatedAfterSnapshot(t *testing.T) {
	db := openInternalDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()
	wfs, _ := q.ListWorkflows(ctx)
	wfID := wfs[0].ID
	repoPath := t.TempDir()
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: repoPath, WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}

	mk := func(archived bool) string {
		id := uuid.NewString()
		if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: id, Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "plan"}); err != nil {
			t.Fatal(err)
		}
		if archived {
			if _, err := q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: id}); err != nil {
				t.Fatal(err)
			}
		}
		dir := filepath.Join(repoPath, worktreesSubdir, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	freshDir := mk(false)
	archivedDir := mk(true)

	s := New(q, time.Hour)
	s.minAge = 0                                          // isolate the DB re-check from the mtime guard
	s.reconcileRepo(ctx, repoPath, map[string]struct{}{}) // stale snapshot: nothing kept

	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("worktree of a task created after the snapshot must survive: %v", err)
	}
	if _, err := os.Stat(archivedDir); !os.IsNotExist(err) {
		t.Errorf("archived task's dir should still be reclaimed, stat err=%v", err)
	}
}

// TestReconcileRepo_SkipsRecentlyModifiedDir covers the mtime guard.
func TestReconcileRepo_SkipsRecentlyModifiedDir(t *testing.T) {
	db := openInternalDB(t)
	q := gen.New(db.SQL())
	repoPath := t.TempDir()
	dir := filepath.Join(repoPath, worktreesSubdir, uuid.NewString())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := New(q, time.Hour)
	s.reconcileRepo(context.Background(), repoPath, map[string]struct{}{})
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("fresh orphan dir should be left for a later sweep: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(dir, old, old)
	s.reconcileRepo(context.Background(), repoPath, map[string]struct{}{})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("old orphan dir should be reclaimed, stat err=%v", err)
	}
}
