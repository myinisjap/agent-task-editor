package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type csFixture struct {
	h            *handlers.TasksHandler
	q            *gen.Queries
	parent, b, c gen.Task
}

type csCheckRec struct {
	mu  sync.Mutex
	ids []string
}

func (c *csCheckRec) Check(_ context.Context, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, id)
}

func newCSFixture(t *testing.T) (*csFixture, *csCheckRec) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()
	wfs, _ := q.ListWorkflows(ctx)
	wfID := wfs[0].ID
	labels, _ := q.ListWorkflowLabels(ctx, wfID)
	term := ""
	for _, l := range labels {
		if l.IsTerminal != 0 {
			term = l.Name
			break
		}
	}
	mkRepo := func(n string) string {
		id := uuid.NewString()
		if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: id, Name: n, Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	mk := func(title, repo string, parent *string) gen.Task {
		tk, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: title, WorkflowID: wfID, RepoID: repo, Label: term})
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
	f := &csFixture{q: q}
	f.parent = mk("parent", mkRepo("a"), nil)
	pid := f.parent.ID
	f.b = mk("api", mkRepo("b"), &pid)
	f.c = mk("client", mkRepo("c"), &pid)
	for id, st := range map[string]string{f.b.ID: "pr_open", f.c.ID: "pr_open"} {
		if _, err := db.SQL().Exec(`INSERT INTO task_git (task_id, branch, git_state, pr_url) VALUES (?, 'br', ?, 'https://x/'||?)`, id, st, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{TaskID: f.c.ID, DependsOnTaskID: f.b.ID}); err != nil {
		t.Fatal(err)
	}
	f.h = handlers.NewTasksHandler(q, workflow.New(db.SQL(), noopPub{}), t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil, nil)
	rec := &csCheckRec{}
	f.h.SetChangeSetAnnouncer(rec)
	return f, rec
}

func (f *csFixture) router() http.Handler {
	r := chi.NewRouter()
	r.Get("/tasks", f.h.List)
	r.Get("/tasks/{id}", f.h.Get)
	r.Patch("/tasks/{id}/git-state", f.h.UpdateGitState)
	return r
}

func TestMergeOrderPending_GetAndList(t *testing.T) {
	f, _ := newCSFixture(t)
	r := f.router()
	type pred struct {
		TaskID string `json:"task_id"`
		Title  string `json:"title"`
	}
	get := func(id string) []pred {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/"+id, nil))
		var body struct {
			M []pred `json:"merge_order_pending"`
		}
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil || body.M == nil {
			t.Fatalf("decode %v m=%v", err, body.M)
		}
		return body.M
	}
	if m := get(f.c.ID); len(m) != 1 || m[0].TaskID != f.b.ID || m[0].Title != "api" {
		t.Fatalf("client pending = %+v", m)
	}
	if m := get(f.b.ID); len(m) != 0 {
		t.Fatalf("api pending = %+v", m)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	if !strings.Contains(w.Body.String(), `"merge_order_pending":[{"task_id":"`+f.b.ID) {
		t.Fatalf("list missing merge_order_pending: %s", w.Body.String())
	}
	// Merged predecessor clears the warning.
	if _, err := f.q.UpdateTaskGitState(context.Background(), gen.UpdateTaskGitStateParams{GitState: "pr_merged", ID: f.b.ID}); err != nil {
		t.Fatal(err)
	}
	if m := get(f.c.ID); len(m) != 0 {
		t.Fatalf("after merge pending = %+v", m)
	}
}

func TestUpdateGitState_AnnouncesChangeSet(t *testing.T) {
	f, rec := newCSFixture(t)
	req := httptest.NewRequest(http.MethodPatch, "/tasks/"+f.b.ID+"/git-state", strings.NewReader(`{"git_state":"pr_merged"}`))
	w := httptest.NewRecorder()
	f.router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if len(rec.ids) != 1 || rec.ids[0] != f.parent.ID {
		t.Fatalf("announcer checks = %v", rec.ids)
	}
}

func TestNeedsMe_ChangeSetPartial(t *testing.T) {
	f, _ := newCSFixture(t)
	ctx := context.Background()
	_, _ = f.q.UpdateTaskGitState(ctx, gen.UpdateTaskGitStateParams{GitState: "pr_merged", ID: f.b.ID})
	_, _ = f.q.UpdateTaskGitState(ctx, gen.UpdateTaskGitStateParams{GitState: "pr_closed", ID: f.c.ID})
	w := httptest.NewRecorder()
	handlers.NewNeedsMeHandler(f.q).List(w, httptest.NewRequest(http.MethodGet, "/needs-me", nil))
	var body struct {
		Items []struct {
			TaskID    string `json:"task_id"`
			Reason    string `json:"reason"`
			Message   string `json:"message"`
			ChangeSet *struct {
				Merged []struct{ TaskID, State string } `json:"merged"`
				Failed []struct{ TaskID, State string } `json:"failed"`
			} `json:"change_set"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, it := range body.Items {
		if it.TaskID == f.parent.ID && it.Reason == "change_set_partial" {
			if it.ChangeSet == nil || len(it.ChangeSet.Merged) != 1 || len(it.ChangeSet.Failed) != 1 || it.Message == "" {
				t.Fatalf("bad item %+v", it)
			}
			return
		}
	}
	t.Fatalf("no change_set_partial item in %+v", body.Items)
}
