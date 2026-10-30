package propose

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Run marks runs left running by a restart as failed, then runs due gates
// every Tick until ctx is cancelled. Manual runs started through Start also
// stop with ctx.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()
	now := s.clock()
	if err := s.Q.FailRunningProposalRuns(ctx, &now); err != nil {
		slog.Warn("autonomy_propose: recover running runs", "err", err)
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

// RunDue runs every gate whose last run is older than the configured
// interval, one at a time, and prunes old run rows. Scheduled runs only cover
// gates whose mode is not ask: an ask rule changes nothing at an ask gate.
// Errors are logged, never fatal.
func (s *Service) RunDue(ctx context.Context) {
	if n, err := s.Q.DeleteOldProposalRuns(ctx, s.clock().AddDate(0, 0, -RunRetentionDays)); err != nil {
		slog.Warn("autonomy_propose: prune runs", "err", err)
	} else if n > 0 {
		slog.Info("autonomy_propose: pruned old runs", "rows", n)
	}
	settings, err := s.Q.GetAutonomySettings(ctx)
	if err != nil {
		slog.Warn("autonomy_propose: load settings", "err", err)
		return
	}
	if settings.ProposalProviderConfigID == nil || *settings.ProposalProviderConfigID == "" || settings.ProposalIntervalHours <= 0 {
		return
	}
	interval := time.Duration(settings.ProposalIntervalHours) * time.Hour
	workflows, err := s.Q.ListWorkflows(ctx)
	if err != nil {
		slog.Warn("autonomy_propose: list workflows", "err", err)
		return
	}
	for _, wf := range workflows {
		labels, err := s.Q.ListWorkflowLabels(ctx, wf.ID)
		if err != nil {
			continue
		}
		transitions, err := s.Q.ListWorkflowTransitions(ctx, wf.ID)
		if err != nil {
			continue
		}
		def := needsme.WorkflowDef{Labels: labels, Transitions: transitions}
		for _, l := range labels {
			if ctx.Err() != nil {
				return
			}
			if l.AutonomyMode == "ask" || !needsme.IsHumanGate(def, l.Name) {
				continue
			}
			last, err := s.Q.GetLatestProposalRun(ctx, gen.GetLatestProposalRunParams{WorkflowID: wf.ID, Label: l.Name})
			if err == nil && s.clock().Sub(last.StartedAt) < interval {
				continue
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if _, err := s.RunGate(ctx, wf.ID, l.Name, TriggerSchedule, "policy"); err != nil && !errors.Is(err, ErrRunning) {
				slog.Warn("autonomy_propose: scheduled run", "workflow_id", wf.ID, "label", l.Name, "err", err)
				if errors.Is(err, ErrNoProvider) || errors.Is(err, ErrNoModel) {
					return
				}
			}
		}
	}
}
