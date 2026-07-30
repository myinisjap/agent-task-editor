package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestTasks_GetRun_OK verifies GetRun returns the requested run regardless of
// which task it's nested under in the URL — it looks the run up by run_id
// alone (see TasksHandler.GetRun), unlike CancelRun which does check the
// run belongs to the URL's task id.
func TestTasks_GetRun_OK(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)
	taskID, runID := seedRunningRun(t, q, wfID, repoID, "running")

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/runs/"+runID, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body)
	}
	var got gen.AgentRun
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != runID || got.TaskID != taskID {
		t.Errorf("expected run %s for task %s, got %+v", runID, taskID, got)
	}
}

func TestTasks_GetRun_UnknownRun(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)
	taskID, _ := seedRunningRun(t, q, wfID, repoID, "running")

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/runs/"+uuid.NewString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
