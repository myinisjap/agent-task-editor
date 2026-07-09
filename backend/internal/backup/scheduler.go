// Package backup implements the optional automatic local-snapshot backup
// scheduler: on a configurable interval, it writes a rotated VACUUM INTO
// snapshot of the database to a configurable directory, pruning older
// snapshots beyond a configurable retention count.
//
// This is a local-disk rotation mechanism only — it does not upload
// anywhere. For offsite/durable retention, pair it with the Litestream
// sidecar documented in docs/backup.md, or a cron job that syncs the backup
// directory to remote storage.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
)

// filePrefix/fileSuffix delimit the scheduler's own snapshot files within
// dir, so pruning never touches files it didn't create — important if a
// user points BackupDir at a non-empty directory.
const (
	filePrefix = "agent-task-editor-"
	fileSuffix = ".db"
	// timeLayout produces lexicographically-sortable, filesystem-safe
	// timestamps (sortable by name == sortable by time).
	timeLayout = "20060102-150405"
)

// Scheduler periodically writes a rotated snapshot of db to dir, keeping
// only the newest `keep` snapshots.
type Scheduler struct {
	db       *storage.DB
	dir      string
	interval time.Duration
	keep     int
}

// New creates a Scheduler that writes snapshots of db to dir every interval,
// retaining the newest keep snapshots.
func New(db *storage.DB, dir string, interval time.Duration, keep int) *Scheduler {
	return &Scheduler{db: db, dir: dir, interval: interval, keep: keep}
}

// Run ticks on the configured interval until ctx is cancelled, calling
// RunOnce each tick. Errors are logged, never fatal — a bad backup
// destination (e.g. disk full) must not take down the process.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.RunOnce(ctx); err != nil {
				slog.Error("backup: scheduled snapshot failed", "err", err)
			}
		}
	}
}

// RunOnce writes one timestamped snapshot to dir and prunes old snapshots
// beyond the retention count. Exported so it's directly testable/reusable
// (e.g. from an admin command) without waiting on the ticker.
func (s *Scheduler) RunOnce(ctx context.Context) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	// VACUUM INTO refuses to overwrite an existing file. A same-second
	// collision is only realistically possible if RunOnce is invoked twice
	// in rapid succession (e.g. manual triggers/tests) — disambiguate by
	// appending an incrementing suffix rather than failing outright.
	base := time.Now().UTC().Format(timeLayout)
	name := filePrefix + base + fileSuffix
	dst := filepath.Join(s.dir, name)
	for i := 2; fileExists(dst); i++ {
		name = fmt.Sprintf("%s%s-%d%s", filePrefix, base, i, fileSuffix)
		dst = filepath.Join(s.dir, name)
	}

	if err := s.db.Backup(ctx, dst); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	slog.Info("backup written", "path", dst)

	if err := s.prune(); err != nil {
		slog.Error("backup: prune old snapshots failed", "err", err)
	}
	return nil
}

// prune keeps only the newest `keep` snapshots matching this scheduler's own
// naming pattern in dir, deleting the rest. Files that don't match the
// pattern (e.g. a user's own files in a shared directory) are never touched.
func (s *Scheduler) prune() error {
	if s.keep <= 0 {
		return nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read backup dir: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, filePrefix) && strings.HasSuffix(n, fileSuffix) {
			names = append(names, n)
		}
	}
	if len(names) <= s.keep {
		return nil
	}

	// Names embed a sortable timestamp, so lexicographic sort == chronological.
	sort.Strings(names)
	toDelete := names[:len(names)-s.keep]
	for _, n := range toDelete {
		path := filepath.Join(s.dir, n)
		if err := os.Remove(path); err != nil {
			slog.Warn("backup: failed to prune old snapshot", "path", path, "err", err)
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
