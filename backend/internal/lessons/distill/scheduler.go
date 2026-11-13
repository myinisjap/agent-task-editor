package distill

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Run marks runs left running by a restart as failed, then runs due repos
// every Tick until ctx is cancelled. Manual runs started through Start also
// stop with ctx.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()
	now := s.clock()
	if err := s.Q.FailRunningLessonDistillRuns(ctx, &now); err != nil {
		slog.Warn("lesson_distill: recover running runs", "err", err)
	}
	tick := s.Tick
	if tick <= 0 {
		tick = defaultTick
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}

// RunDue runs every repo whose last run is older than the configured interval
// (the rule-proposal interval setting) and that has evidence newer than that
// run, one at a time, and prunes old run rows. Errors are logged, never fatal.
func (s *Service) RunDue(ctx context.Context) {
	if n, err := s.Q.DeleteOldLessonDistillRuns(ctx, s.clock().AddDate(0, 0, -RunRetentionDays)); err != nil {
		slog.Warn("lesson_distill: prune runs", "err", err)
	} else if n > 0 {
		slog.Info("lesson_distill: pruned old runs", "rows", n)
	}
	settings, err := s.Q.GetAutonomySettings(ctx)
	if err != nil {
		slog.Warn("lesson_distill: load settings", "err", err)
		return
	}
	if settings.ProposalProviderConfigID == nil || *settings.ProposalProviderConfigID == "" || settings.ProposalIntervalHours <= 0 {
		return
	}
	interval := time.Duration(settings.ProposalIntervalHours) * time.Hour
	repos, err := s.Q.ListRepos(ctx)
	if err != nil {
		slog.Warn("lesson_distill: list repos", "err", err)
		return
	}
	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		if !s.due(ctx, repo.ID, interval) {
			continue
		}
		if _, err := s.RunRepo(ctx, repo.ID, TriggerSchedule, "policy"); err != nil && !errors.Is(err, ErrRunning) {
			slog.Warn("lesson_distill: scheduled run", "repo_id", repo.ID, "err", err)
			if errors.Is(err, ErrNoProvider) || errors.Is(err, ErrNoModel) {
				return
			}
		}
	}
}

// due reports whether a scheduled run should start: the last run is older than
// the interval and there is evidence newer than it.
func (s *Service) due(ctx context.Context, repoID string, interval time.Duration) bool {
	after := s.clock().AddDate(0, 0, -WindowDays)
	last, err := s.Q.GetLatestLessonDistillRun(ctx, repoID)
	switch {
	case err == nil:
		if s.clock().Sub(last.StartedAt) < interval {
			return false
		}
		if last.StartedAt.After(after) {
			after = last.StartedAt
		}
	case !errors.Is(err, sql.ErrNoRows):
		return false
	}
	n, err := s.Q.CountRepoDistillSignals(ctx, gen.CountRepoDistillSignalsParams{RepoID: repoID, After: storage.FormatTime(after)})
	return err == nil && n > 0
}
