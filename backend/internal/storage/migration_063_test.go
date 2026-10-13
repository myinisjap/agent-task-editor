package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// snapshotRows reads every row of table (or view) keyed by id, each value
// rendered as a string, so a before/after comparison is by column name and is
// independent of column order.
func snapshotRows(t *testing.T, db *sql.DB, query string) map[string]map[string]string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	out := map[string]map[string]string{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		row := map[string]string{}
		for i, c := range cols {
			row[c] = fmt.Sprintf("%v", vals[i])
		}
		out[row["id"]] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func newMigrator063(t *testing.T, db *DB) *migrate.Migrate {
	t.Helper()
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
	return m
}

// TestMigration063TaskSideTables seeds a database at version 62 with tasks
// carrying git and write-back data, migrates up, and checks that the data
// landed in the side tables, that the tasks_full view reproduces every
// pre-migration task row exactly (including the JSON a handler would emit),
// and that rolling back restores the original columns and values.
func TestMigration063TaskSideTables(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig063.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	sqlDB := db.SQL()
	m := newMigrator063(t, db)

	if err := m.Migrate(62); err != nil {
		t.Fatalf("down to 62: %v", err)
	}
	assertColumnPresent(t, sqlDB, "tasks", "branch")
	assertColumnPresent(t, sqlDB, "tasks", "writeback_closed")

	for _, stmt := range []string{
		`INSERT INTO workflows (id, name) VALUES ('wf1', 'Default')`,
		`INSERT INTO repos (id, name, path, workflow_id) VALUES ('repo1', 'repo', '/tmp/repo1', 'wf1')`,
		// full git + write-back data
		`INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments,
			branch, worktree_path, base_ref, git_state, pr_url, pr_mergeable,
			writeback_in_progress_sent, writeback_pr_commented, writeback_closed)
		 VALUES ('full', 'Full', 'd', 'feature', 'work', 'repo1', 'wf1', '[]',
			'ate/full', '/tmp/repo1/.ate-worktrees/full', 'main', 'pr_open', 'https://example.test/pr/1', 'mergeable', 1, 1, 0)`,
		// git data only, no write-back flags
		`INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments, branch, base_ref)
		 VALUES ('gitonly', 'Git only', '', 'bug', 'work', 'repo1', 'wf1', '[]', 'ate/gitonly', 'main')`,
		// write-back only
		`INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments, writeback_closed)
		 VALUES ('wbonly', 'Write-back only', '', 'bug', 'work', 'repo1', 'wf1', '[]', 1)`,
		// untouched defaults: must get no side rows
		`INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments)
		 VALUES ('plain', 'Plain', '', 'bug', 'work', 'repo1', 'wf1', '[]')`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	before := snapshotRows(t, sqlDB, `SELECT * FROM tasks`)
	if len(before) != 4 {
		t.Fatalf("seeded %d tasks, want 4", len(before))
	}

	if err := m.Migrate(63); err != nil {
		t.Fatalf("up to 63: %v", err)
	}
	for _, col := range []string{"branch", "worktree_path", "base_ref", "git_state", "pr_url", "pr_mergeable",
		"writeback_in_progress_sent", "writeback_pr_commented", "writeback_closed"} {
		assertColumnAbsent(t, sqlDB, "tasks", col)
	}

	// Side tables hold exactly the tasks that had non-default values.
	var gitRows, wbRows int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM task_git`).Scan(&gitRows); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT count(*) FROM task_writeback`).Scan(&wbRows); err != nil {
		t.Fatal(err)
	}
	if gitRows != 2 || wbRows != 2 {
		t.Fatalf("task_git rows = %d (want 2), task_writeback rows = %d (want 2)", gitRows, wbRows)
	}
	var branch, prURL string
	if err := sqlDB.QueryRow(`SELECT branch, pr_url FROM task_git WHERE task_id = 'full'`).Scan(&branch, &prURL); err != nil {
		t.Fatal(err)
	}
	if branch != "ate/full" || prURL != "https://example.test/pr/1" {
		t.Fatalf("task_git full = %q/%q", branch, prURL)
	}

	// The flat view reproduces every old row, column for column.
	after := snapshotRows(t, sqlDB, `SELECT * FROM tasks_full`)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("tasks_full differs from pre-migration tasks:\nbefore=%v\nafter=%v", before, after)
	}

	// API level: the JSON for every task is identical to what the old
	// 37-column row produced, and the plain task keeps the old defaults.
	q := gen.New(sqlDB)
	got, err := q.GetTask(context.Background(), "full")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	want := gen.Task{
		ID: "full", Title: "Full", Description: "d", Type: "feature", Label: "work", RepoID: "repo1", WorkflowID: "wf1",
		Attachments: "[]", Branch: "ate/full", WorktreePath: "/tmp/repo1/.ate-worktrees/full", BaseRef: "main",
		GitState: "pr_open", PrUrl: "https://example.test/pr/1", PrMergeable: "mergeable",
		WritebackInProgressSent: 1, WritebackPrCommented: 1, WritebackClosed: 0,
		CreatedAt: got.CreatedAt, UpdatedAt: got.UpdatedAt,
	}
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	if string(gj) != string(wj) {
		t.Fatalf("task JSON changed:\n got=%s\nwant=%s", gj, wj)
	}
	plain, err := q.GetTask(context.Background(), "plain")
	if err != nil {
		t.Fatalf("GetTask plain: %v", err)
	}
	if plain.Branch != "" || plain.GitState != "" || plain.PrUrl != "" || plain.PrMergeable != "" ||
		plain.WritebackClosed != 0 || plain.WritebackInProgressSent != 0 || plain.WritebackPrCommented != 0 {
		t.Fatalf("plain task has non-default git/write-back fields: %+v", plain)
	}

	// Cascade delete removes the side rows.
	if _, err := sqlDB.Exec(`DELETE FROM tasks WHERE id = 'full'`); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := sqlDB.QueryRow(`SELECT (SELECT count(*) FROM task_git WHERE task_id = 'full') + (SELECT count(*) FROM task_writeback WHERE task_id = 'full')`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d side rows survived task delete", left)
	}
	delete(before, "full")

	// Down: columns return with their data, side tables and view are gone.
	if err := m.Migrate(62); err != nil {
		t.Fatalf("down to 62 (063 rollback): %v", err)
	}
	assertColumnPresent(t, sqlDB, "tasks", "pr_mergeable")
	for _, name := range []string{"task_git", "task_writeback", "tasks_full"} {
		var n int
		if err := sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s still exists after rollback", name)
		}
	}
	if restored := snapshotRows(t, sqlDB, `SELECT * FROM tasks`); !reflect.DeepEqual(before, restored) {
		t.Fatalf("rollback did not restore task rows:\nwant=%v\n got=%v", before, restored)
	}

	// And forward again.
	if err := m.Migrate(63); err != nil {
		t.Fatalf("re-up to 63: %v", err)
	}
	if again := snapshotRows(t, sqlDB, `SELECT * FROM tasks_full`); !reflect.DeepEqual(before, again) {
		t.Fatalf("re-migrated view differs:\nwant=%v\n got=%v", before, again)
	}
}

func assertColumnPresent(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	if n != 1 {
		t.Fatalf("column %q missing from %q", column, table)
	}
}

// TestTaskSideTableWriters checks the lazy-upsert write path: a task starts
// with no side rows, each writer creates/updates them, reads see the values
// through GetTask, updated_at is bumped except by SetTaskPRMergeable, and an
// unknown task id is sql.ErrNoRows with no stray side rows.
func TestTaskSideTableWriters(t *testing.T) {
	db, err := Open(t.TempDir() + "/side.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	s := db.SQL()
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO workflows (id, name) VALUES ('wf1', 'Default')`,
		`INSERT INTO repos (id, name, path, workflow_id) VALUES ('repo1', 'repo', '/tmp/repo1', 'wf1')`,
		`INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments, updated_at)
		 VALUES ('t1', 'T', '', 'bug', 'work', 'repo1', 'wf1', '[]', '2000-01-01 00:00:00')`,
	} {
		if _, err := s.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	q := gen.New(s)
	count := func(table string) int {
		var n int
		if err := s.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("task_git") != 0 || count("task_writeback") != 0 {
		t.Fatal("side rows exist before any write")
	}
	stamp := func() string { return rawText(t, s, `SELECT updated_at FROM tasks WHERE id = 't1'`) }

	// Mergeable first: creates the row, leaves updated_at alone.
	got, err := q.SetTaskPRMergeable(ctx, gen.SetTaskPRMergeableParams{PrMergeable: "conflicting", ID: "t1"})
	if err != nil || got.PrMergeable != "conflicting" {
		t.Fatalf("SetTaskPRMergeable = %+v, %v", got, err)
	}
	if stamp() != "2000-01-01 00:00:00" {
		t.Fatalf("SetTaskPRMergeable touched updated_at: %s", stamp())
	}

	if err := q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: "b", WorktreePath: "/w", BaseRef: "main", ID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if stamp() == "2000-01-01 00:00:00" {
		t.Fatal("SetTaskWorktree did not bump updated_at")
	}
	got, _ = q.GetTask(ctx, "t1")
	if got.Branch != "b" || got.WorktreePath != "/w" || got.BaseRef != "main" || got.PrMergeable != "conflicting" {
		t.Fatalf("after worktree: %+v", got)
	}

	got, err = q.SetTaskPR(ctx, gen.SetTaskPRParams{GitState: "pr_open", PrUrl: "u", ID: "t1"})
	if err != nil || got.GitState != "pr_open" || got.PrUrl != "u" || got.Branch != "b" {
		t.Fatalf("SetTaskPR = %+v, %v", got, err)
	}
	got, err = q.UpdateTaskGitState(ctx, gen.UpdateTaskGitStateParams{GitState: "pr_merged", ID: "t1"})
	if err != nil || got.GitState != "pr_merged" || got.PrUrl != "u" {
		t.Fatalf("UpdateTaskGitState = %+v, %v", got, err)
	}
	if err := q.ClearTaskWorktreePath(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	got, _ = q.GetTask(ctx, "t1")
	if got.WorktreePath != "" || got.Branch != "b" {
		t.Fatalf("after clear: %+v", got)
	}

	for _, fn := range []func(context.Context, string) error{q.SetTaskWritebackInProgress, q.SetTaskWritebackPRCommented, q.SetTaskWritebackClosed} {
		if err := fn(ctx, "t1"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = q.GetTask(ctx, "t1")
	if got.WritebackInProgressSent != 1 || got.WritebackPrCommented != 1 || got.WritebackClosed != 1 {
		t.Fatalf("write-back flags: %+v", got)
	}
	if count("task_git") != 1 || count("task_writeback") != 1 {
		t.Fatalf("side rows: git=%d writeback=%d, want 1 each", count("task_git"), count("task_writeback"))
	}

	// Unknown task: ErrNoRows, nothing created.
	if _, err := q.SetTaskPR(ctx, gen.SetTaskPRParams{GitState: "x", PrUrl: "x", ID: "nope"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetTaskPR unknown id err = %v, want ErrNoRows", err)
	}
	if err := q.SetTaskWritebackClosed(ctx, "nope"); err != nil {
		t.Fatalf("write-back flag on unknown id: %v", err)
	}
	if count("task_git") != 1 || count("task_writeback") != 1 {
		t.Fatal("unknown task id created side rows")
	}
}
