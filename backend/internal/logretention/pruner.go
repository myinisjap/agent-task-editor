// Package logretention implements the optional periodic pruning of
// agent_logs rows for runs in a terminal status, bounding SQLite growth on
// long-lived boards. Disabled by default (LOG_RETENTION_DAYS=0); the active
// run's logs and the WS replay path are never touched because the DELETE
// predicate only matches runs with a non-null completed_at in a terminal
// status (completed/failed/waiting_human).
package logretention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Pruner periodically deletes agent_logs rows for runs in a terminal status
// whose completed_at is older than `days` days ago.
type Pruner struct {
	q        *gen.Queries
	days     int
	interval time.Duration
	now      func() time.Time // injectable for tests
}

// New creates a Pruner that, when days > 0, deletes old terminal-run logs
// every interval. A days value of 0 (or negative) disables pruning: Run/
// RunOnce become no-ops, matching how BackupDir="" disables the backup
// scheduler.
func New(q *gen.Queries, days int, interval time.Duration) *Pruner {
	return &Pruner{q: q, days: days, interval: interval, now: time.Now}
}

// Run ticks on the configured interval until ctx is cancelled, calling
// RunOnce each tick. Errors are logged, never fatal.
func (p *Pruner) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.RunOnce(ctx); err != nil {
				slog.Error("logretention: prune failed", "err", err)
			}
		}
	}
}

// RunOnce deletes agent_logs rows for runs in a terminal status
// (completed/failed/waiting_human) whose completed_at is older than
// p.days days ago. A no-op when p.days <= 0. Exported so it's directly
// testable/reusable without waiting on the ticker.
func (p *Pruner) RunOnce(ctx context.Context) error {
	if p.days <= 0 {
		return nil
	}
	cutoff := p.now().UTC().AddDate(0, 0, -p.days)
	n, err := p.q.DeleteOldAgentLogs(ctx, &cutoff)
	if err != nil {
		return fmt.Errorf("delete old agent logs: %w", err)
	}
	if n > 0 {
		slog.Info("logretention: pruned old agent logs", "deleted", n, "retention_days", p.days)
	}
	return nil
}
