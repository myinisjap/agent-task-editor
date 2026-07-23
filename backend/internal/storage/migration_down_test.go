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
//
// A later migration (020, agent_retry_policy) was added on top of 018 and
// doesn't touch agent_runs, so this test rolls all the way back past 018
// (to version 17) to ensure 018's down migration itself actually runs.
func TestMigration018DownStep(t *testing.T) {
	const targetVersion = 17
	dbPath := t.TempDir() + "/migtest.db"
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

	// Migrations after 018 (019 command_filters, 020 agent_retry_policy)
	// don't touch agent_runs, but Steps(-1) only rolls back the single most
	// recent migration, and Migrate(N) moves the schema to the state *after*
	// migration N has been applied (i.e. Migrate(18) only undoes later
	// migrations, since 18 is already applied at that point). To actually
	// exercise 018's down migration, roll back past it entirely to version 17.
	if err := m.Migrate(targetVersion); err != nil {
		t.Fatalf("down to version %d (018 rollback): %v", targetVersion, err)
	}

	// Verify the columns are actually gone.
	rows, err := db.SQL().Query(`PRAGMA table_info(agent_runs)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer func() { _ = rows.Close() }()
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

// TestMigration042DownStep verifies migration 042's down migration (which
// drops the model_pricing table and agent_runs.cost_unknown column) applies
// cleanly against this repo's SQLite driver/version. 042 is the latest
// migration at the time this test was written, so Migrate(41) rolls back
// just that one step and exercises its down migration directly.
func TestMigration042DownStep(t *testing.T) {
	const targetVersion = 41
	dbPath := t.TempDir() + "/migtest.db"
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
		t.Fatalf("down to version %d (042 rollback): %v", targetVersion, err)
	}

	// model_pricing table should be gone.
	var tableCount int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='model_pricing'`).Scan(&tableCount); err != nil {
		t.Fatalf("check model_pricing table: %v", err)
	}
	if tableCount != 0 {
		t.Error("expected model_pricing table to be dropped after down migration")
	}

	// agent_runs.cost_unknown column should be gone.
	rows, err := db.SQL().Query(`PRAGMA table_info(agent_runs)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer func() { _ = rows.Close() }()
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
		if name == "cost_unknown" {
			t.Fatal("column \"cost_unknown\" still present after down migration")
		}
	}
}
