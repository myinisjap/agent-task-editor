package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// makeOldVersionDB creates a file DB migrated to latest, then rolled back one
// step, and returns its path and the (old, latest) versions.
func makeOldVersionDB(t *testing.T) (path string, old, latest uint) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "app.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	driver, err := sqlite3.WithInstance(db.SQL(), &sqlite3.Config{NoTxWrap: true})
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
	latest, _, err = m.Version()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Steps(-1); err != nil {
		t.Fatalf("step down: %v", err)
	}
	old, _, err = m.Version()
	if err != nil {
		t.Fatal(err)
	}
	return path, old, latest
}

func snapshotsOf(t *testing.T, dbPath string) []string {
	t.Helper()
	m, err := filepath.Glob(dbPath + preMigrateInfix + "*")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPreMigrateSnapshotCreated(t *testing.T) {
	t.Setenv(SkipPreMigrateSnapshotEnv, "")
	path, old, latest := makeOldVersionDB(t)
	if len(snapshotsOf(t, path)) != 0 {
		t.Fatal("unexpected snapshot before pending migrations")
	}
	db, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = db.Close()

	snaps := snapshotsOf(t, path)
	if len(snaps) != 1 {
		t.Fatalf("want 1 snapshot, got %v", snaps)
	}
	if want := fmt.Sprintf("%sv%d-to-v%d-", preMigrateInfix, old, latest); !strings.Contains(snaps[0], want) || !strings.HasSuffix(snaps[0], preMigrateSuffix) {
		t.Fatalf("bad snapshot name %s, want to contain %s", snaps[0], want)
	}
	// Snapshot is a valid DB at the old version.
	sdb, err := sql.Open("sqlite3", snaps[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sdb.Close() }()
	var v uint
	if err := sdb.QueryRow(`SELECT version FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatalf("snapshot not a valid migrated DB: %v", err)
	}
	if v != old || old >= latest {
		t.Fatalf("snapshot version = %d, want %d (latest %d)", v, old, latest)
	}
}

func TestPreMigrateNoSnapshotWhenUpToDateOrFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	for i := 0; i < 2; i++ { // fresh, then up-to-date reopen
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
	}
	if s := snapshotsOf(t, path); len(s) != 0 {
		t.Fatalf("unexpected snapshots: %v", s)
	}
}

func TestPreMigrateMemoryAndURIDSNsSkipped(t *testing.T) {
	for _, p := range []string{":memory:", "", "file:x.db?mode=memory&cache=shared", "file:foo.db"} {
		if isFileDBPath(p) {
			t.Errorf("isFileDBPath(%q) = true", p)
		}
	}
	dir := t.TempDir()
	t.Chdir(dir)
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("files written for :memory: DB: %v", ents)
	}
}

func TestPreMigrateRetention(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "app.db")
	other := filepath.Join(dir, "other.db.pre-migrate-v1-to-v2-20200101T000000Z.sqlite")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var want []string
	for i := 1; i <= 5; i++ {
		n := db + preMigrateInfix + "v1-to-v2-" + time.Date(2024, 1, i, 0, 0, 0, 0, time.UTC).Format(preMigrateTSFmt) + preMigrateSuffix
		if err := os.WriteFile(n, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if i > 2 {
			want = append(want, n)
		}
	}
	prunePreMigrateSnapshots(db, 3)
	got := snapshotsOf(t, db)
	if len(got) != 3 {
		t.Fatalf("want 3 kept, got %v", got)
	}
	for _, w := range want {
		if _, err := os.Stat(w); err != nil {
			t.Errorf("newest snapshot %s was pruned", w)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("another DB's snapshot was touched")
	}
}

func TestPreMigrateSnapshotFailureAborts(t *testing.T) {
	t.Setenv(SkipPreMigrateSnapshotEnv, "")
	path, old, latest := makeOldVersionDB(t)
	fixed := time.Date(2025, 5, 5, 5, 5, 5, 0, time.UTC)
	preMigrateNow = func() time.Time { return fixed }
	t.Cleanup(func() { preMigrateNow = time.Now })

	// Occupy the destination with a directory so VACUUM INTO (and the
	// cleanup remove of a plain file) cannot succeed.
	dst := fmt.Sprintf("%s%sv%d-to-v%d-%s%s", path, preMigrateInfix, old, latest, fixed.Format(preMigrateTSFmt), preMigrateSuffix)
	if err := os.MkdirAll(filepath.Join(dst, "x"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := Open(path)
	if err == nil {
		t.Fatal("expected Open to fail when the snapshot cannot be written")
	}
	if !strings.Contains(err.Error(), SkipPreMigrateSnapshotEnv) {
		t.Fatalf("error should mention %s: %v", SkipPreMigrateSnapshotEnv, err)
	}
	// Migrations must not have run.
	raw, _ := sql.Open("sqlite3", path)
	defer func() { _ = raw.Close() }()
	var v uint
	if err := raw.QueryRow(`SELECT version FROM schema_migrations`).Scan(&v); err != nil || v != old {
		t.Fatalf("schema version = %d (err %v), want unchanged %d", v, err, old)
	}

	// With the skip var set, startup proceeds without a snapshot.
	t.Setenv(SkipPreMigrateSnapshotEnv, "true")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open with skip var: %v", err)
	}
	_ = db.Close()
}
