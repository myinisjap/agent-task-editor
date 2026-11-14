package prreview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// Publisher publishes WebSocket events.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Ingester turns eligible open PRs into review tasks and re-opens a task's
// review when new commits land.
type Ingester struct {
	db     *sql.DB
	q      *gen.Queries
	engine *workflow.Engine
	pub    Publisher
	// ForgeFor resolves a repo's forge and parsed name; defaults to forge.ForRemote.
	ForgeFor func(remoteURL string) (forge.Forge, string, bool)
}

// New builds an Ingester.
func New(db *sql.DB, engine *workflow.Engine, pub Publisher) *Ingester {
	return &Ingester{db: db, q: gen.New(db), engine: engine, pub: pub, ForgeFor: forge.ForRemote}
}

// SourceRef is the dedupe key for a PR: owner/repo#N.
func SourceRef(repoName string, number int) string { return fmt.Sprintf("%s#%d", repoName, number) }

// ConsiderPR is the webhook path: re-fetch the PR through the forge and apply
// the same logic as the polling sweep to every enabled repo that matches.
func (in *Ingester) ConsiderPR(ctx context.Context, repoFullName string, number int) {
	repos, err := in.q.ListPRReviewRepos(ctx)
	if err != nil {
		slog.Warn("prreview: list repos", "err", err)
		return
	}
	for _, repo := range repos {
		f, name, ok := in.ForgeFor(derefStr(repo.RemoteUrl))
		if !ok || !strings.EqualFold(name, repoFullName) {
			continue
		}
		pr, err := f.GetPR(ctx, name, number)
		if err != nil {
			slog.Warn("prreview: get pr", "repo", name, "pr", number, "err", err)
			continue
		}
		in.consider(ctx, repo, f, name, pr)
	}
}

// Sweep is the polling fallback: one ListOpenPRs per enabled repo.
func (in *Ingester) Sweep(ctx context.Context) {
	repos, err := in.q.ListPRReviewRepos(ctx)
	if err != nil {
		slog.Warn("prreview: list repos", "err", err)
		return
	}
	for _, repo := range repos {
		if ctx.Err() != nil {
			return
		}
		f, name, ok := in.ForgeFor(derefStr(repo.RemoteUrl))
		if !ok {
			continue
		}
		prs, err := f.ListOpenPRs(ctx, name)
		if err != nil {
			slog.Warn("prreview: list open prs", "repo", name, "err", err)
			continue
		}
		for _, pr := range prs {
			in.consider(ctx, repo, f, name, pr)
		}
	}
}

// Run sweeps every interval until ctx is cancelled.
func (in *Ingester) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		in.Sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (in *Ingester) consider(ctx context.Context, repo gen.Repo, f forge.Forge, repoName string, pr forge.PullRequest) {
	source := forge.SourceForForge(f.Name())
	ref := SourceRef(repoName, pr.Number)
	eligible, reason := Eligible(repo, pr)

	task, err := in.q.GetTaskBySource(ctx, gen.GetTaskBySourceParams{Source: source, SourceRef: ref})
	if errors.Is(err, sql.ErrNoRows) {
		if !eligible {
			slog.Debug("prreview: skip pr", "ref", ref, "reason", reason)
			return
		}
		in.create(ctx, repo, source, ref, pr)
		return
	}
	if err != nil {
		slog.Warn("prreview: lookup task", "ref", ref, "err", err)
		return
	}
	in.refresh(ctx, repo, task, pr, eligible)
}

func (in *Ingester) upsertSource(ctx context.Context, q *gen.Queries, taskID, repoID string, pr forge.PullRequest) error {
	state := pr.State
	if state == "" {
		state = "open"
	}
	return q.UpsertTaskPRSource(ctx, gen.UpsertTaskPRSourceParams{
		TaskID: taskID, RepoID: repoID, PrNumber: int64(pr.Number), PrUrl: pr.URL, PrTitle: pr.Title, PrBody: pr.Body,
		PrAuthor: pr.Author, HeadRef: pr.HeadRef, HeadSha: pr.HeadSHA, BaseRef: pr.BaseRef, PrState: state,
	})
}

// resolveTarget picks the workflow and the label new review tasks start on.
func (in *Ingester) resolveTarget(ctx context.Context, repo gen.Repo) (wfID, label string, ok bool) {
	if repo.PrReviewWorkflowID != nil && *repo.PrReviewWorkflowID != "" {
		wfID = *repo.PrReviewWorkflowID
	} else if wf, err := in.q.GetWorkflowByName(ctx, "PR review"); err == nil {
		wfID = wf.ID
	} else {
		slog.Warn("prreview: no PR review workflow configured or seeded", "repo", repo.Name)
		return "", "", false
	}
	labels, err := in.q.ListWorkflowLabels(ctx, wfID)
	if err != nil || len(labels) == 0 {
		slog.Warn("prreview: workflow has no labels", "workflow_id", wfID, "err", err)
		return "", "", false
	}
	if repo.PrReviewTargetLabel != "" {
		return wfID, repo.PrReviewTargetLabel, true
	}
	sorted := append([]gen.WorkflowLabel(nil), labels...)
	for i := range sorted {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].SortOrder < sorted[i].SortOrder {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for _, l := range sorted {
		if l.AgentIgnore == 0 && l.IsTerminal == 0 {
			return wfID, l.Name, true
		}
	}
	slog.Warn("prreview: workflow has no review label", "workflow_id", wfID)
	return "", "", false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func (in *Ingester) create(ctx context.Context, repo gen.Repo, source, ref string, pr forge.PullRequest) {
	wfID, label, ok := in.resolveTarget(ctx, repo)
	if !ok {
		return
	}
	tx, err := in.db.BeginTx(ctx, nil)
	if err != nil {
		slog.Warn("prreview: begin tx", "err", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck
	qtx := in.q.WithTx(tx)
	// The description is server-written and trusted: the PR title/body/diff
	// are delivered separately, fenced as untrusted, in the prompt.
	desc := fmt.Sprintf("Review pull request %s (head %s, base %s). The PR title, body and diff are provided in the PULL REQUEST section.",
		pr.URL, shortSHA(pr.HeadSHA), pr.BaseRef)
	task, err := qtx.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
		ID:          uuid.NewString(),
		Title:       fmt.Sprintf("PR review: %s %s", ref, truncate(oneLine(pr.Title), 120)),
		Description: desc,
		Type:        "chore",
		Label:       label,
		RepoID:      repo.ID,
		WorkflowID:  wfID,
		Attachments: "[]",
		Source:      source,
		SourceRef:   ref,
	})
	if err != nil {
		// Unique-index conflict: a concurrent webhook/poll created it first.
		slog.Debug("prreview: create task", "ref", ref, "err", err)
		return
	}
	if err := in.upsertSource(ctx, qtx, task.ID, repo.ID, pr); err != nil {
		slog.Warn("prreview: record pr source", "ref", ref, "err", err)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Warn("prreview: commit", "ref", ref, "err", err)
		return
	}
	slog.Info("prreview: review task created", "task_id", task.ID, "ref", ref)
	if in.pub != nil {
		in.pub.Publish(wsevent.TypeTaskCreated, wsevent.TaskCreated{
			ID: task.ID, Title: task.Title, Label: task.Label, RepoID: task.RepoID,
			Source: &task.Source, SourceRef: &task.SourceRef,
		})
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (in *Ingester) refresh(ctx context.Context, repo gen.Repo, task gen.Task, pr forge.PullRequest, eligible bool) {
	if err := in.upsertSource(ctx, in.q, task.ID, repo.ID, pr); err != nil {
		slog.Warn("prreview: update pr source", "task_id", task.ID, "err", err)
		return
	}
	if pr.State != "open" || !eligible || task.Archived != 0 || task.ActiveAgentRunID != nil {
		return
	}
	src, err := in.q.GetTaskPRSource(ctx, task.ID)
	if err != nil || src.HeadSha == "" || src.HeadSha == src.ReviewedSha {
		return
	}
	if pend, perr := in.q.ListPendingPRReviewPosts(ctx, task.ID); perr != nil || len(pend) > 0 {
		return // a posted-but-not-yet-delivered review is still queued
	}
	labels, err := in.q.ListWorkflowLabels(ctx, task.WorkflowID)
	if err != nil {
		return
	}
	onTerminal := false
	for _, l := range labels {
		if l.Name == task.Label && l.IsTerminal != 0 {
			onTerminal = true
		}
	}
	if !onTerminal {
		return // review queued or running; the next sweep sees the new head
	}
	_, target, ok := in.resolveTarget(ctx, repo)
	if !ok {
		return
	}
	if err := in.engine.Transition(ctx, task.ID, target, workflow.TriggerHuman, workflow.ActorPRReviewSync, "PR updated: re-review"); err != nil {
		slog.Warn("prreview: re-review transition", "task_id", task.ID, "err", err)
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
