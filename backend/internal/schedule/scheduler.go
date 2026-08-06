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
	"github.com/myinisjap/agent-task-editor/backend/internal/intake"
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

	// Loaded once per sweep, mirroring tasksource.Importer.Sweep: every
	// schedule firing in this sweep evaluates against the same rule set.
	rules, err := s.q.ListEnabledIntakeRules(ctx)
	if err != nil {
		log.Warn("schedule sweep: list intake rules failed; firing without rule shaping", "err", err)
		rules = nil
	}

	now := time.Now()
	fired := 0
	for _, sched := range schedules {
		if s.fireIfDue(ctx, sched, now, rules) {
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
//
// rules is matched with Source: "schedule" against the schedule's own
// template title/description. Unlike the issue-import path,
// intake.AutoStartAllowed is deliberately NOT enforced here: a schedule's
// target_label is already a human-configured, validated value (see
// validateTargetLabelForRepo in the schedules API handler), not untrusted
// third-party content, so the auto-start safety gate that protects against
// imported issue bodies (#331) doesn't apply to it. A later reader should
// not "fix" this to call AutoStartAllowed — that would incorrectly reject a
// human-authored schedule for lacking an author-association constraint that
// makes no sense for it.
func (s *Scheduler) fireIfDue(ctx context.Context, sched gen.TaskSchedule, now time.Time, rules []gen.IntakeRule) bool {
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
	// last_run_at is stored as wall-clock now (e.g. 10:00:37). Truncate to the
	// minute so the occurrence that already fired isn't re-found by Next().
	// cron.Next() is exclusive of its argument, so from a truncated 10:00:00 it
	// returns the *following* occurrence — the just-fired one is not re-evaluated.
	next := cron.Next(last.Truncate(time.Minute))
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

	// A rule only supplies target_label when the schedule leaves its own
	// empty — the schedule's own explicit target_label is the more specific,
	// human-set config and always wins. Priority and max_cost_usd, however,
	// come from the rule whenever it matches (a schedule has no equivalent
	// field of its own to prefer).
	decision, matched := intake.Match(rules, intake.Candidate{
		Source: "schedule",
		RepoID: repo.ID,
		Title:  tmpl.Title,
		Body:   tmpl.Description,
	})

	labels, err := s.q.ListWorkflowLabels(ctx, *repo.WorkflowID)
	if err != nil {
		log.Warn("schedule sweep: label lookup failed", "workflow_id", *repo.WorkflowID, "err", err)
		return false
	}
	gate, first := workflow.GateLabel(labels)
	fallback := gate
	if fallback == "" {
		fallback = first
	}

	targetLabel := sched.TargetLabel
	if targetLabel == "" && matched && decision.TargetLabel != "" {
		// Validate a rule-supplied label the same way a human-set one is
		// validated (see validateTargetLabelForRepo in the schedules API
		// handler): an invalid rule-supplied label falls back to the gate
		// rather than creating a task nothing can place.
		valid := false
		for _, l := range labels {
			if l.Name == decision.TargetLabel {
				valid = true
				break
			}
		}
		if valid {
			targetLabel = decision.TargetLabel
		} else {
			log.Warn("schedule sweep: rule target_label is not a label in the repo's workflow; ignoring", "rule_id", decision.RuleID, "target_label", decision.TargetLabel)
		}
	}
	if targetLabel == "" {
		if fallback == "" {
			log.Warn("schedule sweep: repo workflow has no labels; skipping")
			return false
		}
		targetLabel = fallback
	}

	taskType := tmpl.Type
	var priority int64
	var maxCostUSD float64
	var matchedRuleID *string
	if matched {
		if decision.Priority != nil {
			priority = *decision.Priority
		}
		if decision.MaxCostUsd != nil {
			maxCostUSD = *decision.MaxCostUsd
		}
		ruleID := decision.RuleID
		matchedRuleID = &ruleID
		if decision.TemplateID != nil && *decision.TemplateID != "" {
			// apply_template_id is a no-op here: the task is always shaped
			// from the schedule's own tmpl (sched.TemplateID) above, per
			// the CRUD handler's write-time rejection of this combination
			// (see intake_rules.go's validate). This can only be reached
			// via a direct DB edit that bypassed that check; log loudly
			// rather than silently ignoring it so it's debuggable.
			log.Warn("schedule sweep: rule apply_template_id has no effect for scheduled tasks; ignoring", "rule_id", decision.RuleID, "template_id", *decision.TemplateID)
		}
	}

	// source_ref must be unique per firing (tasks has a UNIQUE(source,
	// source_ref) index), so it's the schedule id plus a run-specific
	// suffix. HasOpenTaskForSchedule matches on the "<schedule_id>#" prefix.
	sourceRef := sched.ID + "#" + strconv.FormatInt(now.UnixNano(), 10)

	task, err := s.q.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
		ID:            uuid.NewString(),
		Title:         tmpl.Title,
		Description:   description,
		Type:          taskType,
		Label:         targetLabel,
		RepoID:        repo.ID,
		WorkflowID:    *repo.WorkflowID,
		Attachments:   "[]",
		Source:        "schedule",
		SourceRef:     sourceRef,
		Priority:      priority,
		MaxCostUsd:    maxCostUSD,
		MatchedRuleID: matchedRuleID,
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
