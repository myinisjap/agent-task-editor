package agent_test

import (
	"context"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func runReviewJob(t *testing.T, res agent.Result) (*gen.Queries, string) {
	t.Helper()
	db := openAgentTestDB(t)
	pub := &testPub{}
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), pub)
	pool := agent.NewPool(1, db.SQL(), engine, pub)
	wfs, _ := q.ListWorkflows(context.Background())
	taskID, agCfgID, runID := seedJobFixtures(t, q, wfs[0].ID)
	_, stop := startPool(t, pool)
	job := buildJob(runID, taskID, agCfgID, wfs[0].ID, t.TempDir(), &mockProvider{result: res})
	job.Input.PRReview = &agent.PRReviewInput{HeadSHA: "abc123", PriorFindings: []agent.PriorFinding{{ID: "F1-1"}}}
	pool.Submit(job)
	waitForLabel(t, q, taskID, "review-plan")
	stop()
	return q, taskID
}

func TestPool_PRReviewResultStoredAsPendingPost(t *testing.T) {
	q, taskID := runReviewJob(t, agent.Result{Status: "completed", Outcome: "success", PRReview: &agent.PRReviewResult{
		Summary: "Two issues.",
		Comments: []agent.PRReviewComment{
			{Path: "a.go", Line: 4, Body: "bad"},
			{Path: "../x", Line: 4, Body: "traversal"},
		},
		ResolvedPrior: []string{"F1-1", "F7-7"},
	}})
	posts, err := q.ListPendingPRReviewPosts(context.Background(), taskID)
	if err != nil || len(posts) != 1 {
		t.Fatalf("posts=%d err=%v", len(posts), err)
	}
	p := posts[0]
	if p.Seq != 1 || p.HeadSha != "abc123" || p.Summary != "Two issues." {
		t.Fatalf("post: %+v", p)
	}
	if p.CommentsJson != `[{"path":"a.go","line":4,"body":"bad"}]` {
		t.Fatalf("comments: %s", p.CommentsJson)
	}
	if p.ResolvedJson != `["F1-1"]` {
		t.Fatalf("resolved: %s", p.ResolvedJson)
	}
}

func TestPool_PRReviewFallsBackToNotesWhenToolNotCalled(t *testing.T) {
	notes := "Looks fine; one nit."
	q, taskID := runReviewJob(t, agent.Result{Status: "completed", Outcome: "success", Notes: &notes})
	posts, _ := q.ListPendingPRReviewPosts(context.Background(), taskID)
	if len(posts) != 1 || posts[0].Summary != notes || posts[0].CommentsJson != "[]" {
		t.Fatalf("posts: %+v", posts)
	}
}
