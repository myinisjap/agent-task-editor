package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// setupPRURLRouter wires just the PRURL route.
func setupPRURLRouter(t *testing.T) (http.Handler, *gen.Queries, string, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), noopPub{})

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID: repoID, Name: "test-repo", Path: t.TempDir(), WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	h := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil)
	r := chi.NewRouter()
	r.Get("/tasks/{id}/pr-url", h.PRURL)
	return r, q, wfID, repoID
}

func TestTasks_PRURL_OK(t *testing.T) {
	router, q, wfID, repoID := setupPRURLRouter(t)
	remote := "https://github.com/acme/widgets"
	if _, err := q.UpdateRepo(context.Background(), gen.UpdateRepoParams{
		ID: repoID, Name: "test-repo", Path: t.TempDir(), RemoteUrl: &remote, WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("update repo: %v", err)
	}

	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Fix the bug", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := q.SetTaskWorktree(context.Background(), gen.SetTaskWorktreeParams{
		Branch: "my-branch", WorktreePath: "", BaseRef: "main", ID: task.ID,
	}); err != nil {
		t.Fatalf("set worktree: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/pr-url", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantPrefix := "https://github.com/acme/widgets/compare/main...my-branch?"
	if len(got.URL) < len(wantPrefix) || got.URL[:len(wantPrefix)] != wantPrefix {
		t.Errorf("expected URL to start with %q, got %q", wantPrefix, got.URL)
	}
}

func TestTasks_PRURL_UnknownTask(t *testing.T) {
	router, _, _, _ := setupPRURLRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+uuid.NewString()+"/pr-url", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestTasks_PRURL_NoBranch(t *testing.T) {
	router, q, wfID, repoID := setupPRURLRouter(t)
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "No branch yet", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/pr-url", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestTasks_PRURL_NoRemoteURL(t *testing.T) {
	router, q, wfID, repoID := setupPRURLRouter(t)
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := q.SetTaskWorktree(context.Background(), gen.SetTaskWorktreeParams{
		Branch: "my-branch", WorktreePath: "", BaseRef: "main", ID: task.ID,
	}); err != nil {
		t.Fatalf("set worktree: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/pr-url", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

func TestTasks_PRURL_NonGitHubRemote(t *testing.T) {
	router, q, wfID, repoID := setupPRURLRouter(t)
	remote := "https://gitlab.com/acme/widgets"
	if _, err := q.UpdateRepo(context.Background(), gen.UpdateRepoParams{
		ID: repoID, Name: "test-repo", Path: t.TempDir(), RemoteUrl: &remote, WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("update repo: %v", err)
	}
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := q.SetTaskWorktree(context.Background(), gen.SetTaskWorktreeParams{
		Branch: "my-branch", WorktreePath: "", BaseRef: "main", ID: task.ID,
	}); err != nil {
		t.Fatalf("set worktree: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/pr-url", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

// dirExists (unexported) is covered by TestDirExists in
// task_pr_internal_test.go (package handlers). Diff below drives real git
// commands against a local, no-network repo. GitHubStatus/refreshPRMergeable/
// CreatePR additionally shell out to the gh CLI and aren't covered here;
// PRURL only exercises paths where the git calls are best-effort (a missing/
// invalid worktree falls back to the repo path, and a failing `git log` just
// yields no commit list rather than erroring the request).

// TestTasks_Diff_UnknownTask verifies Diff 404s for an unknown task id.
func TestTasks_Diff_UnknownTask(t *testing.T) {
	router, _, _, _ := setupTaskRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+uuid.NewString()+"/diff", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body)
	}
}

// TestTasks_Diff_NoBranch verifies Diff returns an empty diff (200, not an
// error) for a task that never provisioned a branch.
func TestTasks_Diff_NoBranch(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "No branch yet", WorkflowID: wfID, RepoID: repoID, Label: "not_ready",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/diff", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Branch string `json:"branch"`
		Diff   string `json:"diff"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Branch != "" || resp.Diff != "" {
		t.Errorf("expected empty branch/diff, got %+v", resp)
	}
}

// TestTasks_Diff_InvalidBaseRef verifies Diff rejects an unsafe/invalid
// base_ref or branch (e.g. one containing shell metacharacters) with 400
// rather than passing it to git.
func TestTasks_Diff_InvalidBaseRef(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Bad ref", WorkflowID: wfID, RepoID: repoID, Label: "not_ready",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := q.SetTaskWorktree(context.Background(), gen.SetTaskWorktreeParams{
		Branch: "feature/x", WorktreePath: t.TempDir(), BaseRef: "--evil-flag", ID: task.ID,
	}); err != nil {
		t.Fatalf("set worktree: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/diff", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body)
	}
}

// TestTasks_Diff_ComputesRealDiff drives Diff end-to-end against a real local
// git repo: a base commit, a branch with one additional commit, and no
// network access. Verifies the returned diff contains the new content.
func TestTasks_Diff_ComputesRealDiff(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)

	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	if err := os.WriteFile(repoDir+"/base.txt", []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "base commit")
	runGit("checkout", "-b", "feature/diff-test")
	if err := os.WriteFile(repoDir+"/new.txt", []byte("hello from the branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "add new file")
	runGit("checkout", "main")

	if _, err := q.UpdateRepo(context.Background(), gen.UpdateRepoParams{
		ID: repoID, Name: "test-repo", Path: repoDir, WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("update repo path: %v", err)
	}

	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "Has a real diff", WorkflowID: wfID, RepoID: repoID, Label: "not_ready",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	// No worktree_path set (or a non-existent one) so Diff falls back to the
	// repo's main clone, which has both branches.
	if err := q.SetTaskWorktree(context.Background(), gen.SetTaskWorktreeParams{
		Branch: "feature/diff-test", WorktreePath: "", BaseRef: "main", ID: task.ID,
	}); err != nil {
		t.Fatalf("set worktree: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/diff", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Branch string `json:"branch"`
		Diff   string `json:"diff"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Branch != "feature/diff-test" {
		t.Errorf("branch = %q, want feature/diff-test", resp.Branch)
	}
	if !strings.Contains(resp.Diff, "hello from the branch") {
		t.Errorf("expected diff to contain the new file's content, got %q", resp.Diff)
	}
}
