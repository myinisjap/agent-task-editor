package storage

import (
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

var canonicalTS = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)

func rawText(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow("SELECT CAST(("+query+") AS TEXT)", args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s.String
}

func TestFormatTime(t *testing.T) {
	loc := time.FixedZone("x", 2*3600)
	got := FormatTime(time.Date(2026, 1, 2, 12, 30, 45, 999, loc))
	if got != "2026-01-02 10:30:45" {
		t.Fatalf("FormatTime = %q", got)
	}
}

// TestTimestampWritePathsAgree inserts rows via the CURRENT_TIMESTAMP default
// and via Go-supplied time.Time values (value and pointer, non-UTC zone) and
// asserts every stored value has the identical canonical text format.
func TestTimestampWritePathsAgree(t *testing.T) {
	db, err := Open(t.TempDir() + "/ts.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := db.SQL()

	if _, err := s.Exec(`INSERT INTO workflows (id, name) VALUES ('wf', 'W')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO repos (id, name, path, workflow_id) VALUES ('r', 'r', '/tmp/r', 'wf')`); err != nil {
		t.Fatal(err)
	}
	// created_at via default; next_retry_at via a Go time.Time with offset
	// and sub-second precision; source_state_at via *time.Time.
	if _, err := s.Exec(`INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments)
		VALUES ('t', 'T', '', 'feature', 'work', 'r', 'wf', '[]')`); err != nil {
		t.Fatal(err)
	}
	local := time.Date(2099, 7, 13, 20, 57, 44, 123456789, time.FixedZone("CDT", -5*3600))
	if _, err := s.Exec(`UPDATE tasks SET next_retry_at = ?, source_state_at = ? WHERE id = 't'`, local, &local); err != nil {
		t.Fatal(err)
	}
	var nilTime *time.Time
	if _, err := s.Exec(`UPDATE tasks SET updated_at = ? WHERE id = 't'`, local); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE tasks SET next_retry_at = ? WHERE id = 'nope'`, nilTime); err != nil {
		t.Fatal(err)
	}

	for _, col := range []string{"created_at", "updated_at", "next_retry_at", "source_state_at"} {
		v := rawText(t, s, "SELECT "+col+" FROM tasks WHERE id = 't'")
		if !canonicalTS.MatchString(v) {
			t.Errorf("tasks.%s = %q, not canonical", col, v)
		}
	}
	if v := rawText(t, s, "SELECT next_retry_at FROM tasks WHERE id = 't'"); v != "2099-07-14 01:57:44" {
		t.Errorf("next_retry_at = %q, want UTC conversion 2099-07-14 01:57:44", v)
	}

	// Reading back through the driver still yields the right instant.
	var got time.Time
	if err := s.QueryRow(`SELECT next_retry_at FROM tasks WHERE id = 't'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(local.Truncate(time.Second)) {
		t.Errorf("read back %v, want %v", got, local.Truncate(time.Second))
	}

	// Plain string comparison now agrees with chronological order.
	var due int
	if err := s.QueryRow(`SELECT COUNT(*) FROM tasks WHERE next_retry_at <= CURRENT_TIMESTAMP`).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if due != 0 {
		t.Errorf("future next_retry_at compared as due")
	}
}

// TestMigration058NormalizeTimestamps seeds mixed-format values into a pre-058
// database and checks they are rewritten to the canonical format.
func TestMigration058NormalizeTimestamps(t *testing.T) {
	db, err := Open(t.TempDir() + "/mig058.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := db.SQL()

	driver, err := sqlite3.WithInstance(s, &sqlite3.Config{NoTxWrap: true})
	if err != nil {
		t.Fatal(err)
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite3", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(56); err != nil {
		t.Fatalf("down to 56: %v", err)
	}

	if _, err := s.Exec(`INSERT INTO workflows (id, name) VALUES ('wf', 'W')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO repos (id, name, path, workflow_id) VALUES ('r', 'r', '/tmp/r', 'wf')`); err != nil {
		t.Fatal(err)
	}
	seed := func(id, created, retry string) {
		t.Helper()
		var retryArg any = retry
		if retry == "" {
			retryArg = nil
		}
		if _, err := s.Exec(`INSERT INTO tasks (id, title, description, type, label, repo_id, workflow_id, attachments, created_at, next_retry_at)
			VALUES (?, 'T', '', 'feature', 'work', 'r', 'wf', '[]', ?, ?)`, id, created, retryArg); err != nil {
			t.Fatal(err)
		}
	}
	seed("canon", "2026-01-02 03:04:05", "")
	seed("offset", "2026-07-13 20:57:44.123456789-05:00", "2026-07-13T20:57:44+02:00")
	seed("zulu", "2026-03-04T05:06:07Z", "2026-03-04T05:06:07.5Z")
	seed("junk", "not a time", "")
	seed("empty", "", "")

	if err := m.Up(); err != nil {
		t.Fatalf("up: %v", err)
	}

	want := map[string][2]string{
		"canon":  {"2026-01-02 03:04:05", ""},
		"offset": {"2026-07-14 01:57:44", "2026-07-13 18:57:44"},
		"zulu":   {"2026-03-04 05:06:07", "2026-03-04 05:06:07"},
		"junk":   {"not a time", ""},
		"empty":  {"", ""},
	}
	for id, w := range want {
		c := rawText(t, s, `SELECT created_at FROM tasks WHERE id = ?`, id)
		r := rawText(t, s, `SELECT next_retry_at FROM tasks WHERE id = ?`, id)
		if c != w[0] || r != w[1] {
			t.Errorf("%s: created_at=%q next_retry_at=%q, want %q / %q", id, c, r, w[0], w[1])
		}
	}
	// Default-populated columns on the workflow row also stay canonical.
	if v := rawText(t, s, `SELECT created_at FROM workflows WHERE id = 'wf'`); !canonicalTS.MatchString(v) {
		t.Errorf("workflows.created_at = %q", v)
	}
}
