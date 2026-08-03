package storage

import (
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// TestMigrationsUpDownUpRoundTrip runs every migration's .up.sql forward to
// the latest version, then every .down.sql all the way back to 0, then
// forward again — against a scratch SQLite file using the exact same
// NoTxWrap driver configuration the app uses at startup (see runMigrations'
// comment on why the transaction wrapper can't be used: some migrations
// rebuild FK-referenced tables and rely on their own in-file
// "PRAGMA foreign_keys=OFF/ON" actually taking effect, which only happens
// outside a wrapping transaction).
//
// Beyond the spot checks in migration_039_test.go / migration_down_test.go
// (which each roll back one specific migration and assert on its resulting
// schema), this is a blanket smoke test: all ~49 down migrations exist but,
// before this test, none of them had ever actually been *executed* in CI —
// a broken down-migration (a typo, a table/column that no longer exists by
// the time a later migration touches it, an ordering bug, etc.) would only
// have been discovered during a real rollback in production. A Go test is
// used rather than a `migrate` CLI invocation in a CI step because the CLI
// would wrap each migration in a transaction, which — per the comment on
// NoTxWrap above — behaves differently from how the app actually runs
// migrations and would mask exactly the class of bug (in-transaction
// PRAGMA foreign_keys being a no-op) this test exists to catch.
func TestMigrationsUpDownUpRoundTrip(t *testing.T) {
	dbPath := t.TempDir() + "/migtest_roundtrip.db"
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open (initial up to latest): %v", err)
	}
	defer func() { _ = db.Close() }()

	sqlDB := db.SQL()
	driver, err := sqlite3.WithInstance(sqlDB, &sqlite3.Config{NoTxWrap: true})
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

	// Open() above already ran every up migration once. Sanity-check we're
	// not sitting on ErrNoChange only because nothing was ever applied.
	version, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("version after initial up: %v", err)
	}
	if dirty {
		t.Fatalf("migrator reports dirty state after initial up, version=%d", version)
	}
	if version == 0 {
		t.Fatalf("expected a non-zero migration version after Open(), got 0")
	}
	topVersion := version

	// Down: roll back every migration, one at a time via Steps(-1), so a
	// failure identifies exactly which down migration is broken rather than
	// just "some down migration in the whole chain failed".
	for {
		v, _, verr := m.Version()
		if verr == migrate.ErrNilVersion {
			break // fully rolled back
		}
		if verr != nil {
			t.Fatalf("version during rollback: %v", verr)
		}
		if err := m.Steps(-1); err != nil {
			t.Fatalf("down migration failed rolling back from version %d: %v", v, err)
		}
	}

	// Confirm we're actually at the bottom (no tables left from the schema,
	// modulo migrate's own schema_migrations bookkeeping table).
	var tableCount int
	if err := sqlDB.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'
	`).Scan(&tableCount); err != nil {
		t.Fatalf("count remaining tables after full rollback: %v", err)
	}
	if tableCount != 0 {
		rows, qerr := sqlDB.Query(`
			SELECT name FROM sqlite_master
			WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'
		`)
		if qerr == nil {
			var leftover []string
			for rows.Next() {
				var name string
				if scanErr := rows.Scan(&name); scanErr == nil {
					leftover = append(leftover, name)
				}
			}
			_ = rows.Close()
			t.Fatalf("expected no application tables after rolling back every migration, found: %v", leftover)
		}
		t.Fatalf("expected no application tables after rolling back every migration, found %d", tableCount)
	}

	// Up: replay every up migration from scratch and verify it lands back at
	// the same version with no errors.
	if err := m.Up(); err != nil {
		t.Fatalf("re-migrating up from scratch failed: %v", err)
	}
	version, dirty, err = m.Version()
	if err != nil {
		t.Fatalf("version after re-up: %v", err)
	}
	if dirty {
		t.Fatalf("migrator reports dirty state after re-up, version=%d", version)
	}
	if version != topVersion {
		t.Fatalf("expected re-up to reach version %d, got %d", topVersion, version)
	}

	// Smoke-check the schema is actually usable post-round-trip: seed a
	// workflow (touches workflows/workflow_labels/workflow_transitions) and
	// then insert a repo referencing it. Any error here hard-fails the test
	// — the FK target is guaranteed to exist, so a failure means the
	// round-trip left the schema unusable end-to-end (missing table/column,
	// a NOT NULL reintroduced without a default, a broken FK, etc.).
	if _, err := sqlDB.Exec(`INSERT INTO workflows (id, name) VALUES ('roundtrip-wf', 'roundtrip')`); err != nil {
		t.Fatalf("seed workflow after round-trip: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO repos (id, name, path, workflow_id)
		VALUES ('roundtrip-repo', 'roundtrip', '/tmp/roundtrip-repo', 'roundtrip-wf')`); err != nil {
		t.Fatalf("repo insert after round-trip failed (schema regression?): %v", err)
	}
}
