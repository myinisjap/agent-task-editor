package writeback

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestParseSourceRef(t *testing.T) {
	cases := []struct {
		name       string
		ref        string
		wantName   string
		wantNumber int
		wantOK     bool
	}{
		{"valid", "acme/widgets#123", "acme/widgets", 123, true},
		{"missing hash", "acme/widgets", "", 0, false},
		{"missing slash", "widgets#123", "", 0, false},
		{"non numeric suffix", "acme/widgets#abc", "", 0, false},
		{"empty", "", "", 0, false},
		{"trailing hash no number", "acme/widgets#", "", 0, false},
		{"zero issue number", "acme/widgets#0", "", 0, false},
		{"negative issue number", "acme/widgets#-1", "", 0, false},
		{"nested org path", "acme/sub/widgets#5", "acme/sub/widgets", 5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, num, ok := ParseSourceRef(tc.ref)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if num != tc.wantNumber {
				t.Errorf("number = %d, want %d", num, tc.wantNumber)
			}
		})
	}
}

func openTestDB(t *testing.T) *gen.Queries {
	t.Helper()
	f, err := os.CreateTemp("", "writeback-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return gen.New(db.SQL())
}

// seedRepo creates a workflow + repo, with issue write-back optionally enabled.
func seedRepo(t *testing.T, q *gen.Queries, writebackEnabled bool) gen.Repo {
	t.Helper()
	ctx := context.Background()

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID:   uuid.NewString(),
		Name: "wf-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	wbEnabled := int64(0)
	if writebackEnabled {
		wbEnabled = 1
	}
	remote := "https://github.com/acme/widgets"
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:                    uuid.NewString(),
		Name:                  "acme/widgets",
		Path:                  t.TempDir(),
		RemoteUrl:             &remote,
		WorkflowID:            &wf.ID,
		IssueSyncEnabled:      0,
		IssueSyncLabel:        "",
		IssueWritebackEnabled: wbEnabled,
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return repo
}

// seedSourcedTask creates a task imported from GitHub (source/source_ref set).
func seedSourcedTask(t *testing.T, q *gen.Queries, repo gen.Repo, sourceRef string) gen.Task {
	t.Helper()
	ctx := context.Background()
	task, err := q.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
		ID:          uuid.NewString(),
		Title:       "Fix crash",
		Description: "",
		Type:        "bug",
		Label:       "not_ready",
		RepoID:      repo.ID,
		WorkflowID:  *repo.WorkflowID,
		Attachments: "[]",
		Source:      "github",
		SourceRef:   sourceRef,
	})
	if err != nil {
		t.Fatalf("create sourced task: %v", err)
	}
	return task
}

// seedManualTask creates a task with no source (as if manually created).
func seedManualTask(t *testing.T, q *gen.Queries, repo gen.Repo) gen.Task {
	t.Helper()
	ctx := context.Background()
	task, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID:          uuid.NewString(),
		Title:       "Manual task",
		Type:        "feature",
		Label:       "not_ready",
		RepoID:      repo.ID,
		WorkflowID:  *repo.WorkflowID,
		Attachments: "[]",
	})
	if err != nil {
		t.Fatalf("create manual task: %v", err)
	}
	return task
}

// fakeGH records calls made to the three gh-wrapping funcs and lets tests
// script a canned error for any of them.
type fakeGH struct {
	labelCalls   []string
	commentCalls []string
	closeCalls   []string

	labelErr, commentErr, closeErr error
}

func newWritebackWithFake(q *gen.Queries) (*Writeback, *fakeGH) {
	fg := &fakeGH{}
	wb := &Writeback{
		q: q,
		addLabel: func(ctx context.Context, repoName string, issueNumber int, label string) error {
			fg.labelCalls = append(fg.labelCalls, label)
			return fg.labelErr
		},
		commentOnIssue: func(ctx context.Context, repoName string, issueNumber int, body string) error {
			fg.commentCalls = append(fg.commentCalls, body)
			return fg.commentErr
		},
		closeWithComment: func(ctx context.Context, repoName string, issueNumber int, body string) error {
			fg.closeCalls = append(fg.closeCalls, body)
			return fg.closeErr
		},
	}
	return wb, fg
}

func TestOnLeaveNotReady_Disabled(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, false) // writeback disabled
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	wb, fg := newWritebackWithFake(q)

	wb.OnLeaveNotReady(context.Background(), task, repo)

	if len(fg.labelCalls) != 0 {
		t.Fatalf("expected no gh calls, got %v", fg.labelCalls)
	}
}

func TestOnLeaveNotReady_NoSource(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedManualTask(t, q, repo)
	wb, fg := newWritebackWithFake(q)

	wb.OnLeaveNotReady(context.Background(), task, repo)

	if len(fg.labelCalls) != 0 {
		t.Fatalf("expected no gh calls for a manually created task, got %v", fg.labelCalls)
	}
}

func TestOnLeaveNotReady_FirstCallAppliesLabelAndMarksDone(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	wb, fg := newWritebackWithFake(q)

	wb.OnLeaveNotReady(context.Background(), task, repo)

	if len(fg.labelCalls) != 1 || fg.labelCalls[0] != InProgressLabel {
		t.Fatalf("expected one add-label call with %q, got %v", InProgressLabel, fg.labelCalls)
	}
	updated, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackInProgressSent == 0 {
		t.Fatal("expected writeback_in_progress_sent to be set")
	}
}

func TestOnLeaveNotReady_Idempotent(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	wb, fg := newWritebackWithFake(q)

	wb.OnLeaveNotReady(context.Background(), task, repo)
	// Refetch to pick up the flag, then call again — must be a no-op.
	refreshed, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	wb.OnLeaveNotReady(context.Background(), refreshed, repo)

	if len(fg.labelCalls) != 1 {
		t.Fatalf("expected exactly 1 gh call across both invocations, got %d", len(fg.labelCalls))
	}
}

func TestOnLeaveNotReady_MarksDoneEvenOnGHFailure(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	wb, fg := newWritebackWithFake(q)
	fg.labelErr = errors.New("label not found")

	wb.OnLeaveNotReady(context.Background(), task, repo)

	updated, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackInProgressSent == 0 {
		t.Fatal("expected writeback_in_progress_sent to be set even though gh call failed (optional signal, no retry)")
	}
}

func TestOnPROpened_Disabled(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, false)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	task.PrUrl = "https://github.com/acme/widgets/pull/7"
	wb, fg := newWritebackWithFake(q)

	wb.OnPROpened(context.Background(), task, repo)

	if len(fg.commentCalls) != 0 {
		t.Fatalf("expected no gh calls, got %v", fg.commentCalls)
	}
}

func TestOnPROpened_NoPRURL(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	wb, fg := newWritebackWithFake(q)

	wb.OnPROpened(context.Background(), task, repo)

	if len(fg.commentCalls) != 0 {
		t.Fatalf("expected no gh calls when task has no PR URL yet, got %v", fg.commentCalls)
	}
}

func TestOnPROpened_FirstCallCommentsAndMarksDone(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	task.PrUrl = "https://github.com/acme/widgets/pull/7"
	wb, fg := newWritebackWithFake(q)

	wb.OnPROpened(context.Background(), task, repo)

	if len(fg.commentCalls) != 1 {
		t.Fatalf("expected one comment call, got %v", fg.commentCalls)
	}
	if want := "https://github.com/acme/widgets/pull/7"; !contains(fg.commentCalls[0], want) {
		t.Errorf("comment body = %q, want it to contain %q", fg.commentCalls[0], want)
	}
	updated, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackPrCommented == 0 {
		t.Fatal("expected writeback_pr_commented to be set")
	}
}

func TestOnPROpened_Idempotent(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	task.PrUrl = "https://github.com/acme/widgets/pull/7"
	wb, fg := newWritebackWithFake(q)

	wb.OnPROpened(context.Background(), task, repo)
	refreshed, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	wb.OnPROpened(context.Background(), refreshed, repo)

	if len(fg.commentCalls) != 1 {
		t.Fatalf("expected exactly 1 gh call across both invocations, got %d", len(fg.commentCalls))
	}
}

func TestOnPROpened_LeavesFlagUnsetOnGHFailure(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	task.PrUrl = "https://github.com/acme/widgets/pull/7"
	wb, fg := newWritebackWithFake(q)
	fg.commentErr = errors.New("rate limited")

	wb.OnPROpened(context.Background(), task, repo)

	updated, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackPrCommented != 0 {
		t.Fatal("expected writeback_pr_commented to remain unset so a later call retries")
	}

	// A later call (e.g. next sweep) should retry since the flag is unset.
	// GetTask doesn't reflect the in-memory-only task.PrUrl set above (it was
	// never persisted), so carry it forward explicitly to simulate the next
	// sweep having refetched a task whose PrUrl is now set for real.
	updated.PrUrl = task.PrUrl
	fg.commentErr = nil
	wb.OnPROpened(context.Background(), updated, repo)
	if len(fg.commentCalls) != 2 {
		t.Fatalf("expected retry on second call, got %d calls", len(fg.commentCalls))
	}
}

func TestOnPRMerged_NotMergedYet(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	task.GitState = "pr_open"
	wb, fg := newWritebackWithFake(q)

	wb.OnPRMerged(context.Background(), task, repo)

	if len(fg.closeCalls) != 0 {
		t.Fatalf("expected no gh calls, got %v", fg.closeCalls)
	}
}

func TestOnPRMerged_FirstCallClosesAndMarksDone(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	task.GitState = "pr_merged"
	task.PrUrl = "https://github.com/acme/widgets/pull/7"
	wb, fg := newWritebackWithFake(q)

	wb.OnPRMerged(context.Background(), task, repo)

	if len(fg.closeCalls) != 1 {
		t.Fatalf("expected one close call, got %v", fg.closeCalls)
	}
	updated, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackClosed == 0 {
		t.Fatal("expected writeback_closed to be set")
	}
}

func TestOnPRMerged_Idempotent(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedSourcedTask(t, q, repo, "acme/widgets#1")
	task.GitState = "pr_merged"
	task.PrUrl = "https://github.com/acme/widgets/pull/7"
	wb, fg := newWritebackWithFake(q)

	wb.OnPRMerged(context.Background(), task, repo)
	refreshed, err := q.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	wb.OnPRMerged(context.Background(), refreshed, repo)

	if len(fg.closeCalls) != 1 {
		t.Fatalf("expected exactly 1 gh call across both invocations, got %d", len(fg.closeCalls))
	}
}

func TestOnPRMerged_NoSourceRef(t *testing.T) {
	q := openTestDB(t)
	repo := seedRepo(t, q, true)
	task := seedManualTask(t, q, repo)
	task.GitState = "pr_merged"
	wb, fg := newWritebackWithFake(q)

	wb.OnPRMerged(context.Background(), task, repo)

	if len(fg.closeCalls) != 0 {
		t.Fatalf("expected no gh calls for a task with no source_ref, got %v", fg.closeCalls)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 ||
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}())
}
