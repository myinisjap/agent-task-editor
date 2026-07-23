package tasksource

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// Publisher is satisfied by *ws.Hub — it sends events to all connected clients.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Importer polls all repos with issue sync enabled and creates tasks for
// external items that haven't been imported yet. Deduplication is by
// (source, source_ref): as long as the imported task exists (in any state),
// the item is never imported again. Deleting an imported task while the
// external item still matches the filter causes it to be re-imported on the
// next sweep — to stop that, remove the filter label from the issue instead.
type Importer struct {
	q        *gen.Queries
	hub      Publisher
	interval time.Duration
	source   Source
}

// New creates an Importer that polls the given source on the given interval.
func New(db *sql.DB, hub Publisher, interval time.Duration, source Source) *Importer {
	return &Importer{
		q:        gen.New(db),
		hub:      hub,
		interval: interval,
		source:   source,
	}
}

// Run sweeps on the configured interval until ctx is cancelled.
func (im *Importer) Run(ctx context.Context) {
	ticker := time.NewTicker(im.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			im.Sweep(ctx)
		}
	}
}

// Sweep imports new tasks for every issue-sync-enabled repo.
func (im *Importer) Sweep(ctx context.Context) {
	start := time.Now()
	defer func() { metrics.TasksourceSweepDurationSeconds.Observe(time.Since(start).Seconds()) }()

	log := slog.With("component", "tasksource")
	repos, err := im.q.ListIssueSyncRepos(ctx)
	if err != nil {
		log.Warn("issue import: list repos failed", "err", err)
		return
	}
	if len(repos) == 0 {
		return
	}
	log.Info("issue import: sweep start", "repos", len(repos))

	imported := 0
	for _, repo := range repos {
		imported += im.sweepRepo(ctx, repo)
	}
	log.Info("issue import: sweep done", "imported", imported)
}

// sweepRepo imports tasks for one repo and returns the number created.
func (im *Importer) sweepRepo(ctx context.Context, repo gen.Repo) int {
	log := slog.With("component", "tasksource", "repo", repo.Name)

	// Tasks require a workflow; a repo without one can't receive imports.
	if repo.WorkflowID == nil || *repo.WorkflowID == "" {
		log.Warn("issue import: repo has issue sync enabled but no workflow assigned; skipping")
		return 0
	}

	// Imported tasks land on the workflow's human-gate label (the lowest
	// sort_order agent_ignore label, falling back to the first label) so a human
	// promotes them before an agent picks them up — "not_ready" for the default
	// workflow, the equivalent gate for any custom one.
	labels, err := im.q.ListWorkflowLabels(ctx, *repo.WorkflowID)
	if err != nil {
		log.Warn("issue import: label lookup failed", "workflow_id", *repo.WorkflowID, "err", err)
		return 0
	}
	gate, first := workflow.GateLabel(labels)
	startLabel := gate
	if startLabel == "" {
		startLabel = first
	}
	if startLabel == "" {
		log.Warn("issue import: repo workflow has no labels; skipping")
		return 0
	}

	items, err := im.source.Fetch(ctx, repo)
	if err != nil {
		log.Warn("issue import: fetch failed", "err", err)
		return 0
	}

	created := 0
	for _, item := range items {
		n, err := im.q.CountTasksBySource(ctx, gen.CountTasksBySourceParams{
			Source:    im.source.Name(),
			SourceRef: item.Ref,
		})
		if err != nil {
			log.Warn("issue import: dedupe check failed", "ref", item.Ref, "err", err)
			continue
		}
		if n > 0 {
			continue // already imported
		}

		description := item.Body
		if item.URL != "" {
			if description != "" {
				description += "\n\n"
			}
			description += "_Imported from " + item.URL + "_"
		}

		task, err := im.q.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
			ID:          uuid.NewString(),
			Title:       item.Title,
			Description: description,
			Type:        TaskTypeFromLabels(item.Labels),
			Label:       startLabel,
			RepoID:      repo.ID,
			WorkflowID:  *repo.WorkflowID,
			Attachments: "[]",
			Source:      im.source.Name(),
			SourceRef:   item.Ref,
		})
		if err != nil {
			// A UNIQUE violation here means a concurrent insert won the race —
			// harmless; anything else is worth surfacing.
			log.Warn("issue import: create task failed", "ref", item.Ref, "err", err)
			continue
		}
		created++
		log.Info("issue import: task created", "task_id", task.ID, "ref", item.Ref)
		im.hub.Publish("task.created", map[string]any{
			"id":         task.ID,
			"title":      task.Title,
			"label":      task.Label,
			"repo_id":    task.RepoID,
			"source":     task.Source,
			"source_ref": task.SourceRef,
		})
	}
	return created
}

// TaskTypeFromLabels maps external tracker labels to a board task type
// (feature | bug | chore | spike). First match wins; default is "feature".
func TaskTypeFromLabels(labels []string) string {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "bug", "defect", "regression":
			return "bug"
		case "chore", "maintenance", "dependencies", "ci", "refactor", "cleanup":
			return "chore"
		case "spike", "research", "question", "investigation":
			return "spike"
		case "enhancement", "feature", "feature-request":
			return "feature"
		}
	}
	return "feature"
}
