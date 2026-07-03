package storage

import (
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// TestMigration018DownStep verifies that migration 018's down migration
// (which drops the input_tokens/output_tokens/cost_usd columns added for
// per-run cost/usage tracking) applies cleanly against this repo's SQLite
// driver/version, since older SQLite versions require a table-rebuild
// pattern for DROP COLUMN rather than a direct ALTER TABLE.
func TestMigration018DownStep(t *testing.T) {
	dbPath := t.TempDir() + "/migtest.db"
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

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

	if err := m.Steps(-1); err != nil {
		t.Fatalf("down one step (018 rollback): %v", err)
	}

	// Verify the columns are actually gone.
	rows, err := db.SQL().Query(`PRAGMA table_info(agent_runs)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "input_tokens" || name == "output_tokens" || name == "cost_usd" {
			t.Fatalf("column %q still present after down migration", name)
		}
	}
}
