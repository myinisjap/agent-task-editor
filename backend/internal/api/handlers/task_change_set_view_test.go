package handlers_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type csvFixture struct {
	r                   http.Handler
	q                   *gen.Queries
	sql                 *sql.DB
	wf                  string
	parent              gen.Task
	home, b, c          gen.Task
	repoA, repoB, repoC string
}

func newCSVFixture(t *testing.T) *csvFixture {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()
	wfs, _ := q.ListWorkflows(ctx)
	wf := wfs[0].ID
	mkRepo := func(n string) string {
		id := uuid.NewString()
		if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: id, Name: n, Path: t.TempDir(), WorkflowID: &wf}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f := &csvFixture{q: q, sql: db.SQL(), wf: wf}
	f.repoA, f.repoB, f.repoC = mkRepo("a"), mkRepo("b"), mkRepo("c")
	f.parent = mkTask(t, q, wf, f.repoA, "parent", "plan")
	f.home = f.child(t, "home", f.repoA)
	f.b = f.child(t, "api", f.repoB)
	f.c = f.child(t, "client", f.repoC)
	h := handlers.NewTasksHandler(q, workflow.New(db.SQL(), noopPub{}), t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil, nil)
	r := chi.NewRouter()
	r.Get("/tasks", h.List)
	r.Get("/tasks/{id}", h.Get)
	r.Get("/tasks/{id}/change-set", h.ChangeSet)
	f.r = r
	return f
}

func (f *csvFixture) child(t *testing.T, title, repo string) gen.Task {
	t.Helper()
	tk := mkTask(t, f.q, f.wf, repo, title, "plan")
	if _, err := f.sql.Exec(`UPDATE tasks SET parent_task_id = ?, created_at = datetime('now', ?) WHERE id = ?`, f.parent.ID, "+"+itoa(map[string]int{"home": 1, "api": 2, "client": 3}[title])+" seconds", tk.ID); err != nil {
		t.Fatal(err)
	}
	return tk
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func (f *csvFixture) get(t *testing.T, path string, out any) int {
	t.Helper()
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if out != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decode: %v: %s", err, w.Body.String())
		}
	}
	return w.Code
}

func (f *csvFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.sql.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

type csTask struct {
	ID                 string   `json:"id"`
	ChangeSetRepoCount int      `json:"change_set_repo_count"`
	ForeignChild       bool     `json:"foreign_child"`
	CumulativeCost     float64  `json:"cumulative_cost_usd"`
	RolledUp           *float64 `json:"rolled_up_cost_usd"`
}

func TestChangeSetFields_CountForeignAndRollup(t *testing.T) {
	f := newCSVFixture(t)
	var list []csTask
	f.get(t, "/tasks", &list)
	by := map[string]csTask{}
	for _, x := range list {
		by[x.ID] = x
	}
	if by[f.parent.ID].ChangeSetRepoCount != 3 {
		t.Fatalf("repo count = %d, want 3", by[f.parent.ID].ChangeSetRepoCount)
	}
	if by[f.home.ID].ForeignChild || by[f.parent.ID].ForeignChild || !by[f.b.ID].ForeignChild || !by[f.c.ID].ForeignChild {
		t.Fatalf("foreign_child wrong: %+v", by)
	}
	var kids []csTask
	f.get(t, "/tasks?parent_id="+f.parent.ID, &kids)
	for _, k := range kids {
		if k.ForeignChild != (k.ID == f.b.ID || k.ID == f.c.ID) {
			t.Fatalf("parent_id branch foreign_child wrong for %s", k.ID)
		}
	}

	// Archive the client child: it no longer counts toward the repos.
	f.exec(t, `UPDATE tasks SET archived = 1 WHERE id = ?`, f.c.ID)
	var p csTask
	f.get(t, "/tasks/"+f.parent.ID, &p)
	if p.ChangeSetRepoCount != 2 {
		t.Fatalf("after archive repo count = %d, want 2", p.ChangeSetRepoCount)
	}
	var fc csTask
	f.get(t, "/tasks/"+f.b.ID, &fc)
	if !fc.ForeignChild || fc.RolledUp != nil {
		t.Fatalf("child get: %+v", fc)
	}

	// Cost roll-up: parent 1, child b 2, archived child c 4.
	for id, cost := range map[string]float64{f.parent.ID: 1, f.b.ID: 2, f.c.ID: 4} {
		f.exec(t, `INSERT INTO agent_runs (id, task_id, status, cost_usd) VALUES (?, ?, 'completed', ?)`, uuid.NewString(), id, cost)
	}
	f.get(t, "/tasks/"+f.parent.ID, &p)
	if p.RolledUp == nil || *p.RolledUp != 7 || p.CumulativeCost != 1 {
		t.Fatalf("rollup = %v cumulative = %v", p.RolledUp, p.CumulativeCost)
	}
}

func TestChangeSetFields_PlainAndSingleRepo(t *testing.T) {
	f := newCSVFixture(t)
	plain := mkTask(t, f.q, f.wf, f.repoA, "plain", "plan")
	var x csTask
	f.get(t, "/tasks/"+plain.ID, &x)
	if x.ChangeSetRepoCount != 0 || x.ForeignChild || x.RolledUp != nil {
		t.Fatalf("plain: %+v", x)
	}
	// Single-repo family.
	sp := mkTask(t, f.q, f.wf, f.repoA, "sp", "plan")
	sc := mkTask(t, f.q, f.wf, f.repoA, "sc", "plan")
	f.exec(t, `UPDATE tasks SET parent_task_id = ? WHERE id = ?`, sp.ID, sc.ID)
	f.get(t, "/tasks/"+sp.ID, &x)
	if x.ChangeSetRepoCount != 0 {
		t.Fatalf("single repo count = %d", x.ChangeSetRepoCount)
	}
	var cs struct {
		MultiRepo bool  `json:"multi_repo"`
		Children  []any `json:"children"`
	}
	if code := f.get(t, "/tasks/"+sp.ID+"/change-set", &cs); code != 200 || cs.MultiRepo || len(cs.Children) != 0 {
		t.Fatalf("single-repo change-set: %d %+v", code, cs)
	}
	if code := f.get(t, "/tasks/nope/change-set", nil); code != 404 {
		t.Fatalf("unknown = %d", code)
	}
}

func TestChangeSetEndpoint_MultiRepo(t *testing.T) {
	f := newCSVFixture(t)
	f.exec(t, `INSERT INTO task_git (task_id, branch, git_state, pr_url) VALUES (?, '', 'pr_open', 'https://x/b')`, f.b.ID)
	if err := f.q.CreateTaskDependency(context.Background(), gen.CreateTaskDependencyParams{TaskID: f.c.ID, DependsOnTaskID: f.b.ID}); err != nil {
		t.Fatal(err)
	}
	rid := uuid.NewString()
	f.exec(t, `INSERT INTO agent_runs (id, task_id, status, cost_usd) VALUES (?, ?, 'completed', 2)`, rid, f.b.ID)
	f.exec(t, `INSERT INTO run_verifications (run_id, task_id, command, status) VALUES (?, ?, 'x', 'pass')`, rid, f.b.ID)
	if err := f.q.UpsertTaskAutoMerge(context.Background(), gen.UpsertTaskAutoMergeParams{TaskID: f.c.ID, State: "waiting", Reason: "change_set_order", Message: "wait for api", HeadSha: "h"}); err != nil {
		t.Fatal(err)
	}

	type resp struct {
		MultiRepo  bool     `json:"multi_repo"`
		MergeOrder []string `json:"merge_order"`
		Parent     struct {
			TaskID string  `json:"task_id"`
			Cost   float64 `json:"rolled_up_cost_usd"`
		} `json:"parent"`
		Children []struct {
			TaskID        string `json:"task_id"`
			Foreign       bool   `json:"foreign"`
			PRURL         string `json:"pr_url"`
			Verification  string `json:"verification"`
			Changed       any    `json:"changed"`
			MergePosition *int   `json:"merge_position"`
			AutoMerge     *struct {
				Reason string `json:"reason"`
			} `json:"auto_merge"`
		} `json:"children"`
	}
	var a, b resp
	if code := f.get(t, "/tasks/"+f.parent.ID+"/change-set", &a); code != 200 {
		t.Fatalf("code %d", code)
	}
	f.get(t, "/tasks/"+f.c.ID+"/change-set", &b)
	if !a.MultiRepo || b.Parent.TaskID != f.parent.ID || len(b.Children) != 3 {
		t.Fatalf("child-id call: %+v", b)
	}
	if len(a.Children) != 3 || a.Children[0].TaskID != f.home.ID || a.Children[1].TaskID != f.b.ID || a.Children[2].TaskID != f.c.ID {
		t.Fatalf("created order wrong: %+v", a.Children)
	}
	if len(a.MergeOrder) != 3 || a.MergeOrder[0] != f.home.ID || a.MergeOrder[1] != f.b.ID || a.MergeOrder[2] != f.c.ID {
		t.Fatalf("merge order: %v", a.MergeOrder)
	}
	if a.Parent.Cost != 2 {
		t.Fatalf("cost %v", a.Parent.Cost)
	}
	hb, cc := a.Children[1], a.Children[2]
	if hb.Verification != "pass" || cc.Verification != "not_run" || a.Children[0].Foreign || !hb.Foreign {
		t.Fatalf("verify/foreign: %+v", a.Children)
	}
	if hb.MergePosition == nil || cc.MergePosition == nil || a.Children[0].MergePosition == nil || *hb.MergePosition != 2 || *cc.MergePosition != 3 || *a.Children[0].MergePosition != 1 {
		t.Fatalf("positions: %+v", a.Children)
	}
	if hb.PRURL != "https://x/b" || cc.AutoMerge == nil || cc.AutoMerge.Reason != "change_set_order" || hb.Changed != nil {
		t.Fatalf("details: %+v", a.Children)
	}
}
