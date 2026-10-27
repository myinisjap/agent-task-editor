package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestListHumanDecisions(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)
	ctx := context.Background()
	taskID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "not_ready"}); err != nil {
		t.Fatal(err)
	}
	get := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/"+id+"/human-decisions", nil))
		return w
	}

	// Empty -> [] not null.
	w := get(taskID)
	if w.Code != 200 || w.Body.String() != "[]\n" && w.Body.String() != "[]" {
		t.Fatalf("empty: %d %q", w.Code, w.Body.String())
	}

	r1, r2 := "use tabs", "prefer small PRs"
	for i, rep := range []*string{&r1, &r2} {
		if err := q.RecordHumanReply(ctx, gen.RecordHumanReplyParams{
			RunID: uuid.NewString(), TaskID: taskID, Label: "work", EscalationReason: "ask", Question: "q", Reply: rep,
		}); err != nil {
			t.Fatalf("reply %d: %v", i, err)
		}
	}
	if err := q.UpsertHumanQuestion(ctx, gen.UpsertHumanQuestionParams{RunID: uuid.NewString(), TaskID: taskID, EscalationReason: "ask", Question: "unanswered"}); err != nil {
		t.Fatal(err)
	}

	w = get(taskID)
	if w.Code != 200 {
		t.Fatalf("got %d", w.Code)
	}
	var rows []gen.ListTaskHumanDecisionsRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Reply == nil || r.Question != "q" || r.Label != "work" {
			t.Errorf("bad row %+v", r)
		}
	}

	if w := get("nope"); w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
