package ghsync

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type rerouteFixture struct {
	s      *Syncer
	q      *gen.Queries
	hub    *fakeHub
	head   *forge.PRHead
	checks *[]forge.Check
	task   gen.Task
	ri     repoInfo
}

func newRerouteFixture(t *testing.T, mode, feedbackLabel string) *rerouteFixture {
	t.Helper()
	ctx := context.Background()
	head := &forge.PRHead{Number: 1, HeadSHA: "sha-aaa", BaseRef: "main", Mergeable: forge.MergeableClean, State: "pr_open", URL: "https://github.com/acme/widgets/pull/1"}
	checks := []forge.Check{{Name: "ci", Bucket: "fail"}}
	s, q, hub := newTestSyncerFull(t,
		func(context.Context, repoInfo, string) (forge.PRHead, error) { return *head, nil },
		func(context.Context, repoInfo, int) ([]forge.Review, error) { return nil, nil },
		func(context.Context, repoInfo, int) ([]forge.PRReviewComment, error) { return nil, nil },
		func(context.Context, repoInfo, int) ([]forge.Check, error) { return nil, nil },
		true)
	s.getChecks = func(context.Context, repoInfo, int) ([]forge.Check, error) { return checks, nil }

	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "wf-" + wfID}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"work", "review", "pr-fix", "done"} {
		p := gen.CreateWorkflowLabelParams{ID: uuid.NewString(), WorkflowID: wfID, Name: name, Color: "#000"}
		switch name {
		case "review":
			p.CreatePr = 1
			p.PrFeedbackLabel = feedbackLabel
		case "done":
			p.IsTerminal = 1
		}
		if _, err := q.CreateWorkflowLabel(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "widgets", Path: t.TempDir(), RemoteUrl: ghURL(), WorkflowID: &wfID,
		AutoMergeMode: mode, AutoMergeMethod: "squash",
	}); err != nil {
		t.Fatal(err)
	}
	task := newTestTask(t, q, repoID, wfID, "done", "feature-branch", "", "pr_open", head.URL)
	from := "work"
	if err := q.CreateTaskLabelHistory(ctx, gen.CreateTaskLabelHistoryParams{
		ID: uuid.NewString(), TaskID: task.ID, FromLabel: &from, ToLabel: "review", Trigger: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	return &rerouteFixture{s: s, q: q, hub: hub, head: head, checks: &checks, task: task,
		ri: repoInfo{ghName: "acme/widgets", repo: mustGetRepo(t, q, repoID)}}
}

func (f *rerouteFixture) sync(t *testing.T) string {
	t.Helper()
	f.s.syncTask(context.Background(), f.task, f.ri)
	got, err := f.q.GetTask(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.Label
}

func (f *rerouteFixture) backToDone(t *testing.T) {
	t.Helper()
	if err := f.s.engine.MoveBack(context.Background(), f.task.ID, "done", "human", "back", "pr-fix"); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalReroute_FailedCheckMovesToPRFix(t *testing.T) {
	f := newRerouteFixture(t, "when_green", "pr-fix")
	if got := f.sync(t); got != "pr-fix" {
		t.Fatalf("label = %q, want pr-fix", got)
	}
	hist, _ := f.q.ListTaskLabelHistory(context.Background(), f.task.ID)
	last := hist[len(hist)-1]
	if last.ActorID == nil || *last.ActorID != workflow.ActorPRFeedbackSync || last.FromLabel == nil || *last.FromLabel != "done" {
		t.Fatalf("history = %+v", last)
	}
	if last.Note == nil || !strings.Contains(*last.Note, "sha-aaa") {
		t.Fatalf("note = %v", last.Note)
	}
}

func TestTerminalReroute_ConflictMoves(t *testing.T) {
	f := newRerouteFixture(t, "when_green", "pr-fix")
	*f.checks = nil
	f.head.Mergeable = forge.MergeableConflicting
	if got := f.sync(t); got != "pr-fix" {
		t.Fatalf("label = %q", got)
	}
}

func TestTerminalReroute_AutoMergeOff_NotMoved(t *testing.T) {
	f := newRerouteFixture(t, "off", "pr-fix")
	if got := f.sync(t); got != "done" {
		t.Fatalf("label = %q", got)
	}
}

func TestTerminalReroute_NoFeedbackLabel_NotMoved(t *testing.T) {
	f := newRerouteFixture(t, "when_green", "")
	if got := f.sync(t); got != "done" {
		t.Fatalf("label = %q", got)
	}
}

func TestTerminalReroute_ChecksPending_NotMoved(t *testing.T) {
	f := newRerouteFixture(t, "when_green", "pr-fix")
	*f.checks = []forge.Check{{Name: "ci", Bucket: "pending"}}
	if got := f.sync(t); got != "done" {
		t.Fatalf("label = %q", got)
	}
}

func TestTerminalReroute_SameHeadNotRepeated_NewHeadAllowed_CapStops(t *testing.T) {
	f := newRerouteFixture(t, "when_green", "pr-fix")
	if got := f.sync(t); got != "pr-fix" {
		t.Fatalf("label = %q", got)
	}
	f.backToDone(t)
	if got := f.sync(t); got != "done" {
		t.Fatalf("same head re-routed: %q", got)
	}
	for i, sha := range []string{"sha-bbb", "sha-ccc"} {
		f.head.HeadSHA = sha
		if got := f.sync(t); got != "pr-fix" {
			t.Fatalf("iteration %d: label = %q", i, got)
		}
		f.backToDone(t)
	}
	f.head.HeadSHA = "sha-ddd"
	if got := f.sync(t); got != "done" {
		t.Fatalf("cap exceeded but moved: %q", got)
	}
}

func TestTerminalReroute_PendingTerminalSideEffect_NotMoved(t *testing.T) {
	f := newRerouteFixture(t, "when_green", "pr-fix")
	if err := f.q.EnqueueTaskSideEffect(context.Background(), gen.EnqueueTaskSideEffectParams{
		ID: uuid.NewString(), TaskID: f.task.ID, Kind: workflow.SideEffectTerminal,
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.sync(t); got != "done" {
		t.Fatalf("label = %q", got)
	}
}
