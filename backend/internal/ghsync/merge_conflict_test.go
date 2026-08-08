package ghsync

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// conflictFixture wires a syncer whose only live signal is the PR head (with a
// caller-controlled mergeability), so these tests exercise the merge-conflict
// path in isolation from reviews/comments/checks.
type conflictFixture struct {
	s    *Syncer
	q    *gen.Queries
	hub  *fakeHub
	repo repoInfo
	task gen.Task
	// head is what getPRHead returns; tests mutate it between sweeps to
	// simulate a push, a retarget, or GitHub changing its verdict.
	head *forge.PRHead
}

func newConflictFixture(t *testing.T, autoTransition bool) *conflictFixture {
	t.Helper()
	ctx := context.Background()
	head := &forge.PRHead{Number: 1, HeadSHA: "sha1", BaseRef: "main", Mergeable: forge.MergeableClean, State: "pr_open", URL: "https://github.com/acme/widgets/pull/1"}

	getPRHead := func(ctx context.Context, repo repoInfo, branch string) (forge.PRHead, error) {
		return *head, nil
	}
	noReviews := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Review, error) { return nil, nil }
	noComments := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.PRReviewComment, error) {
		return nil, nil
	}
	noChecks := func(ctx context.Context, repo repoInfo, prNumber int) ([]forge.Check, error) { return nil, nil }

	s, q, hub := newTestSyncerFull(t, getPRHead, noReviews, noComments, noChecks, autoTransition)

	var wfID, label string
	var repoID string
	if autoTransition {
		wfID, label, _ = newFeedbackTestWorkflow(t, q)
		repoID = uuid.NewString()
		if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
			ID: repoID, Name: "widgets", Path: t.TempDir(), RemoteUrl: ghURL(), WorkflowID: &wfID,
			PrReviewAutoTransitionEnabled: 1,
		}); err != nil {
			t.Fatalf("create repo: %v", err)
		}
	} else {
		wfID, label = mustCreateSimpleWorkflow(t, q)
		repoID = newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	}

	task := newTestTask(t, q, repoID, wfID, label, "feature-branch", "", "pr_open", "https://github.com/acme/widgets/pull/1")
	task = seedRunForTask(t, q, task.ID)

	return &conflictFixture{
		s:    s,
		q:    q,
		hub:  hub,
		repo: repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)},
		task: task,
		head: head,
	}
}

// sweep runs one ingestion pass with a freshly-read task row, the way the real
// sweep does (it re-lists tasks every interval). prState overrides f.head's
// State for this call (ingestPRFeedback now reads PR state off the passed
// forge.PRHead rather than a separate parameter — see #340), letting tests
// exercise merge-conflict handling against a PR state other than "pr_open"
// without otherwise touching the fixture's head.
func (f *conflictFixture) sweep(t *testing.T, prState string) {
	t.Helper()
	ctx := context.Background()
	task, err := f.q.GetTask(ctx, f.task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	f.task = task
	head := *f.head
	head.State = prState
	f.s.ingestPRFeedback(ctx, task, f.repo, head)
}

// feedback returns the current agent run's accumulated feedback text.
func (f *conflictFixture) feedback(t *testing.T) string {
	t.Helper()
	run, err := f.q.GetAgentRun(context.Background(), *f.task.CurrentAgentRunID)
	if err != nil {
		t.Fatalf("get agent run: %v", err)
	}
	if run.Feedback == nil {
		return ""
	}
	return *run.Feedback
}

func (f *conflictFixture) storedMergeable(t *testing.T) string {
	t.Helper()
	task, err := f.q.GetTask(context.Background(), f.task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task.PrMergeable
}

func countEvents(hub *fakeHub, eventType string) int {
	n := 0
	for _, c := range hub.calls {
		if c.eventType == eventType {
			n++
		}
	}
	return n
}

func TestIngestMergeConflict_SurfacesOncePerHeadCommit(t *testing.T) {
	f := newConflictFixture(t, false)

	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")

	fb := f.feedback(t)
	if !strings.Contains(fb, "merge conflict") {
		t.Fatalf("expected merge-conflict feedback, got %q", fb)
	}
	if !strings.Contains(fb, "main") {
		t.Errorf("expected feedback to name the base branch, got %q", fb)
	}
	if got := f.storedMergeable(t); got != "conflicting" {
		t.Errorf("pr_mergeable = %q, want conflicting", got)
	}
	if n := countEvents(f.hub, "task.pr_mergeable_changed"); n != 1 {
		t.Errorf("published %d pr_mergeable_changed events, want 1", n)
	}

	// Still conflicting at the same head commit: already said so.
	f.sweep(t, "pr_open")
	if got := f.feedback(t); got != fb {
		t.Errorf("feedback changed on re-sweep:\nfirst:  %q\nsecond: %q", fb, got)
	}
	if n := countEvents(f.hub, "task.pr_mergeable_changed"); n != 1 {
		t.Errorf("published %d pr_mergeable_changed events after re-sweep, want 1", n)
	}
}

func TestIngestMergeConflict_ResurfacesAfterPush(t *testing.T) {
	f := newConflictFixture(t, false)

	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")
	first := f.feedback(t)

	// The agent pushed, but the branch still conflicts: say so again, since
	// the last attempt at resolving it evidently did not work.
	f.head.HeadSHA = "sha2"
	f.sweep(t, "pr_open")

	second := f.feedback(t)
	if second == first {
		t.Fatalf("expected fresh conflict feedback after a push, got unchanged %q", second)
	}
	if got := strings.Count(second, "merge conflict"); got != 2 {
		t.Errorf("merge-conflict feedback appears %d times, want 2", got)
	}
}

func TestIngestMergeConflict_MergeableClearsCursor(t *testing.T) {
	f := newConflictFixture(t, false)

	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")

	// Resolved (base branch reverted, say) without the task's head moving.
	f.head.Mergeable = forge.MergeableClean
	f.sweep(t, "pr_open")
	if got := f.storedMergeable(t); got != "mergeable" {
		t.Fatalf("pr_mergeable = %q, want mergeable", got)
	}

	// Base moves again and reintroduces the conflict at the same head commit:
	// worth telling the agent about a second time.
	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")
	if got := strings.Count(f.feedback(t), "merge conflict"); got != 2 {
		t.Errorf("merge-conflict feedback appears %d times, want 2", got)
	}
}

func TestIngestMergeConflict_UnknownIsRecordedButQuiet(t *testing.T) {
	f := newConflictFixture(t, false)

	// GitHub has not finished computing the test merge yet.
	f.head.Mergeable = forge.MergeableUnknown
	f.sweep(t, "pr_open")

	if got := f.storedMergeable(t); got != "unknown" {
		t.Errorf("pr_mergeable = %q, want unknown", got)
	}
	if got := f.feedback(t); got != "" {
		t.Errorf("expected no feedback for an unknown verdict, got %q", got)
	}

	// A conflict verdict that arrives later, then flaps back to unknown, must
	// not re-surface once the real verdict returns unchanged.
	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")
	f.head.Mergeable = forge.MergeableUnknown
	f.sweep(t, "pr_open")
	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")

	if got := strings.Count(f.feedback(t), "merge conflict"); got != 1 {
		t.Errorf("merge-conflict feedback appears %d times, want 1", got)
	}
}

func TestIngestMergeConflict_IgnoredWhenPRNotOpen(t *testing.T) {
	f := newConflictFixture(t, false)

	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_merged")

	if got := f.feedback(t); got != "" {
		t.Errorf("expected no feedback for a merged PR, got %q", got)
	}
	if got := f.storedMergeable(t); got != "conflicting" {
		t.Errorf("pr_mergeable = %q, want the observed verdict to still be recorded", got)
	}
}

func TestIngestMergeConflict_AutoTransitionsWhenRepoOptedIn(t *testing.T) {
	f := newConflictFixture(t, true)
	before := f.task.Label

	f.head.Mergeable = forge.MergeableConflicting
	f.sweep(t, "pr_open")

	updated, err := f.q.GetTask(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label == before {
		t.Errorf("label = %q, want auto-transitioned away from %q on a merge conflict", updated.Label, before)
	}
}
