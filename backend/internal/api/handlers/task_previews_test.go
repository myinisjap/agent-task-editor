package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestRepos_PreviewConfig(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil)
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)
	repoDir := filepath.Join(base, "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name": "myrepo", "path": repoDir,
		"preview_command": "  npm run dev  ", "preview_routes": "/\r\n  /tasks \n\n/inbox", "preview_paths_glob": "web/**\n",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)
	if repo["preview_command"] != "npm run dev" || repo["preview_routes"] != "/\n/tasks\n/inbox" || repo["preview_paths_glob"] != "web/**" {
		t.Fatalf("create round-trip: %v", repo)
	}

	// Unrelated patch preserves.
	if w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"}); w.Code != http.StatusOK {
		t.Fatalf("patch: %d", w.Code)
	}
	stored, _ := q.GetRepo(context.Background(), id)
	if stored.PreviewCommand != "npm run dev" || stored.PreviewRoutes != "/\n/tasks\n/inbox" {
		t.Errorf("unrelated PATCH changed preview config: %+v", stored)
	}

	// Clear one field.
	if w = patchJSON(t, router, "/repos/"+id, map[string]any{"preview_routes": ""}); w.Code != http.StatusOK {
		t.Fatalf("clear: %d", w.Code)
	}
	stored, _ = q.GetRepo(context.Background(), id)
	if stored.PreviewRoutes != "" || stored.PreviewCommand != "npm run dev" {
		t.Errorf("after clear: %+v", stored)
	}

	for _, bad := range []map[string]any{
		{"preview_routes": "tasks"},
		{"preview_routes": "http://evil.example/x"},
		{"preview_routes": "/a b"},
		{"preview_command": "x\x00y"},
	} {
		if w = patchJSON(t, router, "/repos/"+id, bad); w.Code != http.StatusBadRequest {
			t.Errorf("%v: want 400, got %d", bad, w.Code)
		}
	}
}

func TestTasks_ListPreviews(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()
	task, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "work"})
	if err != nil {
		t.Fatal(err)
	}
	get := func() map[string]any {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/previews", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		var out map[string]any
		_ = json.NewDecoder(w.Body).Decode(&out)
		return out
	}
	if got := get(); got["capture"] != nil {
		t.Fatalf("want null capture, got %v", got)
	}

	c, err := q.CreatePreviewCapture(ctx, gen.CreatePreviewCaptureParams{ID: uuid.NewString(), TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := get()["capture"].(map[string]any); got["status"] != "running" {
		t.Errorf("running: %v", got)
	}
	if err := q.CreatePreviewImage(ctx, gen.CreatePreviewImageParams{
		ID: uuid.NewString(), CaptureID: c.ID, TaskID: task.ID, Route: "/", Variant: "after", Width: 1280, Height: 800, Path: task.ID + "/preview-x.png",
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.FinishPreviewCapture(ctx, gen.FinishPreviewCaptureParams{Status: "ok", ID: c.ID}); err != nil {
		t.Fatal(err)
	}
	got := get()["capture"].(map[string]any)
	imgs := got["images"].([]any)
	if got["status"] != "ok" || len(imgs) != 1 {
		t.Fatalf("ok capture: %v", got)
	}
	if u := imgs[0].(map[string]any)["url"]; u != "/api/v1/uploads/"+task.ID+"/preview-x.png" {
		t.Errorf("url = %v", u)
	}

	c2, _ := q.CreatePreviewCapture(ctx, gen.CreatePreviewCaptureParams{ID: uuid.NewString(), TaskID: task.ID})
	_ = q.FinishPreviewCapture(ctx, gen.FinishPreviewCaptureParams{Status: "failed", Error: "boom", ID: c2.ID})
	got = get()["capture"].(map[string]any)
	if got["status"] != "failed" || got["error"] != "boom" || len(got["images"].([]any)) != 0 {
		t.Errorf("failed capture: %v", got)
	}
}
