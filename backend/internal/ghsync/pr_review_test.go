package ghsync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// newTestSyncerFull is like newTestSyncer but also wires the PR-review
// ingestion function fields and (optionally) a workflow engine, for tests
// exercising ingestPRFeedback.
func newTestSyncerFull(t *testing.T,
	getPRHead func(ctx context.Context, repo repoInfo, branch string) (forge.PRHead, error),
	getReviews func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error),
	getReviewComments func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error),
	getFailedChecks func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error),
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

	head := forge.PRHead{Number: 1, HeadSHA: "sha1", State: "pr_open", URL: "https://github.com/acme/widgets/pull/1"}
	getReviews := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) {
		return []forge.Review{
			{ID: "r1", State: "CHANGES_REQUESTED", Body: "please fix the bug", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
			{ID: "r2", State: "APPROVED", Body: "lgtm elsewhere", Author: "bob", SubmittedAt: "2024-01-01T00:01:00Z"},
		}, nil
	}
	noComments := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
		return nil, nil
	}
	noChecks := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) { return nil, nil }

	s, q, _ := newTestSyncerFull(t, nil, getReviews, noComments, noChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	task = seedRunForTask(t, q, task.ID)
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, head)

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
	s.ingestPRFeedback(ctx, task, repo, head)
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

	head := forge.PRHead{Number: 1, HeadSHA: "sha1", State: "pr_open", URL: "https://github.com/acme/widgets/pull/1"}
	noReviews := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) { return nil, nil }
	noComments := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
		return nil, nil
	}
	getChecks := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) {
		return []forge.Check{{Name: "build", Link: "https://ci/1", Bucket: "fail"}}, nil
	}

	s, q, _ := newTestSyncerFull(t, nil, noReviews, noComments, getChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	task = seedRunForTask(t, q, task.ID)
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, head)

	run, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Feedback == nil || !strings.Contains(*run.Feedback, "build") {
		t.Fatalf("expected feedback to mention the failed 'build' check, got %v", run.Feedback)
	}

	// Re-sweep with the same failing check on the same commit: no duplicate append.
	s.ingestPRFeedback(ctx, task, repo, head)
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

	head := forge.PRHead{Number: 1, HeadSHA: "sha1", State: "pr_open", URL: "https://github.com/acme/widgets/pull/1"}
	noReviews := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) { return nil, nil }
	noChecks := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) { return nil, nil }
	getComments := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
		return []forge.PRReviewComment{
			{ID: "c1", Path: "main.go", Line: 42, StartLine: 42, Side: "RIGHT", Body: "use a constant here", DiffHunk: "@@ -40,3 +40,3 @@"},
		}, nil
	}

	s, q, hub := newTestSyncerFull(t, nil, noReviews, getComments, noChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	s.ingestPRFeedback(ctx, task, repo, head)

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
	s.ingestPRFeedback(ctx, task, repo, head)
	comments2, err := q.ListOpenTaskReviewComments(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments2) != 1 {
		t.Fatalf("expected still 1 review comment after re-sweep (dedup), got %d", len(comments2))
	}
}

// TestIngestPRFeedback_HeadSHAChange_DoesNotReplayOldReviews replaces the
// former TestIngestPRFeedback_HeadSHAChange_ResetsReviewCursor, which existed
// specifically to assert the pre-#340 bug: freshCycle used to reset
// state.LastReviewSubmittedAt on every push, which made the
// `r.SubmittedAt <= lastSeen` filter in ingestReviews exclude nothing and
// re-inject every historical changes_requested review's body into the run's
// feedback on every push. This test now asserts the fixed behavior: an
// already-surfaced review is never replayed, a genuinely new review (later
// SubmittedAt) after the push is still surfaced, head_sha still advances, and
// the failed-check cursor (which SHOULD reset on push, unlike the review
// cursor) does.
func TestIngestPRFeedback_HeadSHAChange_DoesNotReplayOldReviews(t *testing.T) {
	ctx := context.Background()
	headSHA := "sha1"
	// updatedAt changes with each push too (a real PR's updated_at bumps on
	// every push), so the Part B "did the PR change" gate doesn't itself
	// suppress ingestReviews on the second sweep.
	updatedAt := "2024-01-01T00:00:00Z"

	call := 0
	getReviews := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) {
		call++
		if call == 1 {
			return []forge.Review{
				{ID: "r1", State: "CHANGES_REQUESTED", Body: "fix A", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
			}, nil
		}
		// Second sweep (after the simulated push): the same old review r1
		// (SubmittedAt unchanged — it was never resubmitted) plus a genuinely
		// new review r2 with a later SubmittedAt.
		return []forge.Review{
			{ID: "r1", State: "CHANGES_REQUESTED", Body: "fix A", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
			{ID: "r2", State: "CHANGES_REQUESTED", Body: "fix B", Author: "alice", SubmittedAt: "2024-01-02T00:00:00Z"},
		}, nil
	}
	noComments := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
		return nil, nil
	}
	checksCall := 0
	getChecks := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) {
		checksCall++
		return nil, nil
	}

	s, q, _ := newTestSyncerFull(t, nil, getReviews, noComments, getChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	task = seedRunForTask(t, q, task.ID)
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	head := func() forge.PRHead {
		return forge.PRHead{Number: 1, HeadSHA: headSHA, State: "pr_open", URL: "https://github.com/acme/widgets/pull/1", UpdatedAt: updatedAt}
	}

	s.ingestPRFeedback(ctx, task, repo, head())
	run1, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run1.Feedback == nil || !strings.Contains(*run1.Feedback, "fix A") {
		t.Fatalf("expected first sweep feedback to contain 'fix A', got %v", run1.Feedback)
	}
	if got := strings.Count(*run1.Feedback, "fix A"); got != 1 {
		t.Fatalf("'fix A' appears %d times after first sweep, want 1", got)
	}

	// Simulate a push: new head SHA and a bumped PR updated_at, with a new
	// review (r2, later SubmittedAt) alongside the still-unchanged old one
	// (r1, same SubmittedAt as before).
	headSHA = "sha2"
	updatedAt = "2024-01-02T00:00:00Z"
	s.ingestPRFeedback(ctx, task, repo, head())

	run2, err := q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if run2.Feedback == nil {
		t.Fatal("expected feedback after second sweep, got nil")
	}
	if got := strings.Count(*run2.Feedback, "fix A"); got != 1 {
		t.Errorf("'fix A' appears %d times in feedback after push, want exactly 1 (must not be replayed)", got)
	}
	if !strings.Contains(*run2.Feedback, "fix B") {
		t.Errorf("expected feedback to contain the genuinely new review 'fix B', got %q", *run2.Feedback)
	}

	state, err := q.GetTaskPRReviewState(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.HeadSha != "sha2" {
		t.Errorf("head_sha = %q, want sha2 after push", state.HeadSha)
	}
	if checksCall != 2 {
		t.Fatalf("expected getFailedChecks to be called on both sweeps (gated on prChanged, which changed both times), got %d calls", checksCall)
	}
}

func TestIngestPRFeedback_AutoTransition_EnabledOnRepo(t *testing.T) {
	ctx := context.Background()

	head := forge.PRHead{Number: 1, HeadSHA: "sha1", State: "pr_open", URL: "https://github.com/acme/widgets/pull/1"}
	getReviews := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) {
		return []forge.Review{
			{ID: "r1", State: "CHANGES_REQUESTED", Body: "please fix", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
		}, nil
	}
	noComments := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
		return nil, nil
	}
	noChecks := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) { return nil, nil }

	s, q, _ := newTestSyncerFull(t, nil, getReviews, noComments, noChecks, true)
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

	s.ingestPRFeedback(ctx, task, repo, head)

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

	head := forge.PRHead{Number: 1, HeadSHA: "sha1", State: "pr_open", URL: "https://github.com/acme/widgets/pull/1"}
	getReviews := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) {
		return []forge.Review{
			{ID: "r1", State: "CHANGES_REQUESTED", Body: "please fix", Author: "alice", SubmittedAt: "2024-01-01T00:00:00Z"},
		}, nil
	}
	noComments := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
		return nil, nil
	}
	noChecks := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) { return nil, nil }

	s, q, _ := newTestSyncerFull(t, nil, getReviews, noComments, noChecks, true)
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

	s.ingestPRFeedback(ctx, task, repo, head)

	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label != fromLabel {
		t.Errorf("label = %q, want unchanged %q (auto-transition disabled)", updated.Label, fromLabel)
	}
}

// TestIngestPRFeedback_UnchangedPR_SkipsReviewsCommentsFetch asserts the core
// #340 fix: when the PR's UpdatedAt hasn't moved since the last sweep (and
// the check-poll floor hasn't elapsed either), ingestPRFeedback does not call
// getReviews/getReviewComments/getFailedChecks at all — only getPRHead (made
// by the caller, syncTask, not exercised here) is paid for on a steady-state
// sweep of an unchanged PR.
func TestIngestPRFeedback_UnchangedPR_SkipsReviewsCommentsFetch(t *testing.T) {
	ctx := context.Background()

	reviewCalls, commentCalls, checkCalls := 0, 0, 0
	getReviews := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) {
		reviewCalls++
		return nil, nil
	}
	getComments := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
		commentCalls++
		return nil, nil
	}
	getChecks := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) {
		checkCalls++
		return nil, nil
	}

	s, q, _ := newTestSyncerFull(t, nil, getReviews, getComments, getChecks, false)
	wfID, label := mustCreateSimpleWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pushed", "")
	repo := repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}

	head := forge.PRHead{Number: 1, HeadSHA: "sha1", State: "pr_open", URL: "https://github.com/acme/widgets/pull/1", UpdatedAt: "2024-01-01T00:00:00Z"}

	// First sweep: nothing has been ingested yet, so the gate fetches.
	s.ingestPRFeedback(ctx, task, repo, head)
	if reviewCalls != 1 || commentCalls != 1 || checkCalls != 1 {
		t.Fatalf("expected 1 call each on first sweep, got reviews=%d comments=%d checks=%d", reviewCalls, commentCalls, checkCalls)
	}

	// Second sweep: same head, same UpdatedAt (checkPollFloor hasn't
	// elapsed) — nothing should be fetched again.
	s.ingestPRFeedback(ctx, task, repo, head)
	if reviewCalls != 1 || commentCalls != 1 || checkCalls != 1 {
		t.Fatalf("expected still 1 call each on second (unchanged) sweep, got reviews=%d comments=%d checks=%d", reviewCalls, commentCalls, checkCalls)
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
