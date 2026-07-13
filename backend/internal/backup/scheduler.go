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

// MinInterval is the minimum interval a caller is allowed to configure
// between scheduled snapshots (10 minutes). Enforced both here (as a floor
// on whatever interval/settings getter is supplied) and by the
// PUT /api/v1/backup/settings handler's request validation, so a
// misconfigured or malicious value can never make the scheduler thrash.
const MinInterval = 10 * time.Minute

// Scheduler periodically writes a rotated snapshot of db to dir, keeping
// only the newest `keep` snapshots.
//
// interval/keep are re-read from settingsFn before every run (see Run), so
// changes made via PUT /api/v1/backup/settings take effect on the next tick
// without a process restart — this is why Run uses a time.Timer that's
// re-armed with the latest interval each iteration, rather than a
// time.Ticker fixed at construction time.
type Scheduler struct {
	db         *storage.DB
	dir        string
	interval   time.Duration
	keep       int
	settingsFn func() (time.Duration, int)
}

// New creates a Scheduler that writes snapshots of db to dir every interval,
// retaining the newest keep snapshots. interval/keep are fixed for the
// lifetime of the Scheduler; use NewWithSettingsFunc for a scheduler whose
// interval/keep can change at runtime.
func New(db *storage.DB, dir string, interval time.Duration, keep int) *Scheduler {
	return &Scheduler{db: db, dir: dir, interval: interval, keep: keep}
}

// NewWithSettingsFunc creates a Scheduler that re-reads its interval/keep
// from settingsFn before every scheduled run, so runtime changes (e.g. via
// PUT /api/v1/backup/settings) take effect without a process restart.
// settingsFn's returned interval is floored at MinInterval.
func NewWithSettingsFunc(db *storage.DB, dir string, settingsFn func() (time.Duration, int)) *Scheduler {
	return &Scheduler{db: db, dir: dir, settingsFn: settingsFn}
}

// currentSettings returns the interval/keep to use for the next run,
// consulting settingsFn if one was supplied (NewWithSettingsFunc), otherwise
// falling back to the fixed interval/keep from New. The interval is always
// floored at MinInterval as a last line of defense.
func (s *Scheduler) currentSettings() (time.Duration, int) {
	interval, keep := s.interval, s.keep
	if s.settingsFn != nil {
		interval, keep = s.settingsFn()
	}
	if interval < MinInterval {
		interval = MinInterval
	}
	return interval, keep
}

// Run writes a snapshot on the configured interval until ctx is cancelled,
// calling RunOnce each tick. Errors are logged, never fatal — a bad backup
// destination (e.g. disk full) must not take down the process.
//
// Uses a time.Timer (re-armed each iteration with the latest interval from
// currentSettings) rather than a time.Ticker, so a settings change made
// mid-wait via PUT /api/v1/backup/settings is picked up on the very next
// tick instead of only after the process restarts.
func (s *Scheduler) Run(ctx context.Context) {
	interval, _ := s.currentSettings()
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := s.RunOnce(ctx); err != nil {
				slog.Error("backup: scheduled snapshot failed", "err", err)
			}
			interval, _ := s.currentSettings()
			timer.Reset(interval)
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

	_, keep := s.currentSettings()
	if err := s.prune(keep); err != nil {
		slog.Error("backup: prune old snapshots failed", "err", err)
	}
	return nil
}

// prune keeps only the newest `keep` snapshots matching this scheduler's own
// naming pattern in dir, deleting the rest. Files that don't match the
// pattern (e.g. a user's own files in a shared directory) are never touched.
func (s *Scheduler) prune(keep int) error {
	if keep <= 0 {
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
	if len(names) <= keep {
		return nil
	}

	// Names embed a sortable timestamp, so lexicographic sort == chronological.
	sort.Strings(names)
	toDelete := names[:len(names)-keep]
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
