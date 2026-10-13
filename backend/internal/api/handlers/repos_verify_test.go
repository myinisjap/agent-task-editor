package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestRepos_VerifyConfigRoundTrip covers defaults on create, setting, the
// unrelated-PATCH-preserves rule, clearing and validation.
func TestRepos_VerifyConfigRoundTrip(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil)
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)

	repoDir := filepath.Join(base, "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{"name": "myrepo", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)
	if repo["verify_command"] != "" || repo["verify_timeout_seconds"] != float64(600) {
		t.Errorf("defaults = %v / %v", repo["verify_command"], repo["verify_timeout_seconds"])
	}

	w = patchJSON(t, router, "/repos/"+id, map[string]any{"verify_command": "  go test ./...  ", "verify_timeout_seconds": 90})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	stored, _ := q.GetRepo(context.Background(), id)
	if stored.VerifyCommand != "go test ./..." || stored.VerifyTimeoutSeconds != 90 {
		t.Errorf("stored = %q / %d", stored.VerifyCommand, stored.VerifyTimeoutSeconds)
	}

	if w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"}); w.Code != http.StatusOK {
		t.Fatalf("unrelated patch: %d", w.Code)
	}
	stored, _ = q.GetRepo(context.Background(), id)
	if stored.VerifyCommand != "go test ./..." || stored.VerifyTimeoutSeconds != 90 {
		t.Errorf("unrelated PATCH changed verify config: %q / %d", stored.VerifyCommand, stored.VerifyTimeoutSeconds)
	}

	for name, body := range map[string]map[string]any{
		"timeout zero":     {"verify_timeout_seconds": 0},
		"timeout too big":  {"verify_timeout_seconds": 99999},
		"command too long": {"verify_command": strings.Repeat("x", 5000)},
	} {
		if w = patchJSON(t, router, "/repos/"+id, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, w.Code)
		}
	}

	if w = patchJSON(t, router, "/repos/"+id, map[string]any{"verify_command": ""}); w.Code != http.StatusOK {
		t.Fatalf("clear: %d", w.Code)
	}
	stored, _ = q.GetRepo(context.Background(), id)
	if stored.VerifyCommand != "" || stored.VerifyTimeoutSeconds != 90 {
		t.Errorf("after clear = %q / %d", stored.VerifyCommand, stored.VerifyTimeoutSeconds)
	}
}

func TestTasks_ListVerifications(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()
	task, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "work"})
	if err != nil {
		t.Fatal(err)
	}

	get := func() []map[string]any {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/verifications", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		var out []map[string]any
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := get(); got == nil || len(got) != 0 {
		t.Fatalf("empty task should return [], got %v", got)
	}

	code := int64(1)
	runID := uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertRunVerification(ctx, gen.UpsertRunVerificationParams{
		RunID: runID, TaskID: task.ID, Command: "make test", Status: "fail", ExitCode: &code, Output: "boom", Truncated: 1, DurationMs: 12,
	}); err != nil {
		t.Fatal(err)
	}
	got := get()
	if len(got) != 1 {
		t.Fatalf("got %d rows", len(got))
	}
	v := got[0]
	if v["run_id"] != runID || v["status"] != "fail" || v["exit_code"] != float64(1) || v["truncated"] != true || v["output"] != "boom" || v["duration_ms"] != float64(12) {
		t.Errorf("row = %v", v)
	}
}
