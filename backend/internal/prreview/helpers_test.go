package prreview

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// fakeForge implements the slice of forge.Forge the review path uses; any other
// method panics via the nil embedded interface.
type fakeForge struct {
	forge.Forge
	prs       map[int]forge.PullRequest
	reviews   []forge.Review
	comments  []forge.IssueComment
	posted    []forge.ReviewSubmission
	issueCmts []string
	// failInline makes a review with inline comments fail with ErrInlineAnchor.
	failInline bool
	// failAllReviews makes every PostCommentReview fail (non-anchor error).
	failAllReviews error
}

func (f *fakeForge) Name() string { return "github" }
func (f *fakeForge) GetPR(_ context.Context, _ string, n int) (forge.PullRequest, error) {
	pr, ok := f.prs[n]
	if !ok {
		return forge.PullRequest{}, errors.New("not found")
	}
	return pr, nil
}
func (f *fakeForge) ListOpenPRs(context.Context, string) ([]forge.PullRequest, error) {
	var out []forge.PullRequest
	for _, p := range f.prs {
		if p.State == "open" {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeForge) PRReviews(context.Context, string, int) ([]forge.Review, error) {
	return f.reviews, nil
}
func (f *fakeForge) GetIssueComments(context.Context, string, int) ([]forge.IssueComment, error) {
	return f.comments, nil
}
func (f *fakeForge) CommentOnIssue(_ context.Context, _ string, _ int, body string) error {
	f.issueCmts = append(f.issueCmts, body)
	return nil
}
func (f *fakeForge) PostCommentReview(_ context.Context, _ string, _ int, r forge.ReviewSubmission) (string, error) {
	if f.failAllReviews != nil {
		return "", f.failAllReviews
	}
	if f.failInline && len(r.Comments) > 0 {
		return "", forge.ErrInlineAnchor
	}
	f.posted = append(f.posted, r)
	return "rev-1", nil
}

type env struct {
	db     *storage.DB
	q      *gen.Queries
	engine *workflow.Engine
	repo   gen.Repo
	forge  *fakeForge
	ing    *Ingester
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := storage.Open(t.TempDir() + "/pr.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := storage.SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := storage.SeedPRReviewWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	q := gen.New(db.SQL())
	wf, _ := q.GetWorkflowByName(ctx, storage.PRReviewWorkflowName)
	url := "https://github.com/o/r.git"
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: uuid.NewString(), Name: "r", Path: t.TempDir(), RemoteUrl: &url, WorkflowID: &wf.ID})
	if err != nil {
		t.Fatal(err)
	}
	repo, err = q.SetRepoPRReview(ctx, gen.SetRepoPRReviewParams{
		ID: repo.ID, PrReviewEnabled: 1, PrReviewAuthorAssocs: `["OWNER","MEMBER","COLLABORATOR"]`, PrReviewSkipDrafts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ff := &fakeForge{prs: map[int]forge.PullRequest{}}
	engine := workflow.New(db.SQL(), nil)
	ing := New(db.SQL(), engine, nil)
	ing.ForgeFor = func(string) (forge.Forge, string, bool) { return ff, "o/r", true }
	return &env{db: db, q: q, engine: engine, repo: repo, forge: ff, ing: ing}
}

func pr(n int, sha string) forge.PullRequest {
	return forge.PullRequest{
		Number: n, Title: "Add thing", Body: "body", URL: "https://github.com/o/r/pull/1", Author: "alice",
		AuthorAssociation: "MEMBER", HeadRef: "feature/x", HeadSHA: sha, BaseRef: "main", State: "open",
	}
}
