package storage

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestMigration088_SideEffectKindAndRepoDefaults(t *testing.T) {
	db, err := Open(t.TempDir() + "/m088.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(ctx)
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: uuid.NewString(), Name: "r", Path: "/tmp/r-" + uuid.NewString(), WorkflowID: &wfs[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if repo.PrReviewEnabled != 0 || repo.PrReviewSkipDrafts != 1 || repo.PrReviewAuthorAssocs != `["OWNER","MEMBER","COLLABORATOR"]` {
		t.Fatalf("unexpected defaults: %+v", repo)
	}
	task, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: wfs[0].ID, RepoID: repo.ID, Label: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.EnqueueTaskSideEffect(ctx, gen.EnqueueTaskSideEffectParams{ID: uuid.NewString(), TaskID: task.ID, Kind: "pr_review_post"}); err != nil {
		t.Fatalf("pr_review_post kind rejected: %v", err)
	}
	if err := q.EnqueueTaskSideEffect(ctx, gen.EnqueueTaskSideEffectParams{ID: uuid.NewString(), TaskID: task.ID, Kind: "bogus"}); err == nil {
		t.Fatal("expected CHECK violation for unknown kind")
	}
}

func TestSeedPRReviewWorkflow_OnceAndWithDefault(t *testing.T) {
	db, err := Open(t.TempDir() + "/seed.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := SeedPRReviewWorkflow(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(ctx)
	if len(wfs) != 2 {
		t.Fatalf("want Default + PR review, got %d workflows", len(wfs))
	}
	wf, err := q.GetWorkflowByName(ctx, PRReviewWorkflowName)
	if err != nil {
		t.Fatal(err)
	}
	labels, _ := q.ListWorkflowLabels(ctx, wf.ID)
	names := map[string]bool{}
	for _, l := range labels {
		names[l.Name] = true
	}
	if !names["pr-review"] || !names["done"] || !names["needs-attention"] {
		t.Fatalf("labels: %v", names)
	}
	// Deleting it is not undone by a later seed (marker).
	if err := q.DeleteWorkflow(ctx, wf.ID); err != nil {
		t.Fatal(err)
	}
	if err := SeedPRReviewWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetWorkflowByName(ctx, PRReviewWorkflowName); err == nil {
		t.Fatal("workflow re-created after deletion")
	}
}
