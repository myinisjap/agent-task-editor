package tasksource

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
)

// TestMapIssueCommentsFiltersMarkerAndUntrusted covers GitHubIssues.
// FetchComments's classification logic (factored out as mapIssueComments so
// it's testable without shelling out to `gh`): only OWNER/MEMBER/COLLABORATOR
// map to TrustedAuthor, and any comment containing this system's own
// write-back marker is dropped outright, regardless of author.
func TestMapIssueCommentsFiltersMarkerAndUntrusted(t *testing.T) {
	in := []forge.IssueComment{
		{ID: "1", Author: "owner-user", AuthorAssociation: "OWNER", Body: "ok", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "2", Author: "member-user", AuthorAssociation: "MEMBER", Body: "ok", CreatedAt: "2026-01-01T00:01:00Z"},
		{ID: "3", Author: "collab-user", AuthorAssociation: "COLLABORATOR", Body: "ok", CreatedAt: "2026-01-01T00:02:00Z"},
		{ID: "4", Author: "contributor-user", AuthorAssociation: "CONTRIBUTOR", Body: "ok", CreatedAt: "2026-01-01T00:03:00Z"},
		{ID: "5", Author: "rando", AuthorAssociation: "NONE", Body: "ok", CreatedAt: "2026-01-01T00:04:00Z"},
		{ID: "6", Author: "owner-user", AuthorAssociation: "OWNER", Body: writeback.MarkerComment + "\nnotice", CreatedAt: "2026-01-01T00:05:00Z"},
	}
	out := mapIssueComments(in)

	if len(out) != 5 {
		t.Fatalf("expected the marker comment dropped (5 remain of 6), got %d: %+v", len(out), out)
	}
	trustByID := make(map[string]bool, len(out))
	for _, c := range out {
		trustByID[c.ID] = c.TrustedAuthor
	}
	wantTrust := map[string]bool{"1": true, "2": true, "3": true, "4": false, "5": false}
	for id, want := range wantTrust {
		if got, ok := trustByID[id]; !ok {
			t.Errorf("comment %s missing from output", id)
		} else if got != want {
			t.Errorf("comment %s TrustedAuthor = %v, want %v", id, got, want)
		}
	}
	if _, ok := trustByID["6"]; ok {
		t.Errorf("marker comment 6 should have been dropped, found in output")
	}
}

// seedRepoWithSync creates a workflow with a gate label ("triage"), a
// non-gate label ("doing"), and — when goneLabel is non-empty — that label
// plus human transitions into it from both "triage" and "doing" (so the
// move gone-action has somewhere to go from either starting point). It
// returns the repo and the two label names.
func seedRepoWithSync(t *testing.T, q *gen.Queries, updatePolicy, goneAction, goneLabel string, commentSync int64) (repo gen.Repo, gateLabel, offGateLabel string) {
	t.Helper()
	ctx := context.Background()

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID:   uuid.NewString(),
		Name: "wf-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, Name: "triage", Color: "#000", SortOrder: 0, AgentIgnore: 1,
	}); err != nil {
		t.Fatalf("create gate label: %v", err)
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, Name: "doing", Color: "#000", SortOrder: 1, AgentIgnore: 0,
	}); err != nil {
		t.Fatalf("create off-gate label: %v", err)
	}
	if goneLabel != "" {
		if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID: uuid.NewString(), WorkflowID: wf.ID, Name: goneLabel, Color: "#000", SortOrder: 2, AgentIgnore: 0,
		}); err != nil {
			t.Fatalf("create gone-target label: %v", err)
		}
		for _, from := range []string{"triage", "doing"} {
			if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
				ID: uuid.NewString(), WorkflowID: wf.ID, FromLabel: from, ToLabel: goneLabel, TriggerType: "human",
			}); err != nil {
				t.Fatalf("create transition %s->%s: %v", from, goneLabel, err)
			}
		}
	}

	remote := "https://github.com/acme/widgets"
	repo, err = q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:                      uuid.NewString(),
		Name:                    "acme/widgets",
		Path:                    t.TempDir(),
		RemoteUrl:               &remote,
		WorkflowID:              &wf.ID,
		IssueSyncEnabled:        1,
		IssueSyncLabel:          "agent-ok",
		IssueSyncUpdatePolicy:   updatePolicy,
		IssueSyncGoneAction:     goneAction,
		IssueSyncGoneLabel:      goneLabel,
		IssueCommentSyncEnabled: commentSync,
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return repo, "triage", "doing"
}

// fakeCommentSource is a fakeSource that also implements CommentSource,
// returning a fixed comment set per source ref.
type fakeCommentSource struct {
	fakeSource
	comments    map[string][]ExternalComment
	commentsErr error
}

func (f fakeCommentSource) FetchComments(ctx context.Context, repo gen.Repo, ref string) ([]ExternalComment, error) {
	if f.commentsErr != nil {
		return nil, f.commentsErr
	}
	return f.comments[ref], nil
}

func getTaskByRef(t *testing.T, q *gen.Queries, ref string) gen.Task {
	t.Helper()
	task, err := q.GetTaskBySource(context.Background(), gen.GetTaskBySourceParams{Source: "github", SourceRef: ref})
	if err != nil {
		t.Fatalf("get task by source %q: %v", ref, err)
	}
	return task
}

func TestSweepUpdatesTitleOnGateLabel(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	// Default policy fields are "" (the Phase-1 carry-over case): must behave
	// as the documented "gate" default.
	repo, gateLabel, _ := seedRepoWithSync(t, q, "", "", "", 0)
	_ = repo

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Old title", Body: "body", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.Label != gateLabel {
		t.Fatalf("task label = %q, want %q", task.Label, gateLabel)
	}

	src.items[0].Title = "New title"
	pub := &recordingPub{}
	im.hub = pub
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.Title != "New title" {
		t.Errorf("title = %q, want %q", task.Title, "New title")
	}
	if len(pub.events) != 1 || pub.events[0] != "task.updated" {
		t.Errorf("events = %v, want [task.updated]", pub.events)
	}
}

func TestSweepUpdatesTypeOnLabelChange(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "", "", 0)

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "T", Body: "b", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.Type != "bug" {
		t.Fatalf("type = %q, want bug", task.Type)
	}

	src.items[0].Labels = []string{"chore"}
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.Type != "chore" {
		t.Errorf("type = %q, want chore", task.Type)
	}
}

func TestSweepNoOpWhenNothingChanged(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "", "", 0)

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "T", Body: "b", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	before := getTaskByRef(t, q, "acme/widgets#1")

	pub := &recordingPub{}
	im.hub = pub
	im.Sweep(ctx) // identical items again

	after := getTaskByRef(t, q, "acme/widgets#1")
	if before.Title != after.Title || before.Description != after.Description || before.Type != after.Type {
		t.Errorf("task fields changed on no-op sweep: before=%+v after=%+v", before, after)
	}
	if len(pub.events) != 0 {
		t.Errorf("expected no WS events on no-op sweep, got %v", pub.events)
	}
}

func TestSweepGatePolicyBlocksUpdateOffGate(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repo, gateLabel, offGateLabel := seedRepoWithSync(t, q, "gate", "", "", 0)
	_ = repo

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Old title", Body: "b", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.Label != gateLabel {
		t.Fatalf("expected task to start on gate label %q, got %q", gateLabel, task.Label)
	}
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: offGateLabel, ID: task.ID}); err != nil {
		t.Fatalf("move task off gate: %v", err)
	}

	src.items[0].Title = "New title"
	pub := &recordingPub{}
	im.hub = pub
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.Title != "Old title" {
		t.Errorf("title = %q, want unchanged %q (gate policy off-gate)", task.Title, "Old title")
	}
	if len(pub.events) != 0 {
		t.Errorf("expected no WS events, got %v", pub.events)
	}
}

func TestSweepAlwaysPolicyUpdatesOffGate(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "always", "", "", 0)

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Old title", Body: "b", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: "doing", ID: task.ID}); err != nil {
		t.Fatalf("move task off gate: %v", err)
	}

	src.items[0].Title = "New title"
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.Title != "New title" {
		t.Errorf("title = %q, want %q (always policy updates off gate)", task.Title, "New title")
	}
}

func TestSweepNeverPolicyBlocksUpdateOnGate(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repo, gateLabel, _ := seedRepoWithSync(t, q, "never", "", "", 0)
	_ = repo

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Old title", Body: "b", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.Label != gateLabel {
		t.Fatalf("expected task on gate label %q, got %q", gateLabel, task.Label)
	}

	src.items[0].Title = "New title"
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.Title != "Old title" {
		t.Errorf("title = %q, want unchanged %q (never policy)", task.Title, "Old title")
	}
}

func TestSweepFlagsMissingIssueAsGone(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "", "", 0)

	src := &fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "T", Body: "b", URL: "https://github.com/acme/widgets/issues/1"},
	}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "" {
		t.Fatalf("expected source_state empty right after import, got %q", task.SourceState)
	}

	src.items = nil // issue no longer present in the fetch
	pub := &recordingPub{}
	im.hub = pub
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "gone" {
		t.Errorf("source_state = %q, want gone", task.SourceState)
	}
	if task.Archived != 0 {
		t.Errorf("expected task not archived (default flag action)")
	}
	found := false
	for _, e := range pub.events {
		if e == "task.updated" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a task.updated event on flag, got %v", pub.events)
	}
}

func TestSweepGoneActionArchive(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "archive", "", 0)

	src := &fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	src.items = nil
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "gone" {
		t.Fatalf("source_state = %q, want gone", task.SourceState)
	}
	if task.Archived == 0 {
		t.Errorf("expected task archived")
	}
}

func TestSweepGoneActionArchiveSkippedWithActiveRun(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "archive", "", 0)

	src := &fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	runID := uuid.NewString()
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: task.ID}); err != nil {
		t.Fatalf("set active run: %v", err)
	}

	src.items = nil
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "gone" {
		t.Fatalf("source_state = %q, want gone", task.SourceState)
	}
	if task.Archived != 0 {
		t.Errorf("expected task NOT archived while an agent run is active, got archived=%d", task.Archived)
	}
}

func TestSweepGoneActionMove(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repo, gateLabel, _ := seedRepoWithSync(t, q, "", "move", "shelved", 0)

	src := &fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}}
	hub := &recordingPub{}
	engine := workflow.New(db.SQL(), hub)
	im := NewWithEngine(db.SQL(), hub, time.Minute, src, engine)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.Label != gateLabel {
		t.Fatalf("task label = %q, want %q", task.Label, gateLabel)
	}
	_ = repo

	src.items = nil
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "gone" {
		t.Fatalf("source_state = %q, want gone", task.SourceState)
	}
	if task.Label != "shelved" {
		t.Errorf("label = %q, want shelved", task.Label)
	}
}

func TestSweepGoneActionMoveFallsBackToFlagWithNilEngine(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "move", "shelved", 0)

	src := &fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src) // no engine
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	startLabel := task.Label

	src.items = nil
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "gone" {
		t.Fatalf("source_state = %q, want gone", task.SourceState)
	}
	if task.Label != startLabel {
		t.Errorf("label = %q, want unchanged %q (nil engine falls back to flag)", task.Label, startLabel)
	}
}

func TestSweepGoneActionMoveFallsBackToFlagWithEmptyLabel(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "move", "", 0) // move action but no target label configured

	src := &fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}}
	hub := &recordingPub{}
	engine := workflow.New(db.SQL(), hub)
	im := NewWithEngine(db.SQL(), hub, time.Minute, src, engine)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	startLabel := task.Label

	src.items = nil
	im.Sweep(ctx)

	task = getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "gone" {
		t.Fatalf("source_state = %q, want gone", task.SourceState)
	}
	if task.Label != startLabel {
		t.Errorf("label = %q, want unchanged %q (empty gone label falls back to flag)", task.Label, startLabel)
	}
}

func TestSweepGoneTaskStaysUntouchedOnLaterSweep(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "", "", 0)

	src := &fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	src.items = nil
	im.Sweep(ctx) // flags gone

	goneAt := getTaskByRef(t, q, "acme/widgets#1").SourceStateAt

	pub := &recordingPub{}
	im.hub = pub
	im.Sweep(ctx) // still gone, still absent from fetch -> idempotent no-op

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "gone" {
		t.Fatalf("source_state = %q, want gone", task.SourceState)
	}
	if len(pub.events) != 0 {
		t.Errorf("expected no events re-flagging an already-gone task, got %v", pub.events)
	}
	if goneAt == nil || task.SourceStateAt == nil || !task.SourceStateAt.Equal(*goneAt) {
		t.Errorf("source_state_at changed on idempotent re-sweep: before=%v after=%v", goneAt, task.SourceStateAt)
	}
}

func TestSweepReappearingIssueClearsGoneState(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "", "", 0)

	src := &fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	src.items = nil
	im.Sweep(ctx)
	if getTaskByRef(t, q, "acme/widgets#1").SourceState != "gone" {
		t.Fatalf("expected task flagged gone")
	}

	// The issue reopens / regains the filter label: it reappears in the fetch.
	src.items = []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if task.SourceState != "" {
		t.Errorf("source_state = %q, want cleared to empty on reappearance", task.SourceState)
	}
}

func TestSweepIngestsCommentsFromTrustedAuthors(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "", "", 1)

	// Untrusted authors are dropped by the Importer itself (checking
	// TrustedAuthor); the write-back-marker filter and author_association ->
	// TrustedAuthor mapping are GitHubIssues.FetchComments's job (see
	// mapIssueComments and TestMapIssueCommentsFiltersMarkerAndUntrusted),
	// so this fake CommentSource — standing in for an already-classified
	// Source — returns comments as a real Source would after that mapping.
	src := fakeCommentSource{
		fakeSource: fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}},
		comments: map[string][]ExternalComment{
			"acme/widgets#1": {
				{ID: "c1", Author: "alice", Body: "looks good", CreatedAt: "2026-01-01T00:00:00Z", TrustedAuthor: true},
				{ID: "c2", Author: "mallory", Body: "spam", CreatedAt: "2026-01-01T00:01:00Z", TrustedAuthor: false},
			},
		},
	}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx) // creation sweep: comment ingestion only applies to existing tasks

	task := getTaskByRef(t, q, "acme/widgets#1")

	pub := &recordingPub{}
	im.hub = pub
	im.Sweep(ctx) // now the task exists: comments are fetched and filtered

	comments, err := q.ListTaskSourceComments(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 ingested comment (untrusted dropped), got %d: %+v", len(comments), comments)
	}
	if comments[0].Author != "alice" {
		t.Errorf("author = %q, want alice", comments[0].Author)
	}

	commentEvents := 0
	for _, e := range pub.events {
		if e == "task.source_comment_added" {
			commentEvents++
		}
	}
	if commentEvents != 1 {
		t.Errorf("expected 1 task.source_comment_added event, got %d", commentEvents)
	}

	// Re-sweep must not duplicate.
	im.Sweep(ctx)
	comments, err = q.ListTaskSourceComments(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected still 1 comment after re-sweep, got %d", len(comments))
	}
}

func TestSweepCommentIngestionRespectsUpdatePolicy(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repo, _, offGateLabel := seedRepoWithSync(t, q, "gate", "", "", 1)
	_ = repo

	src := fakeCommentSource{
		fakeSource: fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}},
		comments: map[string][]ExternalComment{
			"acme/widgets#1": {{ID: "c1", Author: "alice", Body: "hi", CreatedAt: "2026-01-01T00:00:00Z", TrustedAuthor: true}},
		},
	}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx)

	task := getTaskByRef(t, q, "acme/widgets#1")
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: offGateLabel, ID: task.ID}); err != nil {
		t.Fatalf("move off gate: %v", err)
	}

	im.Sweep(ctx) // still fetches the same comment, but task is off-gate now

	comments, err := q.ListTaskSourceComments(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 {
		t.Errorf("expected no comments ingested once off the gate label under gate policy, got %d", len(comments))
	}
}

func TestSweepCommentSyncDisabledIngestsNothing(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepoWithSync(t, q, "", "", "", 0) // comment sync disabled (default)

	src := fakeCommentSource{
		fakeSource: fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "T"}}},
		comments: map[string][]ExternalComment{
			"acme/widgets#1": {{ID: "c1", Author: "alice", Body: "hi", CreatedAt: "2026-01-01T00:00:00Z", TrustedAuthor: true}},
		},
	}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	ctx := context.Background()
	im.Sweep(ctx) // creation sweep

	task := getTaskByRef(t, q, "acme/widgets#1")
	im.Sweep(ctx) // existing-task sweep: would ingest if comment sync were enabled

	comments, err := q.ListTaskSourceComments(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 {
		t.Errorf("expected no comments ingested when issue_comment_sync_enabled = 0, got %d", len(comments))
	}
}
