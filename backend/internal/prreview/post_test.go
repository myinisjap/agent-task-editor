package prreview

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func (e *env) poster() *Poster {
	p := NewPoster(e.db.SQL(), nil)
	p.ForgeFor = func(string) (forge.Forge, string, bool) { return e.forge, "o/r", true }
	return p
}

// reviewedTask creates a review task with one pending post.
func (e *env) reviewedTask(t *testing.T, comments string, resolved string) (gen.Task, gen.TaskPrReviewPost) {
	t.Helper()
	ctx := context.Background()
	e.forge.prs[1] = pr(1, "aaa")
	e.ing.Sweep(ctx)
	task := e.task(t)
	run := "run-" + task.ID
	seq, _ := e.q.NextPRReviewPostSeq(ctx, task.ID)
	if err := e.q.CreatePRReviewPost(ctx, gen.CreatePRReviewPostParams{
		ID: "post-" + run, TaskID: task.ID, AgentRunID: &run, HeadSha: "aaa", Summary: "Looks mostly fine.",
		CommentsJson: comments, ResolvedJson: resolved, Seq: seq,
	}); err != nil {
		t.Fatal(err)
	}
	pend, _ := e.q.ListPendingPRReviewPosts(ctx, task.ID)
	return task, pend[0]
}

func TestPost_PostsOnceWithMarkerAndRecordsReviewed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	task, post := e.reviewedTask(t, `[{"path":"a.go","line":3,"body":"nil deref"}]`, `[]`)
	p := e.poster()
	repo, _ := e.q.GetRepo(ctx, e.repo.ID)
	for i := 0; i < 2; i++ { // a second delivery has nothing pending
		if err := p.Post(ctx, task, repo); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.forge.posted) != 1 {
		t.Fatalf("want 1 review posted, got %d", len(e.forge.posted))
	}
	sub := e.forge.posted[0]
	if !strings.Contains(sub.Body, Marker(post.ID)) || sub.CommitSHA != "aaa" {
		t.Fatalf("body/commit: %q %q", sub.Body, sub.CommitSHA)
	}
	if len(sub.Comments) != 1 || !strings.HasPrefix(sub.Comments[0].Body, "**[F1-1]**") {
		t.Fatalf("inline: %+v", sub.Comments)
	}
	src, _ := e.q.GetTaskPRSource(ctx, task.ID)
	if src.ReviewedSha != "aaa" || src.LastPostedAt == nil {
		t.Fatalf("source: %+v", src)
	}
}

func TestPost_MarkerAlreadyOnForgeSkipsPost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	task, post := e.reviewedTask(t, `[]`, `[]`)
	e.forge.reviews = []forge.Review{{Body: "earlier\n" + Marker(post.ID)}}
	repo, _ := e.q.GetRepo(ctx, e.repo.ID)
	if err := e.poster().Post(ctx, task, repo); err != nil {
		t.Fatal(err)
	}
	if len(e.forge.posted) != 0 {
		t.Fatal("must not post again when the marker is already on the PR")
	}
	if pend, _ := e.q.ListPendingPRReviewPosts(ctx, task.ID); len(pend) != 0 {
		t.Fatal("post should be recorded as done")
	}
	posted, _ := e.q.ListPostedPRReviewPosts(ctx, task.ID)
	if len(posted) != 1 || posted[0].PostedMode != "existing" {
		t.Fatalf("posted: %+v", posted)
	}
}

func TestPost_MarkerInIssueCommentSkipsPost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	task, post := e.reviewedTask(t, `[]`, `[]`)
	e.forge.comments = []forge.IssueComment{{Body: Marker(post.ID)}}
	repo, _ := e.q.GetRepo(ctx, e.repo.ID)
	if err := e.poster().Post(ctx, task, repo); err != nil || len(e.forge.posted) != 0 {
		t.Fatalf("err=%v posted=%d", err, len(e.forge.posted))
	}
}

func TestPost_InlineAnchorFallsBackToBodyOnlyReview(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.forge.failInline = true
	task, _ := e.reviewedTask(t, `[{"path":"a.go","line":3,"body":"nil deref"}]`, `[]`)
	repo, _ := e.q.GetRepo(ctx, e.repo.ID)
	if err := e.poster().Post(ctx, task, repo); err != nil {
		t.Fatal(err)
	}
	if len(e.forge.posted) != 1 || len(e.forge.posted[0].Comments) != 0 {
		t.Fatalf("posted: %+v", e.forge.posted)
	}
	if !strings.Contains(e.forge.posted[0].Body, "`a.go:3` [F1-1] nil deref") {
		t.Fatalf("body: %q", e.forge.posted[0].Body)
	}
	posted, _ := e.q.ListPostedPRReviewPosts(ctx, task.ID)
	if posted[0].PostedMode != "review_no_inline" {
		t.Fatalf("mode %s", posted[0].PostedMode)
	}
}

func TestPost_TransientErrorRetriesAndPostsOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	task, _ := e.reviewedTask(t, `[]`, `[]`)
	repo, _ := e.q.GetRepo(ctx, e.repo.ID)
	e.forge.failAllReviews = errors.New("boom 502")
	if err := e.poster().Post(ctx, task, repo); err == nil {
		t.Fatal("expected error so the outbox retries")
	}
	if pend, _ := e.q.ListPendingPRReviewPosts(ctx, task.ID); len(pend) != 1 {
		t.Fatal("post must stay pending")
	}
	e.forge.failAllReviews = nil
	if err := e.poster().Post(ctx, task, repo); err != nil {
		t.Fatal(err)
	}
	if len(e.forge.posted) != 1 {
		t.Fatalf("posted %d", len(e.forge.posted))
	}
}

func TestPost_ReReviewListsResolvedFindings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	task, _ := e.reviewedTask(t, `[{"path":"a.go","line":3,"body":"nil deref"}]`, `[]`)
	repo, _ := e.q.GetRepo(ctx, e.repo.ID)
	if err := e.poster().Post(ctx, task, repo); err != nil {
		t.Fatal(err)
	}
	run := "run-2"
	seq, _ := e.q.NextPRReviewPostSeq(ctx, task.ID)
	if seq != 2 {
		t.Fatalf("seq %d", seq)
	}
	_ = e.q.CreatePRReviewPost(ctx, gen.CreatePRReviewPostParams{ID: "p2", TaskID: task.ID, AgentRunID: &run, HeadSha: "bbb", Summary: "Second pass.", CommentsJson: `[]`, ResolvedJson: `["F1-1"]`, Seq: seq})
	if err := e.poster().Post(ctx, task, repo); err != nil {
		t.Fatal(err)
	}
	body := e.forge.posted[1].Body
	if !strings.Contains(body, "Resolved since the previous review") || !strings.Contains(body, "[F1-1]") {
		t.Fatalf("body: %q", body)
	}
}

func TestPost_IgnoresNonReviewTasks(t *testing.T) {
	e := newEnv(t)
	err := e.poster().Post(context.Background(), gen.Task{Source: "github"}, e.repo)
	if err != nil || len(e.forge.posted) != 0 {
		t.Fatalf("err=%v", err)
	}
}
