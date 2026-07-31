// Package storage handles SQLite database setup, migrations, and seeding.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/mattn/go-sqlite3"
)

// maxPoolConns bounds the shared connection pool. SQLite WAL mode allows
// many concurrent readers alongside a single writer, so this no longer needs
// to be 1 (see the rationale on SetMaxOpenConns below) — but it is still
// capped at a small constant rather than left unbounded, since each
// connection is a real OS file handle/WAL reader snapshot.
const maxPoolConns = 8

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a *sql.DB and exposes query methods.
type DB struct {
	sql  *sql.DB
	path string
}

// Open opens (or creates) the SQLite database at path and runs all pending migrations.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	// A single shared *sql.DB is used for both reads and writes (see SQL()).
	// WAL mode (enabled above) lets any number of readers proceed
	// concurrently with a single in-progress writer, so capping the pool at
	// one connection — as a prior version of this code did — defeated WAL
	// entirely: every read serialized behind every write (and behind every
	// other read) through that one connection.
	//
	// _txlock=immediate makes every BeginTx-started transaction acquire
	// SQLite's write lock (via "BEGIN IMMEDIATE") up front rather than
	// lazily on its first write statement. Without this, two connections
	// could each start a transaction as a reader and then both try to
	// upgrade to a writer mid-transaction, which SQLite cannot resolve
	// without erroring one of them out (SQLITE_BUSY) — BEGIN IMMEDIATE
	// avoids that by failing/blocking (up to _busy_timeout) at the start of
	// the transaction instead. Combined with _busy_timeout=5000, a writer
	// that can't immediately acquire the lock waits rather than erroring,
	// which is what lets multiple connections safely share write paths
	// (BeginTx) alongside concurrent plain reads.
	sqlDB.SetMaxOpenConns(maxPoolConns)
	sqlDB.SetMaxIdleConns(maxPoolConns)

	if err := runMigrations(sqlDB); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &DB{sql: sqlDB, path: path}, nil
}

// SQL returns the underlying *sql.DB for use with sqlc-generated code.
func (db *DB) SQL() *sql.DB {
	return db.sql
}

// Path returns the filesystem path of the database file, as passed to Open
// (no DSN query parameters). Used by the backup handler/scheduler to locate
// a writable directory on the same filesystem as the live DB.
func (db *DB) Path() string {
	return db.path
}

// Size returns the on-disk size in bytes of the SQLite database file at
// db.Path() (the main file only, not -wal/-shm sidecars, since VACUUM
// INTO/backups operate on the logical DB size which os.Stat on the main
// file approximates well enough for a dashboard figure). Used by the Health
// page to surface DB growth. Note: in WAL mode the main file can undercount
// until a checkpoint occurs — acceptable for an observability figure, not
// meant to be exact.
func (db *DB) Size() (int64, error) {
	fi, err := os.Stat(db.path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// Close closes the underlying database connection.
func (db *DB) Close() error {
	return db.sql.Close()
}

// Backup writes a consistent point-in-time snapshot of the database to a new
// file at dstPath, using SQLite's VACUUM INTO. This is safe to run while the
// database is under concurrent write load (unlike a raw file copy of a
// WAL-mode database), because SQLite guarantees VACUUM INTO produces a
// complete, self-consistent copy.
//
// dstPath must not already exist — VACUUM INTO refuses to overwrite an
// existing file. Callers should target a fresh/unique filename and clean it
// up afterward.
func (db *DB) Backup(ctx context.Context, dstPath string) error {
	if _, err := os.Stat(dstPath); err == nil {
		return fmt.Errorf("backup destination already exists: %s", dstPath)
	}
	if _, err := db.sql.ExecContext(ctx, "VACUUM INTO ?", dstPath); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dstPath, err)
	}
	return nil
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	// NoTxWrap: some migrations rebuild a table that other tables reference via
	// ON DELETE SET NULL foreign keys (e.g. 039_provider_configs, following the
	// 008_agent_runs_fk_set_null pattern). Wrapped in a transaction, an in-file
	// "PRAGMA foreign_keys=OFF" is a documented SQLite no-op (PRAGMA foreign_keys
	// only takes effect outside an active transaction), so the DROP TABLE step
	// would otherwise fire ON DELETE SET NULL against every referencing row
	// before the rebuilt table is renamed back into place — silently nulling
	// live data. Running migrations without the wrapper lets those migrations'
	// own "PRAGMA foreign_keys=OFF/ON" statements actually take effect.
	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{NoTxWrap: true})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "sqlite3", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}
