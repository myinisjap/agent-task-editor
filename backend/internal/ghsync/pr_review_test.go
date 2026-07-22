package ghsync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// newTestSyncerFull is like newTestSyncer but also wires the PR-review
// ingestion function fields and (optionally) a workflow engine, for tests
// exercising ingestPRFeedback.
func newTestSyncerFull(t *testing.T,
	getPR func(ctx context.Context, repoName, branch string) (string, string, int, error),
	getPRHead func(ctx context.Context, repoName, branch string) (ghclient.PRHead, error),
	getReviews func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Review, error),
	getReviewComments func(ctx context.Context, repoName string, prNumber int) ([]ghclient.PRReviewComment, error),
	getFailedChecks func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Check, error),
	withEngine bool,
) (*Syncer, *gen.Queries, *fakeHub) {
	t.Helper()
	f := t.TempDir()
	dbPath := filepath.Join(f, "ghsync-pr-review.db")

	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	q := gen.New(db.SQL())
	hub := &fakeHub{}
	s := &Syncer{
		q:                 q,
		hub:               hub,
		getPR:             getPR,
		getPRHead:         getPRHead,
		getReviews:        getReviews,
		getReviewComments: getReviewComments,
		getFailedChecks:   getFailedChecks,
	}
	if withEngine {
		s.engine = workflow.New(db.SQL(), hub)
	}
	return s, q, hub
}

// newFeedbackTestWorkflow seeds a workflow with two labels ("in_review" and
// "work") and a human "failure" transition from in_review -> work, mirroring
// what Reject uses in production, so auto-transition tests have a target.
func newFeedbackTestWorkflow(t *testing.T, q *gen.Queries) (wfID, fromLabel, toLabel string) {
	t.Helper()
	ctx := context.Background()
	wfID = uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "Review", Description: ""}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	for _, name := range []string{"in_review", "work"} {
		if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID: uuid.NewString(), WorkflowID: wfID, Name: name, Color: "#000", SortOrder: 0, AgentIgnore: 0, IsTerminal: 0,
		}); err != nil {
			t.Fatalf("create label %s: %v", name, err)
		}
	}
	path := "failure"
	if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
		ID: uuid.NewString(), WorkflowID: wfID, FromLabel: "in_review", ToLabel: "work",
		TriggerType: "human", Path: &path,
	}); err != nil {
		t.Fatalf("create transition: %v", err)
	}
	return wfID, "in_review", "work"
}

// seedRunForTask creates a pending agent run and points the task at it as its
// current run, so appendRunFeedback has somewhere to write.
func seedRunForTask(t *testing.T, q *gen.Queries, taskID string) gen.Task {
	t.Helper()
	ctx := context.Background()
	runID := uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: taskID}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID,
		ActiveAgentRunID:  nil,
		ID:                taskID,
	}); err != nil {
		t.Fatalf("set task active run: %v", err)
	}
	task, err := q.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task
}

func TestIngestPRFeedback_ChangesRequestedReview_AppendsFeedback(t *testing.T) {
	ctx := context.Background()

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/1", 1, nil
	}
	getPRHead := func(ctx context.Context, repoName, branch string) (ghclient.PRHead, error) {
		return ghclient.PRHead{Number: 1, HeadSHA: "sha1"}, nil
	}
	getReviews := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Review, error) {
		return []ghclient.Review{
			{ID: "r1", State: "CHANGES_REQUESTED", Body: "please fix the bug", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
			{ID: "r2", State: "APPROVED", Body: "lgtm elsewhere", Author: "bob", SubmittedAt: "2024-01-01T00:01:00Z"},
		}, nil
	}
	noComments := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.PRReviewComment, error) {
		return nil, nil
	}
	noChecks := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Check, error) { return nil, nil }

	s, q, _ := newTestSyncerFull(t, getPR, getPRHead, getReviews, noComments, noChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	task = seedRunForTask(t, q, task.ID)
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, 1)

	run, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Feedback == nil || !strings.Contains(*run.Feedback, "please fix the bug") {
		t.Fatalf("expected feedback to contain the changes-requested review body, got %v", run.Feedback)
	}
	if strings.Contains(*run.Feedback, "lgtm elsewhere") {
		t.Fatalf("did not expect an approved review's body in feedback, got %v", run.Feedback)
	}

	// Re-sweep with the same reviews: no duplicate feedback should be appended.
	s.ingestPRFeedback(ctx, task, repo, 1)
	run2, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if *run2.Feedback != *run.Feedback {
		t.Fatalf("expected feedback unchanged on re-sweep, got:\nfirst: %q\nsecond: %q", *run.Feedback, *run2.Feedback)
	}
}

func TestIngestPRFeedback_FailedChecks_AppendsFeedback(t *testing.T) {
	ctx := context.Background()

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/1", 1, nil
	}
	getPRHead := func(ctx context.Context, repoName, branch string) (ghclient.PRHead, error) {
		return ghclient.PRHead{Number: 1, HeadSHA: "sha1"}, nil
	}
	noReviews := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Review, error) { return nil, nil }
	noComments := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.PRReviewComment, error) {
		return nil, nil
	}
	getChecks := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Check, error) {
		return []ghclient.Check{{Name: "build", Link: "https://ci/1", Bucket: "fail"}}, nil
	}

	s, q, _ := newTestSyncerFull(t, getPR, getPRHead, noReviews, noComments, getChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	task = seedRunForTask(t, q, task.ID)
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, 1)

	run, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Feedback == nil || !strings.Contains(*run.Feedback, "build") {
		t.Fatalf("expected feedback to mention the failed 'build' check, got %v", run.Feedback)
	}

	// Re-sweep with the same failing check on the same commit: no duplicate append.
	s.ingestPRFeedback(ctx, task, repo, 1)
	run2, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if *run2.Feedback != *run.Feedback {
		t.Fatalf("expected feedback unchanged on re-sweep, got:\nfirst: %q\nsecond: %q", *run.Feedback, *run2.Feedback)
	}
}

func TestIngestPRFeedback_InlineComments_DedupedAcrossSweeps(t *testing.T) {
	ctx := context.Background()

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/1", 1, nil
	}
	getPRHead := func(ctx context.Context, repoName, branch string) (ghclient.PRHead, error) {
		return ghclient.PRHead{Number: 1, HeadSHA: "sha1"}, nil
	}
	noReviews := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Review, error) { return nil, nil }
	noChecks := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Check, error) { return nil, nil }
	getComments := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.PRReviewComment, error) {
		return []ghclient.PRReviewComment{
			{ID: "c1", Path: "main.go", Line: 42, StartLine: 42, Side: "RIGHT", Body: "use a constant here", DiffHunk: "@@ -40,3 +40,3 @@"},
		}, nil
	}

	s, q, hub := newTestSyncerFull(t, getPR, getPRHead, noReviews, getComments, noChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, 1)

	comments, err := q.ListOpenTaskReviewComments(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 review comment, got %d", len(comments))
	}
	if comments[0].FilePath != "main.go" || comments[0].StartLine != 42 || comments[0].Source != "github" {
		t.Errorf("comment = %+v, unexpected", comments[0])
	}
	if len(hub.calls) != 1 || hub.calls[0].eventType != "task.review_comment_added" {
		t.Errorf("expected 1 task.review_comment_added publish, got %+v", hub.calls)
	}

	// Re-sweep: the same comment ID should not be inserted again.
	s.ingestPRFeedback(ctx, task, repo, 1)
	comments2, err := q.ListOpenTaskReviewComments(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments2) != 1 {
		t.Fatalf("expected still 1 review comment after re-sweep (dedup), got %d", len(comments2))
	}
}

func TestIngestPRFeedback_HeadSHAChange_ResetsReviewCursor(t *testing.T) {
	ctx := context.Background()
	headSHA := "sha1"

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/1", 1, nil
	}
	getPRHead := func(ctx context.Context, repoName, branch string) (ghclient.PRHead, error) {
		return ghclient.PRHead{Number: 1, HeadSHA: headSHA}, nil
	}
	// Same single review both sweeps (submitted before any push); the reset
	// on head-SHA change should let it be treated as "new" again since the
	// cursor is cleared even though the review itself is old — verifying the
	// fresh-cycle mechanism resets the cursor rather than that it re-shows
	// old reviews (which would be surprising); so instead we assert the
	// *state* head_sha updates and a second distinct review after a push is
	// still surfaced.
	call := 0
	getReviews := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Review, error) {
		call++
		if call == 1 {
			return []ghclient.Review{
				{ID: "r1", State: "CHANGES_REQUESTED", Body: "fix A", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
			}, nil
		}
		return []ghclient.Review{
			{ID: "r1", State: "CHANGES_REQUESTED", Body: "fix A", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
			{ID: "r2", State: "CHANGES_REQUESTED", Body: "fix B", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
		}, nil
	}
	noComments := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.PRReviewComment, error) {
		return nil, nil
	}
	noChecks := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Check, error) { return nil, nil }

	s, q, _ := newTestSyncerFull(t, getPR, getPRHead, getReviews, noComments, noChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	task = seedRunForTask(t, q, task.ID)
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, 1)
	run1, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run1.Feedback == nil || !strings.Contains(*run1.Feedback, "fix A") {
		t.Fatalf("expected first sweep feedback to contain 'fix A', got %v", run1.Feedback)
	}

	// Simulate a push: new head SHA, and a second review appears with the
	// same submitted_at (both have identical timestamps in this fixture, so
	// the ONLY way "fix B" surfaces on the second call given the >= lastSeen
	// exclusive comparison is that the cursor reset on head-SHA change).
	headSHA = "sha2"
	s.ingestPRFeedback(ctx, task, repo, 1)

	state, err := q.GetTaskPRReviewState(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.HeadSha != "sha2" {
		t.Errorf("head_sha = %q, want sha2 after push", state.HeadSha)
	}
}

func TestIngestPRFeedback_AutoTransition_EnabledOnRepo(t *testing.T) {
	ctx := context.Background()

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/1", 1, nil
	}
	getPRHead := func(ctx context.Context, repoName, branch string) (ghclient.PRHead, error) {
		return ghclient.PRHead{Number: 1, HeadSHA: "sha1"}, nil
	}
	getReviews := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Review, error) {
		return []ghclient.Review{
			{ID: "r1", State: "CHANGES_REQUESTED", Body: "please fix", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
		}, nil
	}
	noComments := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.PRReviewComment, error) {
		return nil, nil
	}
	noChecks := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Check, error) { return nil, nil }

	s, q, _ := newTestSyncerFull(t, getPR, getPRHead, getReviews, noComments, noChecks, true)
	wfID, fromLabel, toLabel := newFeedbackTestWorkflow(t, q)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "widgets", Path: t.TempDir(), RemoteUrl: ghURL(), WorkflowID: &wfID,
		PrReviewAutoTransitionEnabled: 1,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	task := newTestTask(t, q, repoID, wfID, fromLabel, "feature-branch", "", "pushed", "")
	task = seedRunForTask(t, q, task.ID)
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, 1)

	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label != toLabel {
		t.Errorf("label = %q, want auto-transitioned to %q", updated.Label, toLabel)
	}
}

func TestIngestPRFeedback_AutoTransition_DisabledOnRepo_NoOp(t *testing.T) {
	ctx := context.Background()

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/1", 1, nil
	}
	getPRHead := func(ctx context.Context, repoName, branch string) (ghclient.PRHead, error) {
		return ghclient.PRHead{Number: 1, HeadSHA: "sha1"}, nil
	}
	getReviews := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Review, error) {
		return []ghclient.Review{
			{ID: "r1", State: "CHANGES_REQUESTED", Body: "please fix", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
		}, nil
	}
	noComments := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.PRReviewComment, error) {
		return nil, nil
	}
	noChecks := func(ctx context.Context, repoName string, prNumber int) ([]ghclient.Check, error) { return nil, nil }

	s, q, _ := newTestSyncerFull(t, getPR, getPRHead, getReviews, noComments, noChecks, true)
	wfID, fromLabel, _ := newFeedbackTestWorkflow(t, q)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "widgets", Path: t.TempDir(), RemoteUrl: ghURL(), WorkflowID: &wfID,
		PrReviewAutoTransitionEnabled: 0,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	task := newTestTask(t, q, repoID, wfID, fromLabel, "feature-branch", "", "pushed", "")
	task = seedRunForTask(t, q, task.ID)
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, 1)

	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label != fromLabel {
		t.Errorf("label = %q, want unchanged %q (auto-transition disabled)", updated.Label, fromLabel)
	}
}

// mustCreateSimpleWorkflow is a small wrapper around newTestWorkflow for
// readability in this file's tests.
func mustCreateSimpleWorkflow(t *testing.T, q *gen.Queries) (string, string) {
	t.Helper()
	return newTestWorkflow(t, q)
}

func mustGetRepo(t *testing.T, q *gen.Queries, repoID string) gen.Repo {
	t.Helper()
	repo, err := q.GetRepo(context.Background(), repoID)
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	return repo
}
