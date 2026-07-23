// Package schedule fires task_schedules on their cron interval, creating a
// task from the linked template. It mirrors the tasksource.Importer poll
// loop shape (ticker -> sweep -> dedupe -> create -> publish).
package schedule

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/cronexpr"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// Publisher is satisfied by *ws.Hub — it sends events to all connected clients.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Scheduler polls all enabled task_schedules on the configured interval and
// instantiates a task from the linked template when a schedule is due. Each
// firing creates a task with source="schedule" and source_ref of the form
// "<schedule_id>#<run marker>" (source_ref must be unique per task, but a
// schedule fires repeatedly). A schedule is skipped while an open
// (non-archived, non-terminal-label) task from a prior firing still exists,
// so an unfinished run is never stacked on top of.
type Scheduler struct {
	q        *gen.Queries
	hub      Publisher
	interval time.Duration
}

// New creates a Scheduler that sweeps on the given interval.
func New(db *sql.DB, hub Publisher, interval time.Duration) *Scheduler {
	return &Scheduler{
		q:        gen.New(db),
		hub:      hub,
		interval: interval,
	}
}

// Run sweeps on the configured interval until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Sweep(ctx)
		}
	}
}

// Sweep fires every enabled schedule that is due.
func (s *Scheduler) Sweep(ctx context.Context) {
	start := time.Now()
	defer func() { metrics.ScheduleSweepDurationSeconds.Observe(time.Since(start).Seconds()) }()

	log := slog.With("component", "schedule")
	schedules, err := s.q.ListEnabledTaskSchedules(ctx)
	if err != nil {
		log.Warn("schedule sweep: list schedules failed", "err", err)
		return
	}
	if len(schedules) == 0 {
		return
	}

	now := time.Now()
	fired := 0
	for _, sched := range schedules {
		if s.fireIfDue(ctx, sched, now) {
			fired++
		}
	}
	if fired > 0 {
		log.Info("schedule sweep: done", "fired", fired, "checked", len(schedules))
	}
}

// fireIfDue evaluates one schedule and creates a task if it is due, has no
// open task outstanding, and its repo has a workflow assigned. It returns
// true if a task was created.
func (s *Scheduler) fireIfDue(ctx context.Context, sched gen.TaskSchedule, now time.Time) bool {
	log := slog.With("component", "schedule", "schedule_id", sched.ID)

	cron, err := cronexpr.Parse(sched.CronExpr)
	if err != nil {
		log.Warn("schedule sweep: invalid cron expression; skipping", "cron_expr", sched.CronExpr, "err", err)
		return false
	}

	last := sched.CreatedAt
	if sched.LastRunAt != nil {
		last = *sched.LastRunAt
	}
	// Next() is exclusive of its argument, so back up one minute before
	// asking: this way a fire time that lands exactly on `last` (e.g. a
	// schedule that was just created and immediately matches, or one whose
	// last run was recorded at exactly a matching minute) is still found
	// rather than skipped.
	next := cron.Next(last.Add(-time.Minute))
	if next.After(now) {
		return false // not due yet
	}

	scheduleID := sched.ID
	openCount, err := s.q.HasOpenTaskForSchedule(ctx, &scheduleID)
	if err != nil {
		log.Warn("schedule sweep: open-task check failed", "err", err)
		return false
	}
	if openCount > 0 {
		log.Info("schedule sweep: skipping, open task from this schedule already exists")
		return false
	}

	repo, err := s.q.GetRepo(ctx, sched.RepoID)
	if err != nil {
		log.Warn("schedule sweep: repo lookup failed", "repo_id", sched.RepoID, "err", err)
		return false
	}
	if repo.WorkflowID == nil || *repo.WorkflowID == "" {
		log.Warn("schedule sweep: repo has no workflow assigned; skipping")
		return false
	}

	tmpl, err := s.q.GetTaskTemplate(ctx, sched.TemplateID)
	if err != nil {
		log.Warn("schedule sweep: template lookup failed", "template_id", sched.TemplateID, "err", err)
		return false
	}

	description := tmpl.Description
	if description != "" {
		description += "\n\n"
	}
	description += "_Created from schedule " + sched.CronExpr + "_"

	targetLabel := sched.TargetLabel
	if targetLabel == "" {
		labels, err := s.q.ListWorkflowLabels(ctx, *repo.WorkflowID)
		if err != nil {
			log.Warn("schedule sweep: label lookup failed", "workflow_id", *repo.WorkflowID, "err", err)
			return false
		}
		gate, first := workflow.GateLabel(labels)
		if gate != "" {
			targetLabel = gate
		} else if first != "" {
			targetLabel = first
		} else {
			log.Warn("schedule sweep: repo workflow has no labels; skipping")
			return false
		}
	}

	// source_ref must be unique per firing (tasks has a UNIQUE(source,
	// source_ref) index), so it's the schedule id plus a run-specific
	// suffix. HasOpenTaskForSchedule matches on the "<schedule_id>#" prefix.
	sourceRef := sched.ID + "#" + strconv.FormatInt(now.UnixNano(), 10)

	task, err := s.q.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
		ID:          uuid.NewString(),
		Title:       tmpl.Title,
		Description: description,
		Type:        tmpl.Type,
		Label:       targetLabel,
		RepoID:      repo.ID,
		WorkflowID:  *repo.WorkflowID,
		Attachments: "[]",
		Source:      "schedule",
		SourceRef:   sourceRef,
	})
	if err != nil {
		log.Warn("schedule sweep: create task failed", "err", err)
		return false
	}

	if err := s.q.SetTaskScheduleLastRun(ctx, gen.SetTaskScheduleLastRunParams{
		LastRunAt: &now,
		ID:        sched.ID,
	}); err != nil {
		log.Warn("schedule sweep: failed to record last_run_at", "err", err)
	}

	log.Info("schedule sweep: task created", "task_id", task.ID, "template_id", tmpl.ID, "repo_id", repo.ID)
	s.hub.Publish("task.created", map[string]any{
		"id":         task.ID,
		"title":      task.Title,
		"label":      task.Label,
		"repo_id":    task.RepoID,
		"source":     task.Source,
		"source_ref": task.SourceRef,
	})
	return true
}
