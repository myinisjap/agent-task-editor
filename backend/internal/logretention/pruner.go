// Package logretention implements the optional periodic pruning of
// agent_logs rows for runs in a terminal status, bounding SQLite growth on
// long-lived boards. The active run's logs and the WS replay path are never
// touched because the DELETE predicate only matches runs with a non-null
// completed_at in a terminal status (completed/failed/waiting_human).
//
// Settings (retention days / run interval) are DB-backed
// (log_retention_settings, seeded from config.Defaults()'
// LogRetentionDays/LogRetentionInterval on first migration) and editable at
// runtime via PUT /api/v1/log-retention/settings (or the Health page),
// without a restart — see NewWithSettingsFunc. The env vars
// LOG_RETENTION_DAYS/LOG_RETENTION_INTERVAL now only seed the settings row's
// initial values (and serve as a fallback if the DB read fails); they are no
// longer the live source of truth. Disabled by default (days=0).
package logretention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// MinInterval is the minimum interval a caller is allowed to configure
// between scheduled prune runs (1 minute). Enforced both here (as a floor
// on whatever interval/settings getter is supplied) and by the
// PUT /api/v1/log-retention/settings handler's request validation, so a
// misconfigured or malicious value can never make the pruner thrash.
const MinInterval = 1 * time.Minute

// Pruner periodically deletes agent_logs rows for runs in a terminal status
// whose completed_at is older than `days` days ago.
type Pruner struct {
	q          *gen.Queries
	days       int
	interval   time.Duration
	settingsFn func() (int, time.Duration)
	now        func() time.Time // injectable for tests
}

// New creates a Pruner that, when days > 0, deletes old terminal-run logs
// every interval. days/interval are fixed for the lifetime of the Pruner;
// use NewWithSettingsFunc for a pruner whose days/interval can change at
// runtime. A days value of 0 (or negative) disables pruning: Run/RunOnce
// become no-ops.
func New(q *gen.Queries, days int, interval time.Duration) *Pruner {
	return &Pruner{q: q, days: days, interval: interval, now: time.Now}
}

// NewWithSettingsFunc creates a Pruner that re-reads its days/interval from
// settingsFn before every scheduled run, so runtime changes (e.g. via
// PUT /api/v1/log-retention/settings) take effect without a process
// restart. settingsFn's returned interval is floored at MinInterval; days
// may be 0 (disabled) or any positive value.
func NewWithSettingsFunc(q *gen.Queries, settingsFn func() (int, time.Duration)) *Pruner {
	return &Pruner{q: q, settingsFn: settingsFn, now: time.Now}
}

// currentSettings returns the days/interval to use for the next run,
// consulting settingsFn if one was supplied (NewWithSettingsFunc), otherwise
// falling back to the fixed days/interval from New. The interval is always
// floored at MinInterval as a last line of defense.
func (p *Pruner) currentSettings() (int, time.Duration) {
	days, interval := p.days, p.interval
	if p.settingsFn != nil {
		days, interval = p.settingsFn()
	}
	if interval < MinInterval {
		interval = MinInterval
	}
	return days, interval
}

// Run ticks on the configured interval until ctx is cancelled, calling
// RunOnce each tick. Errors are logged, never fatal.
//
// Uses a time.Timer (re-armed each iteration with the latest interval from
// currentSettings) rather than a time.Ticker, so a settings change made
// mid-wait via PUT /api/v1/log-retention/settings is picked up on the very
// next tick instead of only after the process restarts.
func (p *Pruner) Run(ctx context.Context) {
	_, interval := p.currentSettings()
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := p.RunOnce(ctx); err != nil {
				slog.Error("logretention: prune failed", "err", err)
			}
			_, interval := p.currentSettings()
			timer.Reset(interval)
		}
	}
}

// RunOnce deletes agent_logs rows for runs in a terminal status
// (completed/failed/waiting_human) whose completed_at is older than the
// configured retention days ago. A no-op when days <= 0. Exported so it's
// directly testable/reusable without waiting on the ticker.
func (p *Pruner) RunOnce(ctx context.Context) error {
	days, _ := p.currentSettings()
	if days <= 0 {
		return nil
	}
	cutoff := p.now().UTC().AddDate(0, 0, -days)
	n, err := p.q.DeleteOldAgentLogs(ctx, &cutoff)
	if err != nil {
		return fmt.Errorf("delete old agent logs: %w", err)
	}
	if n > 0 {
		slog.Info("logretention: pruned old agent logs", "deleted", n, "retention_days", days)
	}
	return nil
}
