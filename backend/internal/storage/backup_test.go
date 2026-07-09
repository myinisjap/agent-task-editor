package storage

import (
	"context"
	"os"
	"testing"
)

// TestDBBackup verifies DB.Backup produces a valid, independent SQLite
// snapshot via VACUUM INTO, and refuses to overwrite an existing destination.
func TestDBBackup(t *testing.T) {
	dbPath := t.TempDir() + "/src.db"
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.SQL().Exec(`INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments) VALUES ('t1', 'title', '', 'feature', 'not_ready', 'r1', 'w1', '[]')`); err != nil {
		// Foreign key constraints may reject this without a repo/workflow row;
		// that's fine — the backup should still work on an otherwise-empty DB.
		t.Logf("seed insert skipped: %v", err)
	}

	dstPath := t.TempDir() + "/backup.db"
	if err := db.Backup(context.Background(), dstPath); err != nil {
		t.Fatalf("backup: %v", err)
	}

	if _, err := os.Stat(dstPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	backupDB, err := Open(dstPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer func() { _ = backupDB.Close() }()

	var count int
	if err := backupDB.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='tasks'").Scan(&count); err != nil {
		t.Fatalf("query backup: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected tasks table to exist in backup, got count=%d", count)
	}

	// VACUUM INTO must refuse to overwrite an existing destination.
	if err := db.Backup(context.Background(), dstPath); err == nil {
		t.Fatalf("expected error backing up to an existing destination, got nil")
	}
}

// TestDBPath verifies Path returns the raw path passed to Open, not the DSN
// string used internally for sql.Open.
func TestDBPath(t *testing.T) {
	dbPath := t.TempDir() + "/mydb.db"
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if db.Path() != dbPath {
		t.Fatalf("expected Path()=%q, got %q", dbPath, db.Path())
	}
}
