package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type multiEnv struct {
	r            http.Handler
	q            *gen.Queries
	sql          *sql.DB
	wfID         string
	repoA, repoB string
	repoC        string
}

func setupMultiRepo(t *testing.T) *multiEnv {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID
	mk := func(name string) string {
		id := uuid.NewString()
		if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{ID: id, Name: name, Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	h := handlers.NewTasksHandler(q, workflow.New(db.SQL(), noopPub{}), t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil, nil)
	h.SetAutonomyDeps(db.SQL(), nil, nil)
	r := chi.NewRouter()
	r.Post("/tasks", h.Create)
	return &multiEnv{r: r, q: q, sql: db.SQL(), wfID: wfID, repoA: mk("alpha"), repoB: mk("beta"), repoC: mk("gamma")}
}

func (e *multiEnv) post(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/tasks", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func (e *multiEnv) counts(t *testing.T) [3]int {
	t.Helper()
	var c [3]int
	for i, tbl := range []string{"tasks", "task_dependencies", "task_related_repos"} {
		if err := e.sql.QueryRow("SELECT COUNT(*) FROM " + tbl).Scan(&c[i]); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

type multiResp struct {
	ID             string   `json:"id"`
	RelatedRepoIDs []string `json:"related_repo_ids"`
	Children       []struct {
		ID       string `json:"id"`
		RepoID   string `json:"repo_id"`
		RepoName string `json:"repo_name"`
		Title    string `json:"title"`
		Label    string `json:"label"`
	} `json:"children"`
}

func decodeMulti(t *testing.T, w *httptest.ResponseRecorder) multiResp {
	t.Helper()
	var m multiResp
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateTask_RelatedReposOnly(t *testing.T) {
	e := setupMultiRepo(t)
	w := e.post(t, map[string]any{"title": "x", "repo_id": e.repoA, "related_repo_ids": []string{e.repoB, e.repoC}})
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m := decodeMulti(t, w)
	if len(m.RelatedRepoIDs) != 2 || len(m.Children) != 0 {
		t.Fatalf("resp %+v", m)
	}
	got, _ := e.q.ListTaskRelatedRepos(context.Background(), m.ID)
	if len(got) != 2 {
		t.Fatalf("stored %v", got)
	}
	if c := e.counts(t); c[0] != 1 || c[1] != 0 {
		t.Fatalf("counts %v", c)
	}
}

func TestCreateTask_ChildrenAndRelated(t *testing.T) {
	e := setupMultiRepo(t)
	w := e.post(t, map[string]any{
		"title": "x", "repo_id": e.repoA, "related_repo_ids": []string{e.repoC},
		"children": []map[string]any{
			{"repo_id": e.repoB, "title": "beta half", "description": "d"},
			{"repo_id": e.repoA, "title": "alpha half"},
		},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m := decodeMulti(t, w)
	if len(m.Children) != 2 || m.Children[0].RepoName != "beta" || m.Children[0].Title != "beta half" {
		t.Fatalf("children %+v", m.Children)
	}
	// Union of related + foreign children's repos (primary excluded).
	if got, _ := e.q.ListTaskRelatedRepos(context.Background(), m.ID); len(got) != 2 {
		t.Fatalf("stored %v", got)
	}
	parent, _ := e.q.GetTask(context.Background(), m.ID)
	for _, c := range m.Children {
		child, err := e.q.GetTask(context.Background(), c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if child.ParentTaskID == nil || *child.ParentTaskID != parent.ID || child.WorkflowID != parent.WorkflowID || child.CreatedByRunID != nil {
			t.Fatalf("child %+v", child)
		}
		if c.Label != child.Label {
			t.Fatalf("label mismatch")
		}
		labels, _ := q2labels(e, parent.WorkflowID)
		if !labels[child.Label] {
			t.Fatalf("child label %q is not agent_ignore", child.Label)
		}
	}
	if c := e.counts(t); c[0] != 3 || c[1] != 2 {
		t.Fatalf("counts %v", c)
	}
}

func q2labels(e *multiEnv, wf string) (map[string]bool, error) {
	ls, err := e.q.ListWorkflowLabels(context.Background(), wf)
	out := map[string]bool{}
	for _, l := range ls {
		out[l.Name] = l.AgentIgnore != 0
	}
	return out, err
}

func TestCreateTask_Multipart(t *testing.T) {
	e := setupMultiRepo(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("title", "x")
	_ = mw.WriteField("repo_id", e.repoA)
	_ = mw.WriteField("related_repo_ids", e.repoB)
	_ = mw.WriteField("related_repo_ids", e.repoC)
	kids, _ := json.Marshal([]map[string]string{{"repo_id": e.repoB, "title": "b"}})
	_ = mw.WriteField("children", string(kids))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/tasks", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m := decodeMulti(t, w)
	if len(m.Children) != 1 || len(m.RelatedRepoIDs) != 2 {
		t.Fatalf("resp %+v", m)
	}
}

func TestCreateTask_MultiRepoValidationRollsBack(t *testing.T) {
	e := setupMultiRepo(t)
	many := make([]map[string]any, 11)
	for i := range many {
		many[i] = map[string]any{"repo_id": e.repoA, "title": "c"}
	}
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown related", map[string]any{"related_repo_ids": []string{"nope"}}, 400},
		{"unknown child repo", map[string]any{"children": []map[string]any{{"repo_id": "nope", "title": "c"}}}, 400},
		{"duplicate related", map[string]any{"related_repo_ids": []string{e.repoB, e.repoB}}, 400},
		{"related equals primary", map[string]any{"related_repo_ids": []string{e.repoA}}, 400},
		{"empty related", map[string]any{"related_repo_ids": []string{""}}, 400},
		{"empty child title", map[string]any{"children": []map[string]any{{"repo_id": e.repoB, "title": " "}}}, 400},
		{"child without repo", map[string]any{"children": []map[string]any{{"title": "c"}}}, 400},
		{"nested children", map[string]any{"children": []map[string]any{{"repo_id": e.repoB, "title": "c", "children": []any{}}}}, 400},
		{"child parent_task_id", map[string]any{"children": []map[string]any{{"repo_id": e.repoB, "title": "c", "parent_task_id": "x"}}}, 400},
		{"too many children", map[string]any{"children": many}, 422},
	}
	for _, tc := range cases {
		before := e.counts(t)
		body := map[string]any{"title": "x", "repo_id": e.repoA}
		for k, v := range tc.body {
			body[k] = v
		}
		if w := e.post(t, body); w.Code != tc.want {
			t.Errorf("%s: got %d %s, want %d", tc.name, w.Code, w.Body, tc.want)
		}
		if after := e.counts(t); after != before {
			t.Errorf("%s: rows changed %v -> %v", tc.name, before, after)
		}
	}

	// A workflow without an agent_ignore label can't host children.
	if _, err := e.sql.Exec("UPDATE workflow_labels SET agent_ignore = 0 WHERE workflow_id = ?", e.wfID); err != nil {
		t.Fatal(err)
	}
	before := e.counts(t)
	w := e.post(t, map[string]any{"title": "x", "repo_id": e.repoA, "children": []map[string]any{{"repo_id": e.repoB, "title": "c"}}})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "agent_ignore") {
		t.Errorf("no gate label: %d %s", w.Code, w.Body)
	}
	if after := e.counts(t); after != before {
		t.Errorf("no gate label: rows changed")
	}
}

func TestCreateTask_BackCompatResponse(t *testing.T) {
	e := setupMultiRepo(t)
	w := e.post(t, map[string]any{"title": "x", "repo_id": e.repoA})
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	if _, ok := raw["children"]; ok {
		t.Error("children key present")
	}
	if _, ok := raw["related_repo_ids"]; ok {
		t.Error("related_repo_ids key present")
	}
	if c := e.counts(t); c[2] != 0 {
		t.Errorf("related rows: %v", c)
	}
}
