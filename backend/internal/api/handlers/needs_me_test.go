package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestNeedsMeList(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()
	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("list workflows: %v", err)
	}
	wfID := wfs[0].ID
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}
	mk := func(title, label string) {
		if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: title, WorkflowID: wfID, RepoID: repoID, Label: label}); err != nil {
			t.Fatal(err)
		}
	}
	mk("gate", "review-plan")
	mk("backlog", "not_ready")
	mk("busy", "work")

	h := handlers.NewNeedsMeHandler(q)
	w := httptest.NewRecorder()
	h.List(w, httptest.NewRequest(http.MethodGet, "/needs-me", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct {
			TaskTitle string `json:"task_title"`
			Reason    string `json:"reason"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].TaskTitle != "gate" || body.Items[0].Reason != "human_gate" {
		t.Fatalf("unexpected items: %+v", body.Items)
	}
}
