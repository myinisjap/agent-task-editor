package automerge_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/automerge"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func green() []forge.Check { return []forge.Check{{Name: "ci", Bucket: "pass"}} }

func baseInput() automerge.Input {
	return automerge.Input{
		Task:    gen.Task{Branch: "ate/x", GitState: "pr_open"},
		Repo:    gen.Repo{AutoMergeMode: "when_green", AutoMergeMethod: "squash"},
		PR:      forge.PRHead{Number: 3, HeadSHA: "abc", State: "pr_open", Mergeable: forge.MergeableClean},
		Checks:  green(),
		Reviews: []forge.Review{{Author: "bob", State: "APPROVED", SubmittedAt: "2026-01-01T00:00:00Z"}},
	}
}

func TestEvaluate_Table(t *testing.T) {
	cases := []struct {
		name       string
		mut        func(*automerge.Input)
		applies    bool
		eligible   bool
		wantReason string
	}{
		{"all green", func(*automerge.Input) {}, true, true, ""},
		{"mode off", func(in *automerge.Input) { in.Repo.AutoMergeMode = "off" }, false, false, ""},
		{"mode empty reads as off", func(in *automerge.Input) { in.Repo.AutoMergeMode = "" }, false, false, ""},
		{"archived", func(in *automerge.Input) { in.Task.Archived = 1 }, false, false, ""},
		{"no open PR", func(in *automerge.Input) { in.PR.State = "pushed" }, false, false, ""},
		{"pending check", func(in *automerge.Input) { in.Checks = append(in.Checks, forge.Check{Name: "slow", Bucket: "pending"}) }, true, false, automerge.ReasonChecksPending},
		{"failed check", func(in *automerge.Input) { in.Checks = append(in.Checks, forge.Check{Name: "t", Bucket: "fail"}) }, true, false, automerge.ReasonChecksFailed},
		{"cancelled check", func(in *automerge.Input) { in.Checks = append(in.Checks, forge.Check{Name: "t", Bucket: "cancel"}) }, true, false, automerge.ReasonChecksFailed},
		{"skipped check ok", func(in *automerge.Input) { in.Checks = append(in.Checks, forge.Check{Name: "s", Bucket: "skipping"}) }, true, true, ""},
		{"unknown bucket is not green", func(in *automerge.Input) { in.Checks = []forge.Check{{Name: "?", Bucket: "weird"}} }, true, false, automerge.ReasonChecksPending},
		{"no checks yet", func(in *automerge.Input) { in.Checks = nil }, true, false, automerge.ReasonNoChecks},
		{"no checks settled", func(in *automerge.Input) { in.Checks = nil; in.NoChecksSettled = true }, true, true, ""},
		{"conflict", func(in *automerge.Input) { in.PR.Mergeable = forge.MergeableConflicting }, true, false, automerge.ReasonConflict},
		{"mergeability unknown", func(in *automerge.Input) { in.PR.Mergeable = forge.MergeableUnknown }, true, false, automerge.ReasonMergeableUnknown},
		{"draft", func(in *automerge.Input) { in.PR.Draft = true }, true, false, automerge.ReasonDraft},
		{"changes requested", func(in *automerge.Input) {
			in.Reviews = []forge.Review{{Author: "bob", State: "CHANGES_REQUESTED", SubmittedAt: "2026-01-02T00:00:00Z"}}
		}, true, false, automerge.ReasonChangesRequested},
		{"changes requested then approved", func(in *automerge.Input) {
			in.Reviews = []forge.Review{
				{Author: "bob", State: "APPROVED", SubmittedAt: "2026-01-03T00:00:00Z"},
				{Author: "bob", State: "CHANGES_REQUESTED", SubmittedAt: "2026-01-02T00:00:00Z"},
			}
		}, true, true, ""},
		{"changes requested then comment only", func(in *automerge.Input) {
			in.Reviews = []forge.Review{
				{Author: "bob", State: "CHANGES_REQUESTED", SubmittedAt: "2026-01-02T00:00:00Z"},
				{Author: "bob", State: "COMMENTED", SubmittedAt: "2026-01-03T00:00:00Z"},
			}
		}, true, false, automerge.ReasonChangesRequested},
		{"always_ask", func(in *automerge.Input) { in.Task.AlwaysAsk = 1 }, true, false, automerge.ReasonAlwaysAsk},
		{"paused", func(in *automerge.Input) { in.Task.Paused = 1 }, true, false, automerge.ReasonPaused},
		{"live run", func(in *automerge.Input) { in.HasLiveRun = true }, true, false, automerge.ReasonActiveRun},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput()
			tc.mut(&in)
			d := automerge.Evaluate(in)
			if d.Applies != tc.applies || d.Eligible != tc.eligible || d.Reason != tc.wantReason {
				t.Fatalf("got %+v, want applies=%v eligible=%v reason=%q", d, tc.applies, tc.eligible, tc.wantReason)
			}
			if tc.applies && !tc.eligible && d.Message == "" {
				t.Error("blocked decision needs a message")
			}
		})
	}
}

func TestMethodDefaults(t *testing.T) {
	if automerge.Method(gen.Repo{}) != forge.MergeSquash {
		t.Error("empty method should default to squash")
	}
	if automerge.Method(gen.Repo{AutoMergeMethod: "rebase"}) != forge.MergeRebase {
		t.Error("rebase not honoured")
	}
}

// --- DB-backed ---

type fakeForge struct {
	forge.Forge // nil: any unexpected call panics
	head        forge.PRHead
	checks      []forge.Check
	reviews     []forge.Review
	mergeErr    error
	merges      []string
	mergeSHA    string
}

func (f *fakeForge) PRHead(context.Context, string, string) (forge.PRHead, error) { return f.head, nil }
func (f *fakeForge) PRChecks(context.Context, string, int) ([]forge.Check, error) {
	return f.checks, nil
}
func (f *fakeForge) PRReviews(context.Context, string, int) ([]forge.Review, error) {
	return f.reviews, nil
}
func (f *fakeForge) MergePullRequest(_ context.Context, _ string, _ int, m forge.MergeMethod, sha string) error {
	f.merges = append(f.merges, string(m))
	f.mergeSHA = sha
	return f.mergeErr
}

func setup(t *testing.T) (*storage.DB, *gen.Queries, gen.Task, gen.Repo) {
	t.Helper()
	f, err := os.CreateTemp("", "am-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := storage.SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(ctx)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfs[0].ID, AutoMergeMode: "when_green", AutoMergeMethod: "merge"}); err != nil {
		t.Fatal(err)
	}
	task, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: wfs[0].ID, RepoID: repoID, Label: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO task_git (task_id, branch) VALUES (?, 'ate/x')`, task.ID); err != nil {
		t.Fatal(err)
	}
	task, _ = q.GetTask(ctx, task.ID)
	repo, _ := q.GetRepo(ctx, repoID)
	return db, q, task, repo
}

func openHead() forge.PRHead {
	return forge.PRHead{Number: 9, HeadSHA: "sha9", State: "pr_open", Mergeable: forge.MergeableClean}
}

func outbox(t *testing.T, db *storage.DB, taskID string) (n int, status, lastErr string) {
	t.Helper()
	rows, err := db.SQL().Query(`SELECT status, COALESCE(last_error,'') FROM task_side_effects WHERE task_id = ? AND kind = 'auto_merge'`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		n++
		if err := rows.Scan(&status, &lastErr); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestReconcile_RecordsStateAndEnqueuesOnce(t *testing.T) {
	db, q, task, repo := setup(t)
	ctx := context.Background()
	engine := workflow.New(db.SQL(), nil)
	ff := &fakeForge{checks: []forge.Check{{Name: "ci", Bucket: "pending"}}}
	src := automerge.ForgeSource{Forge: ff, RepoName: "o/r"}

	d, err := automerge.Reconcile(ctx, q, engine, nil, src, task, repo, openHead())
	if err != nil || d.Eligible || d.Reason != automerge.ReasonChecksPending {
		t.Fatalf("d=%+v err=%v", d, err)
	}
	row, err := q.GetTaskAutoMerge(ctx, task.ID)
	if err != nil || row.State != automerge.StateWaiting || row.Reason != automerge.ReasonChecksPending {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	if n, _, _ := outbox(t, db, task.ID); n != 0 {
		t.Fatal("must not enqueue while checks pending")
	}

	ff.checks = green()
	for i := 0; i < 2; i++ { // idempotent
		if d, err = automerge.Reconcile(ctx, q, engine, nil, src, task, repo, openHead()); err != nil || !d.Eligible {
			t.Fatalf("d=%+v err=%v", d, err)
		}
	}
	if n, st, _ := outbox(t, db, task.ID); n != 1 || st != "pending" {
		t.Fatalf("want exactly one pending auto_merge row, got n=%d st=%s", n, st)
	}
	if row, _ = q.GetTaskAutoMerge(ctx, task.ID); row.State != automerge.StateMerging {
		t.Fatalf("row=%+v", row)
	}

	// Turning the policy off clears the verdict.
	repo.AutoMergeMode = "off"
	if d, _ = automerge.Reconcile(ctx, q, engine, nil, src, task, repo, openHead()); d.Applies {
		t.Fatal("off must not apply")
	}
	if _, err := q.GetTaskAutoMerge(ctx, task.ID); err == nil {
		t.Fatal("verdict should be cleared when off")
	}
}

func TestReconcile_BlockedByLiveRun(t *testing.T) {
	db, q, task, repo := setup(t)
	ctx := context.Background()
	runID := uuid.NewString()
	if _, err := db.SQL().Exec(`INSERT INTO agent_runs (id, task_id, status) VALUES (?, ?, 'running')`, runID, task.ID); err != nil {
		t.Skipf("cannot seed run: %v", err)
	}
	if _, err := db.SQL().Exec(`UPDATE tasks SET current_agent_run_id = ? WHERE id = ?`, runID, task.ID); err != nil {
		t.Fatal(err)
	}
	task, _ = q.GetTask(ctx, task.ID)
	ff := &fakeForge{checks: green()}
	d, err := automerge.Reconcile(ctx, q, nil, nil, automerge.ForgeSource{Forge: ff, RepoName: "o/r"}, task, repo, openHead())
	if err != nil || d.Reason != automerge.ReasonActiveRun {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

func TestMerge_SuccessRecordsHistoryAndPinsSHA(t *testing.T) {
	db, q, task, repo := setup(t)
	ctx := context.Background()
	ff := &fakeForge{head: openHead(), checks: green()}
	if err := automerge.Merge(ctx, q, nil, ff, "o/r", task, repo); err != nil {
		t.Fatal(err)
	}
	if len(ff.merges) != 1 || ff.merges[0] != "merge" || ff.mergeSHA != "sha9" {
		t.Fatalf("merges=%v sha=%q", ff.merges, ff.mergeSHA)
	}
	h, err := q.GetLatestTaskLabelHistory(ctx, task.ID)
	if err != nil || h.ActorID == nil || *h.ActorID != workflow.ActorAutoMerge || h.Note == nil || !strings.Contains(*h.Note, "#9") {
		t.Fatalf("history = %+v err=%v", h, err)
	}
	_ = db
}

func TestMerge_NotEligibleOrHeadMovedDoesNotFail(t *testing.T) {
	_, q, task, repo := setup(t)
	ctx := context.Background()
	// A check went red between enqueue and execution: no merge call at all.
	ff := &fakeForge{head: openHead(), checks: []forge.Check{{Name: "ci", Bucket: "fail"}}}
	if err := automerge.Merge(ctx, q, nil, ff, "o/r", task, repo); err != nil || len(ff.merges) != 0 {
		t.Fatalf("err=%v merges=%v", err, ff.merges)
	}
	// Head moved under us: the forge says so, we drop quietly.
	ff = &fakeForge{head: openHead(), checks: green(), mergeErr: forge.ErrHeadMoved}
	if err := automerge.Merge(ctx, q, nil, ff, "o/r", task, repo); err != nil {
		t.Fatalf("head moved must not be an error, got %v", err)
	}
	// PR already merged: nothing to do.
	ff = &fakeForge{head: forge.PRHead{State: "pr_merged", Number: 9}}
	if err := automerge.Merge(ctx, q, nil, ff, "o/r", task, repo); err != nil || len(ff.merges) != 0 {
		t.Fatalf("err=%v", err)
	}
}

// A forge refusal must park the outbox row as failed immediately (surfaced in
// Needs you with Retry/Dismiss), not retry silently with backoff.
func TestMerge_RefusalSurfacesAsFailedSideEffect(t *testing.T) {
	db, q, task, repo := setup(t)
	ctx := context.Background()
	ff := &fakeForge{head: openHead(), checks: green(), mergeErr: errors.Join(forge.ErrMergeRefused, errors.New("2 approving reviews are required"))}
	engine := workflow.New(db.SQL(), nil)
	engine.OnAutoMerge = func(ctx context.Context, tk gen.Task) error {
		return automerge.Merge(ctx, q, nil, ff, "o/r", tk, repo)
	}
	if ok, err := engine.EnqueueAutoMerge(ctx, task.ID); err != nil || !ok {
		t.Fatalf("enqueue ok=%v err=%v", ok, err)
	}
	if n := engine.DrainSideEffects(ctx); n != 1 {
		t.Fatalf("drained %d", n)
	}
	n, st, lastErr := outbox(t, db, task.ID)
	if n != 1 || st != "failed" || !strings.Contains(lastErr, "approving reviews") {
		t.Fatalf("n=%d status=%s last_error=%q", n, st, lastErr)
	}
	// A failed row blocks re-enqueueing: no silent loop.
	if ok, _ := engine.EnqueueAutoMerge(ctx, task.ID); ok {
		t.Fatal("must not re-enqueue while a failed row awaits a person")
	}
	if engine.DrainSideEffects(ctx) != 0 || len(ff.merges) != 1 {
		t.Fatalf("merge retried: %d", len(ff.merges))
	}
	row, _ := q.GetTaskAutoMerge(ctx, task.ID)
	if row.Reason != automerge.ReasonMergeRefused {
		t.Fatalf("row=%+v", row)
	}
	// Retry (what the Needs-you button calls) runs it again.
	ff.mergeErr = nil
	if cnt, err := engine.RetrySideEffects(ctx, task.ID); err != nil || cnt != 1 {
		t.Fatalf("retry cnt=%d err=%v", cnt, err)
	}
	engine.DrainSideEffects(ctx)
	if n, _, _ := outbox(t, db, task.ID); n != 0 || len(ff.merges) != 2 {
		t.Fatalf("after retry: rows=%d merges=%d", n, len(ff.merges))
	}
}
