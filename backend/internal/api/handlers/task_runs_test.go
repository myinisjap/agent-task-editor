package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestTasks_GetRun_OK verifies GetRun returns the requested run when it
// belongs to the task named in the URL.
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

// TestTasks_GetRun_MismatchedTask verifies GetRun 404s when the run exists
// but belongs to a different task than the one named in the URL, matching
// CancelRun/ReplyRun's existing behavior rather than returning the other
// task's run.
func TestTasks_GetRun_MismatchedTask(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)
	_, runID := seedRunningRun(t, q, wfID, repoID, "running")
	otherTaskID, _ := seedRunningRun(t, q, wfID, repoID, "running")

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+otherTaskID+"/runs/"+runID, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for run belonging to a different task, got %d: %s", w.Code, w.Body)
	}
}

// TestTasks_GetRunLogs_MismatchedTask verifies GetRunLogs 404s when the run
// exists but belongs to a different task than the one named in the URL,
// instead of paging back another task's log transcript.
func TestTasks_GetRunLogs_MismatchedTask(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)
	_, runID := seedRunningRun(t, q, wfID, repoID, "running")
	otherTaskID, _ := seedRunningRun(t, q, wfID, repoID, "running")

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+otherTaskID+"/runs/"+runID+"/logs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for logs of a run belonging to a different task, got %d: %s", w.Code, w.Body)
	}
}

// TestTasks_GetRunLogs_UnknownTask verifies GetRunLogs 404s when the task
// id in the URL doesn't exist at all, rather than paging agent_logs purely
// off the run id and ignoring the path's task id.
func TestTasks_GetRunLogs_UnknownTask(t *testing.T) {
	router, q, wfID, repoID := setupTaskRouter(t)
	_, runID := seedRunningRun(t, q, wfID, repoID, "running")

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+uuid.NewString()+"/runs/"+runID+"/logs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a nonexistent task id, got %d: %s", w.Code, w.Body)
	}
}
