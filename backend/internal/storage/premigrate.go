package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source"
)

// SkipPreMigrateSnapshotEnv names the env var that, when "true", disables the
// automatic pre-migration snapshot (and lets startup proceed if the snapshot
// cannot be written).
const SkipPreMigrateSnapshotEnv = "SKIP_PREMIGRATE_SNAPSHOT"

// preMigrateKeep is how many pre-migration snapshots are retained.
const preMigrateKeep = 3

const (
	preMigrateInfix  = ".pre-migrate-"
	preMigrateSuffix = ".sqlite"
	preMigrateTSFmt  = "20060102T150405Z"
)

// isFileDBPath reports whether path names a plain on-disk SQLite file (as
// opposed to :memory:, an empty path, or a "file:" URI DSN).
func isFileDBPath(path string) bool {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") ||
		strings.Contains(path, "mode=memory") {
		return false
	}
	return true
}

// latestMigrationVersion returns the highest migration version in src.
func latestMigrationVersion(src source.Driver) (uint, error) {
	v, err := src.First()
	if err != nil {
		return 0, err
	}
	for {
		next, err := src.Next(v)
		if errors.Is(err, os.ErrNotExist) {
			return v, nil
		}
		if err != nil {
			return 0, err
		}
		v = next
	}
}

// preMigrateSnapshot writes a VACUUM INTO snapshot of the DB next to the DB
// file before pending migrations run. It returns the snapshot path, or ""
// when no snapshot was needed or taken. Snapshots are only taken for on-disk
// DBs that already have a schema version older than the latest available.
func preMigrateSnapshot(ctx context.Context, db *sql.DB, src source.Driver, m *migrate.Migrate, dbPath string) (string, error) {
	if !isFileDBPath(dbPath) {
		return "", nil
	}
	cur, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return "", nil // fresh DB: nothing to protect
	}
	if err != nil {
		return "", fmt.Errorf("read migration version: %w", err)
	}
	if dirty {
		return "", nil // m.Up will report ErrDirty; a snapshot of a broken state is not useful
	}
	latest, err := latestMigrationVersion(src)
	if err != nil {
		return "", fmt.Errorf("find latest migration: %w", err)
	}
	if cur >= latest {
		return "", nil
	}
	if os.Getenv(SkipPreMigrateSnapshotEnv) == "true" {
		slog.Warn("skipping pre-migration snapshot", "env", SkipPreMigrateSnapshotEnv, "from", cur, "to", latest)
		return "", nil
	}

	dst := fmt.Sprintf("%s%sv%d-to-v%d-%s%s", dbPath, preMigrateInfix, cur, latest,
		preMigrateNow().UTC().Format(preMigrateTSFmt), preMigrateSuffix)
	if err := vacuumInto(ctx, db, dst); err != nil {
		_ = os.Remove(dst)
		slog.Error("pre-migration snapshot failed; refusing to migrate without a safety net",
			"snapshot", dst, "error", err, "skip_env", SkipPreMigrateSnapshotEnv)
		return "", fmt.Errorf("pre-migration snapshot to %s failed: %w (fix the cause, or set %s=true to migrate without a snapshot)",
			dst, err, SkipPreMigrateSnapshotEnv)
	}
	slog.Info("pre-migration snapshot written", "snapshot", dst, "from", cur, "to", latest)
	prunePreMigrateSnapshots(dbPath, preMigrateKeep)
	return dst, nil
}

// prunePreMigrateSnapshots keeps the newest `keep` snapshots belonging to
// dbPath (ordered by the timestamp embedded in the filename) and deletes the
// rest. Failures are logged, never fatal.
func prunePreMigrateSnapshots(dbPath string, keep int) {
	dir, base := filepath.Split(dbPath)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Warn("prune pre-migration snapshots: read dir", "dir", dir, "error", err)
		return
	}
	prefix := base + preMigrateInfix
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, prefix) && strings.HasSuffix(n, preMigrateSuffix) {
			names = append(names, n)
		}
	}
	ts := func(n string) string {
		n = strings.TrimSuffix(n, preMigrateSuffix)
		return n[strings.LastIndex(n, "-")+1:]
	}
	sort.Slice(names, func(i, j int) bool {
		if ti, tj := ts(names[i]), ts(names[j]); ti != tj {
			return ti > tj // newest first
		}
		return names[i] > names[j]
	})
	for _, n := range names[min(keep, len(names)):] {
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			slog.Warn("prune pre-migration snapshot", "file", n, "error", err)
		}
	}
}

// preMigrateNow is the clock used for snapshot names (overridable in tests).
var preMigrateNow = time.Now
