package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type fakeWaker struct{ n int }

func (f *fakeWaker) Wake() { f.n++ }

func TestTasks_WakeDispatcherOnCreateRerunAndUnpause(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}
	h := handlers.NewTasksHandler(q, workflow.New(db.SQL(), noopPub{}), t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil, nil)
	w := &fakeWaker{}
	h.SetDispatchWaker(w)
	r := chi.NewRouter()
	r.Post("/tasks", h.Create)
	r.Post("/tasks/{id}/rerun", h.Rerun)
	r.Patch("/tasks/{id}/pause", h.SetPaused)

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		var req *http.Request
		if body != nil {
			req = httptest.NewRequest(method, path, jsonBody(t, body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := do(http.MethodPost, "/tasks", map[string]string{"title": "t", "repo_id": repoID, "workflow_id": wfID})
	if rec.Code != http.StatusCreated || w.n != 1 {
		t.Fatalf("create: code=%d wakes=%d", rec.Code, w.n)
	}
	taskID, _ := seedRunningRun(t, q, wfID, repoID, "running")
	if rec := do(http.MethodPost, "/tasks/"+taskID+"/rerun", nil); rec.Code != http.StatusNoContent || w.n != 2 {
		t.Fatalf("rerun: code=%d wakes=%d", rec.Code, w.n)
	}
	if rec := do(http.MethodPatch, "/tasks/"+taskID+"/pause", map[string]bool{"paused": true}); rec.Code != http.StatusOK || w.n != 2 {
		t.Fatalf("pause must not wake: code=%d wakes=%d", rec.Code, w.n)
	}
	if rec := do(http.MethodPatch, "/tasks/"+taskID+"/pause", map[string]bool{"paused": false}); rec.Code != http.StatusOK || w.n != 3 {
		t.Fatalf("unpause: code=%d wakes=%d", rec.Code, w.n)
	}
}
