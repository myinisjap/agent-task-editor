// Package storage handles SQLite database setup, migrations, and seeding.
package storage

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a *sql.DB and exposes query methods.
type DB struct {
	sql *sql.DB
}

// Open opens (or creates) the SQLite database at path and runs all pending migrations.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := runMigrations(sqlDB); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &DB{sql: sqlDB}, nil
}

// SQL returns the underlying *sql.DB for use with sqlc-generated code.
func (db *DB) SQL() *sql.DB {
	return db.sql
}

// Close closes the underlying database connection.
func (db *DB) Close() error {
	return db.sql.Close()
}

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
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
