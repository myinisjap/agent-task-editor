package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type apiLesson struct {
	ID            string            `json:"id"`
	RepoID        *string           `json:"repo_id"`
	Kind          string            `json:"kind"`
	Body          string            `json:"body"`
	PathGlobs     []string          `json:"path_globs"`
	Stages        []string          `json:"stages"`
	Citations     []json.RawMessage `json:"citations"`
	Source        string            `json:"source"`
	Status        string            `json:"status"`
	InjectedCount int               `json:"injected_count"`
}

func setupLessons(t *testing.T) (http.Handler, *gen.Queries, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewLessonsHandler(q)
	r := chi.NewRouter()
	r.Get("/repos/{id}/lessons", h.ListForRepo)
	r.Post("/repos/{id}/lessons", h.CreateForRepo)
	r.Get("/lessons", h.ListGlobal)
	r.Post("/lessons", h.CreateGlobal)
	r.Patch("/lessons/{id}", h.Update)
	r.Delete("/lessons/{id}", h.Delete)

	wf := "wf"
	if _, err := q.CreateWorkflow(t.Context(), gen.CreateWorkflowParams{ID: wf, Name: "wf", Description: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateRepo(t.Context(), gen.CreateRepoParams{ID: "r1", Name: "r1", Path: "/tmp/r1", WorkflowID: &wf}); err != nil {
		t.Fatal(err)
	}
	return r, q, "r1"
}

func lessonReq(r http.Handler, method, url string, body any) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, url, rdr)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func mustLesson(t *testing.T, w *httptest.ResponseRecorder, code int) apiLesson {
	t.Helper()
	if w.Code != code {
		t.Fatalf("want %d got %d: %s", code, w.Code, w.Body)
	}
	var l apiLesson
	if err := json.Unmarshal(w.Body.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func listLessons(t *testing.T, r http.Handler, url string) []apiLesson {
	t.Helper()
	w := lessonReq(r, http.MethodGet, url, nil)
	if w.Code != 200 {
		t.Fatalf("list %s: %d %s", url, w.Code, w.Body)
	}
	var out []apiLesson
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLessons_ScopeAndRoundTrip(t *testing.T) {
	r, _, repo := setupLessons(t)
	if got := listLessons(t, r, "/lessons"); got == nil || len(got) != 0 {
		t.Fatalf("empty list should be [], got %v", got)
	}
	rl := mustLesson(t, lessonReq(r, "POST", "/repos/"+repo+"/lessons", map[string]any{
		"kind": "trap", "body": "  use make test  ",
		"path_globs": []string{" backend/** ", "backend/**", "*.sql"},
		"stages":     []string{"work", " review ", "work"},
	}), 201)
	if rl.RepoID == nil || *rl.RepoID != repo || rl.Body != "use make test" || rl.Status != "active" || rl.Source != "human" {
		t.Fatalf("bad create: %+v", rl)
	}
	if strings.Join(rl.PathGlobs, "|") != "backend/**|*.sql" || strings.Join(rl.Stages, "|") != "work|review" {
		t.Fatalf("bad normalization: %+v", rl)
	}
	gl := mustLesson(t, lessonReq(r, "POST", "/lessons", map[string]any{"kind": "convention", "body": "global"}), 201)
	if gl.RepoID != nil {
		t.Fatal("global must have null repo_id")
	}
	rs := listLessons(t, r, "/repos/"+repo+"/lessons")
	if len(rs) != 1 || rs[0].ID != rl.ID {
		t.Fatalf("repo list: %+v", rs)
	}
	gs := listLessons(t, r, "/lessons")
	if len(gs) != 1 || gs[0].ID != gl.ID {
		t.Fatalf("global list: %+v", gs)
	}
	if got := listLessons(t, r, "/repos/"+repo+"/lessons?status=retired"); len(got) != 0 {
		t.Fatalf("status filter: %+v", got)
	}
	if w := lessonReq(r, "GET", "/lessons?status=bogus", nil); w.Code != 400 {
		t.Fatalf("bogus status filter: %d", w.Code)
	}
}

func TestLessons_Validation(t *testing.T) {
	r, _, repo := setupLessons(t)
	url := "/repos/" + repo + "/lessons"
	bad := []map[string]any{
		{"kind": "nope", "body": "x"},
		{"kind": "trap", "body": "x", "status": "nope"},
		{"kind": "trap", "body": "x", "source": "nope"},
		{"kind": "trap", "body": "   "},
		{"kind": "trap", "body": strings.Repeat("a", 301)},
		{"kind": "trap", "body": "x", "citations": map[string]any{"a": 1}},
		{"kind": "trap", "body": "x", "citations": "str"},
		{"kind": "trap", "body": "x", "stages": []string{"a,b"}},
		{"body": "x"},
	}
	for i, b := range bad {
		if w := lessonReq(r, "POST", url, b); w.Code != 400 {
			t.Errorf("case %d: want 400 got %d", i, w.Code)
		}
	}
	mustLesson(t, lessonReq(r, "POST", url, map[string]any{"kind": "trap", "body": strings.Repeat("é", 300)}), 201)
	l := mustLesson(t, lessonReq(r, "POST", url, map[string]any{"kind": "trap", "body": "x", "citations": []any{map[string]any{"path": "a.go"}}}), 201)
	if len(l.Citations) != 1 {
		t.Fatalf("citations: %+v", l.Citations)
	}
}

func TestLessons_NotFound(t *testing.T) {
	r, _, _ := setupLessons(t)
	if w := lessonReq(r, "GET", "/repos/nope/lessons", nil); w.Code != 404 {
		t.Errorf("get unknown repo: %d", w.Code)
	}
	if w := lessonReq(r, "POST", "/repos/nope/lessons", map[string]any{"kind": "trap", "body": "x"}); w.Code != 404 {
		t.Errorf("post unknown repo: %d", w.Code)
	}
	if w := lessonReq(r, "PATCH", "/lessons/nope", map[string]any{"body": "x"}); w.Code != 404 {
		t.Errorf("patch missing: %d", w.Code)
	}
	if w := lessonReq(r, "DELETE", "/lessons/nope", nil); w.Code != 404 {
		t.Errorf("delete missing: %d", w.Code)
	}
}

func TestLessons_PatchMergeAndDelete(t *testing.T) {
	r, _, repo := setupLessons(t)
	l := mustLesson(t, lessonReq(r, "POST", "/repos/"+repo+"/lessons", map[string]any{
		"kind": "trap", "body": "orig", "stages": []string{"work"}, "path_globs": []string{"a/**"},
	}), 201)
	p := mustLesson(t, lessonReq(r, "PATCH", "/lessons/"+l.ID, map[string]any{"status": "retired"}), 200)
	if p.Status != "retired" || p.Body != "orig" || p.Kind != "trap" ||
		len(p.Stages) != 1 || len(p.PathGlobs) != 1 || p.RepoID == nil {
		t.Fatalf("merge lost fields: %+v", p)
	}
	if w := lessonReq(r, "PATCH", "/lessons/"+l.ID, map[string]any{"body": strings.Repeat("a", 301)}); w.Code != 400 {
		t.Fatalf("patch long body: %d", w.Code)
	}
	p = mustLesson(t, lessonReq(r, "PATCH", "/lessons/"+l.ID, map[string]any{"stages": []string{}}), 200)
	if len(p.Stages) != 0 {
		t.Fatalf("stages not cleared: %+v", p)
	}
	if w := lessonReq(r, "DELETE", "/lessons/"+l.ID, nil); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := lessonReq(r, "DELETE", "/lessons/"+l.ID, nil); w.Code != 404 {
		t.Fatalf("second delete: %d", w.Code)
	}
}

func TestLessons_RepoDeleteCascades(t *testing.T) {
	r, q, repo := setupLessons(t)
	l := mustLesson(t, lessonReq(r, "POST", "/repos/"+repo+"/lessons", map[string]any{"kind": "trap", "body": "x"}), 201)
	if err := q.DeleteRepo(t.Context(), repo); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetRepoLesson(t.Context(), l.ID); err == nil {
		t.Fatal("lesson should be cascade-deleted")
	}
}
