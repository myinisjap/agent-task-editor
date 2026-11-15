package automerge_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/automerge"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestPrecheck_ChangeSet(t *testing.T) {
	m := []automerge.ChangeSetMember{{TaskID: "a", Title: "API"}}
	cases := []struct {
		name   string
		mut    func(*automerge.Input)
		reason string
	}{
		{"empty facts unblocked", func(*automerge.Input) {}, ""},
		{"pending predecessor blocks", func(in *automerge.Input) { in.ChangeSet.PendingPredecessors = m }, automerge.ReasonChangeSetOrder},
		{"red sibling blocks", func(in *automerge.Input) { in.ChangeSet.RedSiblings = m }, automerge.ReasonChangeSetSiblingFailed},
		{"draft wins", func(in *automerge.Input) { in.ChangeSet.PendingPredecessors = m; in.PR.Draft = true }, automerge.ReasonDraft},
		{"sibling failure wins over order", func(in *automerge.Input) { in.ChangeSet.PendingPredecessors = m; in.ChangeSet.RedSiblings = m }, automerge.ReasonChangeSetSiblingFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput()
			tc.mut(&in)
			d := automerge.Evaluate(in)
			if d.Reason != tc.reason || (tc.reason == "" && !d.Eligible) {
				t.Fatalf("got %+v want reason %q", d, tc.reason)
			}
			if tc.reason != "" && d.Message == "" {
				t.Error("need message")
			}
		})
	}
}

type csFixture struct {
	db           *storage.DB
	q            *gen.Queries
	parent, b, c gen.Task
	home         gen.Task
	repoB, repoC gen.Repo
}

func csSetGit(t *testing.T, db *storage.DB, id, state string) {
	t.Helper()
	if _, err := db.SQL().Exec(`INSERT INTO task_git (task_id, branch, git_state, pr_url) VALUES (?, 'ate/'||?, ?, 'https://x/pr/'||?)
		ON CONFLICT(task_id) DO UPDATE SET git_state = excluded.git_state`, id, id, state, id); err != nil {
		t.Fatal(err)
	}
}

func csFixtureSetup(t *testing.T) *csFixture {
	t.Helper()
	db, q, parent, repoA := setup(t)
	ctx := context.Background()
	mkRepo := func(name string) gen.Repo {
		id := uuid.NewString()
		if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: id, Name: name, Path: t.TempDir(), WorkflowID: &parent.WorkflowID, AutoMergeMode: "when_green", AutoMergeMethod: "merge"}); err != nil {
			t.Fatal(err)
		}
		r, _ := q.GetRepo(ctx, id)
		return r
	}
	_ = repoA
	f := &csFixture{db: db, q: q, parent: parent, repoB: mkRepo("b"), repoC: mkRepo("c")}
	mk := func(title string, repo gen.Repo) gen.Task {
		tk, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: title, WorkflowID: parent.WorkflowID, RepoID: repo.ID, Label: "review"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(`UPDATE tasks SET parent_task_id = ? WHERE id = ?`, parent.ID, tk.ID); err != nil {
			t.Fatal(err)
		}
		csSetGit(t, db, tk.ID, "pr_open")
		tk, _ = q.GetTask(ctx, tk.ID)
		return tk
	}
	f.b = mk("api", f.repoB)
	f.c = mk("client", f.repoC)
	if err := q.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{TaskID: f.c.ID, DependsOnTaskID: f.b.ID}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *csFixture) assessC(t *testing.T) automerge.Decision {
	t.Helper()
	ctx := context.Background()
	c, _ := f.q.GetTask(ctx, f.c.ID)
	ff := &fakeForge{checks: green()}
	d, err := automerge.Reconcile(ctx, f.q, nil, nil, automerge.ForgeSource{Forge: ff, RepoName: "o/c"}, c, f.repoC, openHead())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestChangeSet_OrderAndSiblingRed(t *testing.T) {
	f := csFixtureSetup(t)
	ctx := context.Background()

	if d := f.assessC(t); d.Reason != automerge.ReasonChangeSetOrder {
		t.Fatalf("B open: %+v", d)
	}
	row, _ := f.q.GetTaskAutoMerge(ctx, f.c.ID)
	if row.Reason != automerge.ReasonChangeSetOrder {
		t.Fatalf("recorded reason %q", row.Reason)
	}

	csSetGit(t, f.db, f.b.ID, "pr_merged")
	if d := f.assessC(t); !d.Eligible {
		t.Fatalf("B merged: %+v", d)
	}

	csSetGit(t, f.db, f.b.ID, "pr_closed")
	if d := f.assessC(t); d.Reason != automerge.ReasonChangeSetSiblingFailed {
		t.Fatalf("B closed: %+v", d)
	}

	// B closed also blocks via order; make B open + merged-less red facts for the sibling-only paths.
	if err := f.q.DeleteTaskDependency(ctx, gen.DeleteTaskDependencyParams{TaskID: f.c.ID, DependsOnTaskID: f.b.ID}); err != nil {
		t.Fatal(err)
	}
	csSetGit(t, f.db, f.b.ID, "pr_open")
	if d := f.assessC(t); !d.Eligible {
		t.Fatalf("B open, no edge: %+v", d)
	}
	if err := f.q.UpsertTaskAutoMerge(ctx, gen.UpsertTaskAutoMergeParams{TaskID: f.b.ID, State: "waiting", Reason: automerge.ReasonChecksFailed, Message: "x", HeadSha: "h"}); err != nil {
		t.Fatal(err)
	}
	if d := f.assessC(t); d.Reason != automerge.ReasonChangeSetSiblingFailed {
		t.Fatalf("B checks_failed: %+v", d)
	}
	if _, err := f.q.DeleteTaskAutoMerge(ctx, f.b.ID); err != nil {
		t.Fatal(err)
	}
	fp := "sha1|ci"
	if _, err := f.q.UpsertTaskPRReviewState(ctx, gen.UpsertTaskPRReviewStateParams{TaskID: f.b.ID, HeadSha: "sha1", LastFailedCheckSha: &fp}); err != nil {
		t.Fatal(err)
	}
	if d := f.assessC(t); d.Reason != automerge.ReasonChangeSetSiblingFailed {
		t.Fatalf("B failing fingerprint: %+v", d)
	}
	// New head: fingerprint is stale.
	if _, err := f.q.UpsertTaskPRReviewState(ctx, gen.UpsertTaskPRReviewStateParams{TaskID: f.b.ID, HeadSha: "sha2", LastFailedCheckSha: &fp}); err != nil {
		t.Fatal(err)
	}
	if d := f.assessC(t); !d.Eligible {
		t.Fatalf("stale fingerprint: %+v", d)
	}
}

func TestChangeSet_HomePredecessorAndTopLevel(t *testing.T) {
	f := csFixtureSetup(t)
	ctx := context.Background()
	// Home predecessor: same repo as parent.
	home, err := f.q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "home", WorkflowID: f.parent.WorkflowID, RepoID: f.parent.RepoID, Label: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SQL().Exec(`UPDATE tasks SET parent_task_id = ? WHERE id = ?`, f.parent.ID, home.ID); err != nil {
		t.Fatal(err)
	}
	_ = f.q.DeleteTaskDependency(ctx, gen.DeleteTaskDependencyParams{TaskID: f.c.ID, DependsOnTaskID: f.b.ID})
	if err := f.q.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{TaskID: f.c.ID, DependsOnTaskID: home.ID}); err != nil {
		t.Fatal(err)
	}
	csSetGit(t, f.db, f.b.ID, "pr_merged") // not red
	csSetGit(t, f.db, f.parent.ID, "pr_open")
	if d := f.assessC(t); d.Reason != automerge.ReasonChangeSetOrder {
		t.Fatalf("parent PR open: %+v", d)
	}
	csSetGit(t, f.db, f.parent.ID, "pr_merged")
	if d := f.assessC(t); !d.Eligible {
		t.Fatalf("parent merged: %+v", d)
	}
	// Archived predecessor is skipped.
	csSetGit(t, f.db, f.parent.ID, "pr_open")
	if _, err := f.db.SQL().Exec(`UPDATE tasks SET archived = 1 WHERE id = ?`, home.ID); err != nil {
		t.Fatal(err)
	}
	if d := f.assessC(t); !d.Eligible {
		t.Fatalf("archived pred: %+v", d)
	}

	// Top-level task: no facts.
	facts, err := automerge.LoadChangeSetFacts(ctx, f.q, f.parent)
	if err != nil || len(facts.PendingPredecessors) != 0 || len(facts.RedSiblings) != 0 {
		t.Fatalf("top-level facts %+v err=%v", facts, err)
	}
	_ = forge.MergeableClean
}
