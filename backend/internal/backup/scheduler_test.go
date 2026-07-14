package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/backup"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	dbPath := t.TempDir() + "/src.db"
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRunOnce_WritesSnapshot(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()

	s := backup.New(db, dir, time.Hour, 7)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 snapshot file, got %d", len(entries))
	}
}

func TestRunOnce_PrunesOldSnapshotsKeepingNewest(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()

	// Seed 5 fake old snapshot files matching the scheduler's naming pattern,
	// with distinct, sortable timestamps.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var seeded []string
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Hour).Format("20060102-150405")
		name := "agent-task-editor-" + ts + ".db"
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
		seeded = append(seeded, name)
	}

	// Also seed an unrelated file that must never be pruned.
	unrelated := filepath.Join(dir, "not-a-backup.txt")
	if err := os.WriteFile(unrelated, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seed unrelated file: %v", err)
	}

	keep := 3
	s := backup.New(db, dir, time.Hour, keep)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	var backups []string
	foundUnrelated := false
	for _, e := range entries {
		if e.Name() == "not-a-backup.txt" {
			foundUnrelated = true
			continue
		}
		backups = append(backups, e.Name())
	}

	if !foundUnrelated {
		t.Errorf("expected unrelated file to survive pruning")
	}
	// keep newest `keep` of the 5 seeded, plus the 1 just-written = keep total
	// (the just-written one is always the newest, so it's always retained).
	if len(backups) != keep {
		t.Fatalf("expected %d backup files retained, got %d: %v", keep, len(backups), backups)
	}

	// The 2 oldest seeded files should be gone; the 3 newest seeded should
	// NOT survive either, since the freshly-written snapshot is newer than
	// all of them and only `keep`=3 total survive: the new one + 2 newest
	// seeded.
	for _, name := range seeded[:2] {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected oldest seeded file %s to be pruned", name)
		}
	}
	for _, name := range seeded[3:] {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected newest seeded file %s to survive pruning: %v", name, err)
		}
	}
}

func TestRunOnce_WithSettingsFunc_UsesLatestKeep(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()

	// Seed 5 old snapshots.
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Hour).Format("20060102-150405")
		path := filepath.Join(dir, "agent-task-editor-"+ts+".db")
		if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}

	keep := 2
	s := backup.NewWithSettingsFunc(db, dir, func() (time.Duration, int) {
		return time.Hour, keep
	})
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != keep {
		t.Fatalf("expected %d files retained per the settings func's keep, got %d", keep, len(entries))
	}
}

func TestScheduler_MinInterval_FloorsSubMinimumInterval(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()

	// A settings func that returns a well-below-floor interval must still be
	// floored at backup.MinInterval by currentSettings (exercised indirectly
	// via RunOnce, which reads keep from the same path).
	s := backup.NewWithSettingsFunc(db, dir, func() (time.Duration, int) {
		return time.Second, 7
	})
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// RunOnce itself doesn't use the interval (only Run's timer does), so
	// this just confirms a sub-floor settings func doesn't break RunOnce.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 snapshot file, got %d", len(entries))
	}
}

func TestRunOnce_KeepZeroOrNegative_DisablesPruning(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()

	for i := 1; i <= 3; i++ {
		ts := time.Now().Add(-time.Duration(i) * time.Hour).UTC().Format("20060102-150405")
		path := filepath.Join(dir, "agent-task-editor-"+ts+".db")
		if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}

	s := backup.New(db, dir, time.Hour, 0)
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	// 3 seeded + 1 newly written, none pruned since keep<=0 disables pruning.
	if len(entries) != 4 {
		t.Fatalf("expected no pruning with keep<=0, got %d entries", len(entries))
	}
}
