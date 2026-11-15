package needsme

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

func csChild(parent gen.Task, id, repo, git string) gen.Task {
	pid := parent.ID
	return gen.Task{ID: id, Title: id, RepoID: repo, ParentTaskID: &pid, GitState: git, PrUrl: "https://x/" + id, UpdatedAt: time.Now()}
}

func TestClassifyChangeSet(t *testing.T) {
	parent := gen.Task{ID: "p", RepoID: "A"}
	term := func(ids ...string) map[string]bool {
		m := map[string]bool{}
		for _, i := range ids {
			m[i] = true
		}
		return m
	}
	cases := []struct {
		name     string
		kids     []gen.Task
		terminal map[string]bool
		lf       map[string]bool
		partial  bool
	}{
		{"merged + closed", []gen.Task{csChild(parent, "b", "B", "pr_merged"), csChild(parent, "c", "C", "pr_closed")}, term("b", "c"), nil, true},
		{"merged + land failed", []gen.Task{csChild(parent, "b", "B", "pr_merged"), csChild(parent, "c", "C", "pushed")}, term("c"), term("c"), true},
		{"closed but back on work label", []gen.Task{csChild(parent, "b", "B", "pr_merged"), csChild(parent, "c", "C", "pr_closed")}, term("b"), nil, false},
		{"failed child archived", []gen.Task{csChild(parent, "b", "B", "pr_merged"), func() gen.Task { c := csChild(parent, "c", "C", "pr_closed"); c.Archived = 1; return c }()}, term("c"), nil, false},
		{"nothing merged", []gen.Task{csChild(parent, "b", "B", "pr_open"), csChild(parent, "c", "C", "pr_closed")}, term("b", "c"), nil, false},
		{"home children ignored", []gen.Task{csChild(parent, "h", "A", "pr_closed"), csChild(parent, "b", "B", "pr_merged")}, term("h", "b"), nil, false},
		{"land failed but pr open", []gen.Task{csChild(parent, "b", "B", "pr_merged"), csChild(parent, "c", "C", "pr_open")}, term("c"), term("c"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs, ok := ClassifyChangeSet(parent, tc.kids, tc.terminal, tc.lf)
			if ok != tc.partial {
				t.Fatalf("ok=%v want %v (%+v)", ok, tc.partial, cs)
			}
			if ok && (len(cs.Merged) != 1 || len(cs.Failed) != 1 || ChangeSetMessage(cs) == "") {
				t.Fatalf("bad result %+v", cs)
			}
		})
	}
}

func TestClassify_ChangeSetPrecedence(t *testing.T) {
	cs := &ChangeSetPartial{Merged: []ChangeSetPR{{TaskID: "b"}}, Failed: []ChangeSetPR{{TaskID: "c"}}}
	run := &gen.AgentRun{Status: "waiting_human"}
	if it, _ := Classify(gen.Task{Label: "review"}, run, wfDef(), nil, cs); it.Reason != ReasonWaitingHuman {
		t.Errorf("waiting_human must win, got %s", it.Reason)
	}
	conflict := gen.Task{Label: "review", GitState: "pr_open", PrMergeable: "conflicting"}
	if it, _ := Classify(conflict, nil, wfDef(), nil, cs); it.Reason != ReasonChangeSetPartial || it.ChangeSet != cs {
		t.Errorf("partial must beat pr_conflict, got %s", it.Reason)
	}
	if it, _ := Classify(gen.Task{Label: "review"}, nil, wfDef(), nil, cs); it.Reason != ReasonChangeSetPartial {
		t.Errorf("partial must beat human_gate, got %s", it.Reason)
	}
	if it, ok := Classify(gen.Task{Label: "done", Paused: 1}, &gen.AgentRun{Status: "running"}, wfDef(), nil, cs); !ok || it.Reason != ReasonChangeSetPartial {
		t.Errorf("partial must show when paused/running/terminal, got %v %v", ok, it.Reason)
	}
	if _, ok := Classify(gen.Task{Label: "review", Archived: 1}, nil, wfDef(), nil, cs); ok {
		t.Error("archived parent must not show")
	}
}

type csFix struct {
	db           *storage.DB
	q            *gen.Queries
	parent, b, c gen.Task
}

func csSetup(t *testing.T) *csFix {
	t.Helper()
	f, err := os.CreateTemp("", "nm-*.db")
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
	wf := wfs[0].ID
	labels, _ := q.ListWorkflowLabels(ctx, wf)
	term, work := "", ""
	for _, l := range labels {
		if l.IsTerminal != 0 && term == "" {
			term = l.Name
		}
		if l.IsTerminal == 0 && l.AgentIgnore == 0 && work == "" {
			work = l.Name
		}
	}
	mkRepo := func(n string) string {
		id := uuid.NewString()
		if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: id, Name: n, Path: t.TempDir(), WorkflowID: &wf}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	mk := func(title, repo, label string, parent *string) gen.Task {
		tk, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: title, WorkflowID: wf, RepoID: repo, Label: label})
		if err != nil {
			t.Fatal(err)
		}
		if parent != nil {
			if _, err := db.SQL().Exec(`UPDATE tasks SET parent_task_id = ? WHERE id = ?`, *parent, tk.ID); err != nil {
				t.Fatal(err)
			}
		}
		return tk
	}
	fx := &csFix{db: db, q: q}
	fx.parent = mk("parent", mkRepo("a"), work, nil)
	pid := fx.parent.ID
	fx.b = mk("b", mkRepo("b"), term, &pid)
	fx.c = mk("c", mkRepo("c"), term, &pid)
	fx.setGit(t, fx.b.ID, "pr_merged")
	fx.setGit(t, fx.c.ID, "pr_closed")
	return fx
}

func (f *csFix) setGit(t *testing.T, id, st string) {
	t.Helper()
	if _, err := f.db.SQL().Exec(`INSERT INTO task_git (task_id, branch, git_state, pr_url) VALUES (?, 'br', ?, 'https://x/'||?)
		ON CONFLICT(task_id) DO UPDATE SET git_state = excluded.git_state`, id, st, id); err != nil {
		t.Fatal(err)
	}
}

func partialFor(t *testing.T, items []Item, id string) *Item {
	for i := range items {
		if items[i].TaskID == id && items[i].Reason == ReasonChangeSetPartial {
			return &items[i]
		}
	}
	return nil
}

func TestCompute_ChangeSetPartialAppearsAndClears(t *testing.T) {
	f := csSetup(t)
	ctx := context.Background()
	items, err := Compute(ctx, f.q)
	if err != nil {
		t.Fatal(err)
	}
	it := partialFor(t, items, f.parent.ID)
	if it == nil || len(it.ChangeSet.Merged) != 1 || len(it.ChangeSet.Failed) != 1 || it.Message == "" {
		t.Fatalf("expected partial item, got %+v", items)
	}
	if _, err := f.db.SQL().Exec(`UPDATE tasks SET archived = 1 WHERE id = ?`, f.c.ID); err != nil {
		t.Fatal(err)
	}
	items, _ = Compute(ctx, f.q)
	if partialFor(t, items, f.parent.ID) != nil {
		t.Fatal("archiving the failed child must clear the reason")
	}
}

func TestCompute_ChangeSetLandFailedSideEffect(t *testing.T) {
	f := csSetup(t)
	ctx := context.Background()
	f.setGit(t, f.c.ID, "pushed")
	items, _ := Compute(ctx, f.q)
	if partialFor(t, items, f.parent.ID) != nil {
		t.Fatal("no failed effect yet, must not be partial")
	}
	if _, err := f.db.SQL().Exec(`INSERT INTO task_side_effects (id, task_id, kind, status) VALUES (?, ?, 'create_pr', 'failed')`, uuid.NewString(), f.c.ID); err != nil {
		t.Skipf("cannot seed side effect: %v", err)
	}
	items, _ = Compute(ctx, f.q)
	it := partialFor(t, items, f.parent.ID)
	if it == nil || it.ChangeSet.Failed[0].State != ChangeSetLandFailed {
		t.Fatalf("want land_failed partial, got %+v", items)
	}
}

type fakePub struct {
	mu     sync.Mutex
	events []wsevent.TaskNeedsHuman
}

func (p *fakePub) Publish(typ string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if typ == wsevent.TypeTaskNeedsHuman {
		p.events = append(p.events, payload.(wsevent.TaskNeedsHuman))
	}
}

func TestChangeSetAnnouncer_EdgeTriggered(t *testing.T) {
	f := csSetup(t)
	ctx := context.Background()
	pub := &fakePub{}
	a := NewChangeSetAnnouncer(f.q, pub)
	a.Check(ctx, f.parent.ID)
	a.Check(ctx, f.parent.ID)
	if len(pub.events) != 1 {
		t.Fatalf("want exactly 1 event, got %d", len(pub.events))
	}
	ev := pub.events[0]
	if ev.TaskID != f.parent.ID || ev.Reason != "change_set_partial" || ev.RunID != "" || ev.Message == "" {
		t.Fatalf("bad event %+v", ev)
	}
	f.setGit(t, f.c.ID, "pr_open") // resolved
	a.Check(ctx, f.parent.ID)
	if len(pub.events) != 1 {
		t.Fatal("clearing must not publish")
	}
	f.setGit(t, f.c.ID, "pr_closed")
	a.Check(ctx, f.parent.ID)
	if len(pub.events) != 2 {
		t.Fatalf("re-partial must publish again, got %d", len(pub.events))
	}
	var nilA *ChangeSetAnnouncer
	nilA.Check(ctx, "x") // nil-safe
}
