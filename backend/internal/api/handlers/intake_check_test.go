package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/intakecheck"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func TestCreateReturnsPossibleDuplicatesWithoutBlocking(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), noopPub{})
	wfs, _ := q.ListWorkflows(t.Context())
	wfID := wfs[0].ID
	repoID := "repo-1"
	if _, err := q.CreateRepo(t.Context(), gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}
	existing := mkTask(t, q, wfID, repoID, "Login page crashes on submit", "not_ready")
	h := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil, nil)
	svc := intakecheck.New(db.SQL(), q, nil)
	h.SetIntake(svc)
	ih := handlers.NewIntakeCheckHandler(svc)
	r := chi.NewRouter()
	r.Post("/tasks", h.Create)
	r.Get("/tasks/similar", ih.Similar)
	r.Post("/tasks/{id}/intake-check", ih.Start)

	req := httptest.NewRequest(http.MethodPost, "/tasks", jsonBody(t, map[string]any{"title": "Login page crashes when submitting", "repo_id": repoID}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create blocked: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		ID                 string `json:"id"`
		PossibleDuplicates []struct {
			ID string `json:"id"`
		} `json:"possible_duplicates"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.PossibleDuplicates) != 1 || got.PossibleDuplicates[0].ID != existing.ID {
		t.Fatalf("possible_duplicates = %+v", got.PossibleDuplicates)
	}

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/similar?repo_id="+repoID+"&q=login+page+crashes+submit", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("similar: %d", w.Code)
	}

	// Assistant is off by default.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/tasks/"+got.ID+"/intake-check", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("intake-check while disabled: %d", w.Code)
	}
}
