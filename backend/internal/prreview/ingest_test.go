package prreview

import (
	"context"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func (e *env) task(t *testing.T) gen.Task {
	t.Helper()
	task, err := e.q.GetTaskBySource(context.Background(), gen.GetTaskBySourceParams{Source: "github_pr", SourceRef: "o/r#1"})
	if err != nil {
		t.Fatalf("task not found: %v", err)
	}
	return task
}

func (e *env) count(t *testing.T) int {
	t.Helper()
	tasks, err := e.q.ListTasksBySourceRepo(context.Background(), gen.ListTasksBySourceRepoParams{Source: "github_pr", RepoID: e.repo.ID})
	if err != nil {
		t.Fatal(err)
	}
	return len(tasks)
}

func TestConsider_DedupesOnePerPR(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.forge.prs[1] = pr(1, "aaa1111")
	e.ing.Sweep(ctx)
	e.ing.Sweep(ctx)
	e.ing.ConsiderPR(ctx, "O/R", 1) // webhook + poll race, case-insensitive repo match
	if n := e.count(t); n != 1 {
		t.Fatalf("want 1 task, got %d", n)
	}
	task := e.task(t)
	if task.Label != "pr-review" || task.Type != "chore" {
		t.Fatalf("unexpected task: label=%s type=%s", task.Label, task.Type)
	}
	if task.Description == "" || strings.Contains(task.Description, "Add thing") {
		t.Fatalf("description must be server-written, got %q", task.Description)
	}
	src, err := e.q.GetTaskPRSource(ctx, task.ID)
	if err != nil || src.HeadSha != "aaa1111" || src.PrBody != "body" {
		t.Fatalf("pr source: %+v err=%v", src, err)
	}
}

func TestConsider_SkipsIneligible(t *testing.T) {
	e := newEnv(t)
	p := pr(1, "aaa")
	p.AuthorAssociation = "NONE"
	e.forge.prs[1] = p
	d := pr(2, "bbb")
	d.Draft = true
	e.forge.prs[2] = d
	b := pr(3, "ccc")
	b.HeadRef = "ate-foo-12345678"
	e.forge.prs[3] = b
	e.ing.Sweep(context.Background())
	tasks, _ := e.q.ListTasksBySourceRepo(context.Background(), gen.ListTasksBySourceRepoParams{Source: "github_pr", RepoID: e.repo.ID})
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks, got %d", len(tasks))
	}
}

func finishReview(t *testing.T, e *env, taskID, sha string) {
	t.Helper()
	ctx := context.Background()
	if err := e.engine.Transition(ctx, taskID, "done", "agent", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.q.SetTaskPRSourceReviewed(ctx, gen.SetTaskPRSourceReviewedParams{ReviewedSha: sha, TaskID: taskID}); err != nil {
		t.Fatal(err)
	}
}

func TestConsider_ReReviewOnNewHead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.forge.prs[1] = pr(1, "aaa")
	e.ing.Sweep(ctx)
	task := e.task(t)

	// Mid-run (label not terminal) and a new push: no transition.
	e.forge.prs[1] = pr(1, "bbb")
	e.ing.Sweep(ctx)
	if got := e.task(t).Label; got != "pr-review" {
		t.Fatalf("label=%s", got)
	}

	finishReview(t, e, task.ID, "bbb")
	// Same sha as reviewed: stays done.
	e.ing.Sweep(ctx)
	if got := e.task(t).Label; got != "done" {
		t.Fatalf("same sha should not re-review, label=%s", got)
	}
	// New commits: back to pr-review on the same task.
	e.forge.prs[1] = pr(1, "ccc")
	e.ing.Sweep(ctx)
	if got := e.task(t).Label; got != "pr-review" {
		t.Fatalf("new head should re-review, label=%s", got)
	}
	if n := e.count(t); n != 1 {
		t.Fatalf("want 1 task, got %d", n)
	}
	h, _ := e.q.GetLatestTaskLabelHistory(ctx, task.ID)
	if h.ActorID == nil || *h.ActorID != "pr-review-sync" {
		t.Fatalf("actor = %v", h.ActorID)
	}
}

func TestConsider_NoReReviewWhilePostPending(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.forge.prs[1] = pr(1, "aaa")
	e.ing.Sweep(ctx)
	task := e.task(t)
	if err := e.engine.Transition(ctx, task.ID, "done", "agent", "", ""); err != nil {
		t.Fatal(err)
	}
	runID := "run-1"
	_ = e.q.CreatePRReviewPost(ctx, gen.CreatePRReviewPostParams{ID: "p1", TaskID: task.ID, AgentRunID: &runID, HeadSha: "aaa", Summary: "s", CommentsJson: "[]", ResolvedJson: "[]", Seq: 1})
	e.forge.prs[1] = pr(1, "bbb")
	e.ing.Sweep(ctx)
	if got := e.task(t).Label; got != "done" {
		t.Fatalf("must wait for the pending post, label=%s", got)
	}
}
