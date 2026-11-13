package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

type fakeSteerCanceller struct {
	fakeCanceller
	steered []agent.SteerRequest
	ok      bool
}

func (c *fakeSteerCanceller) Steer(runID string, req agent.SteerRequest) bool {
	c.steered = append(c.steered, req)
	return c.ok
}

func steerReq(taskID, runID, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/runs/"+runID+"/steer", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestTasks_SteerRun_OK(t *testing.T) {
	c := &fakeSteerCanceller{fakeCanceller: fakeCanceller{found: map[string]bool{}}, ok: true}
	r, q, wfID, repoID := setupCancelRouter(t, c)
	taskID, runID := seedRunningRun(t, q, wfID, repoID, "running")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, steerReq(taskID, runID, `{"message":" use B "}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}
	if len(c.steered) != 1 || c.steered[0].Message != "use B" {
		t.Errorf("unexpected steer calls: %+v", c.steered)
	}
}

func TestTasks_SteerRun_NotRunning(t *testing.T) {
	for _, status := range []string{"waiting_human", "completed", "interrupted"} {
		c := &fakeSteerCanceller{fakeCanceller: fakeCanceller{found: map[string]bool{}}, ok: true}
		r, q, wfID, repoID := setupCancelRouter(t, c)
		taskID, runID := seedRunningRun(t, q, wfID, repoID, status)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, steerReq(taskID, runID, `{"message":"x"}`))
		if w.Code != http.StatusConflict {
			t.Fatalf("%s: expected 409, got %d", status, w.Code)
		}
		if len(c.steered) != 0 {
			t.Errorf("%s: Steer must not be called", status)
		}
	}
}

func TestTasks_SteerRun_NoLongerActiveAndValidation(t *testing.T) {
	c := &fakeSteerCanceller{fakeCanceller: fakeCanceller{found: map[string]bool{}}, ok: false}
	r, q, wfID, repoID := setupCancelRouter(t, c)
	taskID, runID := seedRunningRun(t, q, wfID, repoID, "running")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, steerReq(taskID, runID, `{"message":"x"}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, steerReq(taskID, runID, `{"message":"  "}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
