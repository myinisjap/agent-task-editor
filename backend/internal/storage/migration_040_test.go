package storage

import (
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// TestMigration040PRReviewFeedback verifies migration 040 adds the
// pr_review_auto_transition_enabled column to repos, the external_id/source
// columns to task_review_comments, and creates task_pr_review_state — and
// that rolling back removes them all again.
func TestMigration040PRReviewFeedback(t *testing.T) {
	dbPath := t.TempDir() + "/migtest040.db"
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	sqlDB := db.SQL()

	// Open() runs all migrations up by default, so the schema should already
	// include migration 040's additions.
	if _, err := sqlDB.Exec(`
		INSERT INTO workflows (id, name) VALUES ('wf1', 'Default')
	`); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if _, err := sqlDB.Exec(`
		INSERT INTO repos (id, name, path, workflow_id, pr_review_auto_transition_enabled)
		VALUES ('repo1', 'repo', '/tmp/repo1', 'wf1', 1)
	`); err != nil {
		t.Fatalf("seed repo with pr_review_auto_transition_enabled: %v", err)
	}
	var flag int64
	if err := sqlDB.QueryRow(`SELECT pr_review_auto_transition_enabled FROM repos WHERE id = 'repo1'`).Scan(&flag); err != nil {
		t.Fatalf("query pr_review_auto_transition_enabled: %v", err)
	}
	if flag != 1 {
		t.Fatalf("pr_review_auto_transition_enabled = %d, want 1", flag)
	}

	if _, err := sqlDB.Exec(`
		INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments)
		VALUES ('task1', 'Task', '', 'feature', 'work', 'repo1', 'wf1', '[]')
	`); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	if _, err := sqlDB.Exec(`
		INSERT INTO task_review_comments (id, task_id, file_path, start_line, end_line, body, external_id, source)
		VALUES ('rc1', 'task1', 'main.go', 1, 1, 'fix this', 'gh-comment-1', 'github')
	`); err != nil {
		t.Fatalf("seed github review comment: %v", err)
	}
	var extID, source string
	if err := sqlDB.QueryRow(`SELECT external_id, source FROM task_review_comments WHERE id = 'rc1'`).Scan(&extID, &source); err != nil {
		t.Fatalf("query external_id/source: %v", err)
	}
	if extID != "gh-comment-1" || source != "github" {
		t.Fatalf("external_id/source = %q/%q, want gh-comment-1/github", extID, source)
	}

	// Duplicate external_id for the same task should be rejected by the
	// partial unique index.
	if _, err := sqlDB.Exec(`
		INSERT INTO task_review_comments (id, task_id, file_path, start_line, end_line, body, external_id, source)
		VALUES ('rc2', 'task1', 'main.go', 2, 2, 'dup', 'gh-comment-1', 'github')
	`); err == nil {
		t.Fatalf("expected duplicate external_id for the same task to be rejected")
	}

	if _, err := sqlDB.Exec(`
		INSERT INTO task_pr_review_state (task_id, head_sha) VALUES ('task1', 'sha1')
	`); err != nil {
		t.Fatalf("seed task_pr_review_state: %v", err)
	}
	var headSHA string
	if err := sqlDB.QueryRow(`SELECT head_sha FROM task_pr_review_state WHERE task_id = 'task1'`).Scan(&headSHA); err != nil {
		t.Fatalf("query task_pr_review_state: %v", err)
	}
	if headSHA != "sha1" {
		t.Fatalf("head_sha = %q, want sha1", headSHA)
	}

	// Deleting the task should cascade-delete its review state row.
	if _, err := sqlDB.Exec(`DELETE FROM tasks WHERE id = 'task1'`); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	var remaining int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM task_pr_review_state WHERE task_id = 'task1'`).Scan(&remaining); err != nil {
		t.Fatalf("count task_pr_review_state after task delete: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected task_pr_review_state row to cascade-delete with its task, %d remain", remaining)
	}
}

// TestMigration040DownStep verifies migration 040's down migration (which
// drops task_pr_review_state and the columns it added to repos and
// task_review_comments) applies cleanly against this repo's SQLite
// driver/version — DROP COLUMN requires a table-rebuild pattern on older
// SQLite, mirroring TestMigration018DownStep's approach.
func TestMigration040DownStep(t *testing.T) {
	const targetVersion = 39
	dbPath := t.TempDir() + "/migtest040down.db"
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	driver, err := sqlite3.WithInstance(db.SQL(), &sqlite3.Config{})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite3", driver)
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}

	if err := m.Migrate(targetVersion); err != nil {
		t.Fatalf("down to version %d (040 rollback): %v", targetVersion, err)
	}

	assertColumnAbsent(t, db.SQL(), "repos", "pr_review_auto_transition_enabled")
	assertColumnAbsent(t, db.SQL(), "task_review_comments", "external_id")
	assertColumnAbsent(t, db.SQL(), "task_review_comments", "source")

	var tableCount int
	if err := db.SQL().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'task_pr_review_state'`).Scan(&tableCount); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("expected task_pr_review_state table to be dropped after down migration, still present")
	}
}
