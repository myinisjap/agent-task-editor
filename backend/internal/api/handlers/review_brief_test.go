package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// briefRepo builds a git repo whose branch "feat" adds backend/migrations/001.sql
// and deletes foo_test.go relative to main.
func briefRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "T")
	if err := os.WriteFile(dir+"/foo_test.go", []byte("package foo\n\nfunc TestFoo(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")
	git("checkout", "-b", "feat")
	git("rm", "foo_test.go")
	if err := os.MkdirAll(dir+"/backend/migrations", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/backend/migrations/001.sql", []byte("CREATE TABLE x (a INT);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "work")
	return dir
}

type briefEnv struct {
	q      *gen.Queries
	wfID   string
	repoID string
}

func newBriefEnv(t *testing.T) briefEnv {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatal("no workflows")
	}
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfs[0].ID}); err != nil {
		t.Fatal(err)
	}
	return briefEnv{q: q, wfID: wfs[0].ID, repoID: repoID}
}

func (e briefEnv) task(t *testing.T, label, worktree string) gen.Task {
	t.Helper()
	ctx := context.Background()
	task, err := e.q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "T", WorkflowID: e.wfID, RepoID: e.repoID, Label: label})
	if err != nil {
		t.Fatal(err)
	}
	if worktree != "" {
		if err := e.q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: "feat", WorktreePath: worktree, BaseRef: "main", ID: task.ID}); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func getBrief(t *testing.T, q *gen.Queries, id string) (int, map[string]any) {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/tasks/{id}/review-brief", handlers.NewReviewBriefHandler(q, reviewbrief.NewCache()).Get)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/"+id+"/review-brief", nil))
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestReviewBrief_NotFound(t *testing.T) {
	e := newBriefEnv(t)
	if code, _ := getBrief(t, e.q, "nope"); code != http.StatusNotFound {
		t.Fatalf("code %d", code)
	}
}

func TestReviewBrief_NoBranch(t *testing.T) {
	e := newBriefEnv(t)
	task := e.task(t, "review-plan", "")
	ctx := context.Background()
	runID := uuid.NewString()
	if _, err := e.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	info := "Changed: stuff"
	if _, err := e.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{Status: "failed", StoredInfo: &info, CostUsd: 0.5, ID: runID}); err != nil {
		t.Fatal(err)
	}
	code := int64(1)
	if err := e.q.UpsertRunVerification(ctx, gen.UpsertRunVerificationParams{RunID: runID, TaskID: task.ID, Command: "make test", Status: "fail", ExitCode: &code, Output: "a\nb\nboom", DurationMs: 42}); err != nil {
		t.Fatal(err)
	}
	status, b := getBrief(t, e.q, task.ID)
	if status != http.StatusOK {
		t.Fatalf("code %d", status)
	}
	if b["diff_available"] != false || b["human_gate"] != true {
		t.Fatalf("brief = %v", b)
	}
	if fl, ok := b["risk_flags"].([]any); !ok || len(fl) != 0 {
		t.Fatalf("risk_flags = %v", b["risk_flags"])
	}
	runs := b["runs"].(map[string]any)
	if runs["total"] != float64(1) || runs["failed"] != float64(1) || runs["cost_usd"] != 0.5 {
		t.Fatalf("runs = %v", runs)
	}
	v := b["verification"].(map[string]any)
	if v["status"] != "fail" || v["output_tail"] != "a\nb\nboom" || v["duration_ms"] != float64(42) {
		t.Fatalf("verification = %v", v)
	}
	as := b["agent_summary"].(map[string]any)
	if as["source"] != "stored_info" || as["text"] != info || as["run_id"] != runID {
		t.Fatalf("agent_summary = %v", as)
	}
}

func TestReviewBrief_RealDiffFlags(t *testing.T) {
	e := newBriefEnv(t)
	task := e.task(t, "review-plan", briefRepo(t))
	status, b := getBrief(t, e.q, task.ID)
	if status != http.StatusOK || b["diff_available"] != true {
		t.Fatalf("status %d brief %v", status, b)
	}
	got := map[string]bool{}
	for _, f := range b["risk_flags"].([]any) {
		got[f.(map[string]any)["code"].(string)] = true
	}
	if !got["migration"] || !got["tests_removed"] || len(got) != 2 {
		t.Fatalf("flags = %v", got)
	}
	if b["changed"].(map[string]any)["files"] != float64(2) {
		t.Fatalf("changed = %v", b["changed"])
	}
}

func TestNeedsMe_RiskFlags(t *testing.T) {
	e := newBriefEnv(t)
	gate := e.task(t, "review-plan", briefRepo(t))
	busy := e.task(t, "work", "")

	h := handlers.NewNeedsMeHandler(e.q)
	h.SetBriefCache(reviewbrief.NewCache())
	w := httptest.NewRecorder()
	h.List(w, httptest.NewRequest(http.MethodGet, "/needs-me", nil))
	var body struct {
		Items []struct {
			TaskID    string   `json:"task_id"`
			RiskFlags []string `json:"risk_flags"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].TaskID != gate.ID {
		t.Fatalf("items = %+v (busy=%s)", body.Items, busy.ID)
	}
	if len(body.Items[0].RiskFlags) != 2 || body.Items[0].RiskFlags[0] != "migration" {
		t.Fatalf("risk_flags = %v", body.Items[0].RiskFlags)
	}
}
