package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/notify"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func setupTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "notify-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func defaultWorkflowID(t *testing.T, q *gen.Queries) string {
	t.Helper()
	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatal("no workflow found after seed")
	}
	return wfs[0].ID
}

func createTestTask(t *testing.T, q *gen.Queries, label, workflowID string) gen.Task {
	t.Helper()
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:         repoID,
		Name:       "test-repo",
		Path:       "/tmp/notify-test-repo-" + repoID,
		WorkflowID: &workflowID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Notify me",
		WorkflowID: workflowID,
		RepoID:     repoID,
		Label:      label,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// runNotifier starts n.Run in the background and returns a cancel func that
// stops it and waits briefly for the goroutine to exit.
func runNotifier(t *testing.T, n *notify.Notifier) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		n.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	return cancel
}

func TestNotifier_NeedsHuman_DeliversOnePOSTWithExpectedBody(t *testing.T) {
	db := setupTestDB(t)
	q := gen.New(db.SQL())
	wfID := defaultWorkflowID(t, q)
	task := createTestTask(t, q, "work", wfID)

	var received atomic.Int32
	var lastBody atomic.Pointer[map[string]any]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make(map[string]any)
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastBody.Store(&body)
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.New(srv.URL, "http://example.com", time.Hour, q)
	runNotifier(t, n)

	n.Publish("task.needs_human", map[string]any{
		"task_id": task.ID,
		"run_id":  "run-1",
		"message": "please look at this",
	})

	waitFor(t, func() bool { return received.Load() == 1 })

	body := lastBody.Load()
	if (*body)["event"] != "task.needs_human" {
		t.Errorf("event = %v, want task.needs_human", (*body)["event"])
	}
	if (*body)["reason"] != "needs_human" {
		t.Errorf("reason = %v, want needs_human", (*body)["reason"])
	}
	if (*body)["task_id"] != task.ID {
		t.Errorf("task_id = %v, want %v", (*body)["task_id"], task.ID)
	}
	if (*body)["task_title"] != "Notify me" {
		t.Errorf("task_title = %v, want %q", (*body)["task_title"], "Notify me")
	}
	if (*body)["url"] != "http://example.com/tasks/"+task.ID {
		t.Errorf("url = %v, want deep link", (*body)["url"])
	}
	if (*body)["message"] != "please look at this" {
		t.Errorf("message = %v", (*body)["message"])
	}
}

func TestNotifier_Debounce_SuppressesDuplicateWithinWindow(t *testing.T) {
	db := setupTestDB(t)
	q := gen.New(db.SQL())
	wfID := defaultWorkflowID(t, q)
	task := createTestTask(t, q, "work", wfID)

	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.New(srv.URL, "", time.Hour, q)
	runNotifier(t, n)

	n.Publish("task.needs_human", map[string]any{"task_id": task.ID, "message": "one"})
	waitFor(t, func() bool { return received.Load() == 1 })

	n.Publish("task.needs_human", map[string]any{"task_id": task.ID, "message": "two"})
	// Give it a moment to (not) process; then assert it never went above 1.
	time.Sleep(200 * time.Millisecond)
	if got := received.Load(); got != 1 {
		t.Errorf("expected duplicate within debounce window to be suppressed, got %d deliveries", got)
	}
}

func TestNotifier_Retry_500Retried_400NotRetried(t *testing.T) {
	db := setupTestDB(t)
	q := gen.New(db.SQL())
	wfID := defaultWorkflowID(t, q)

	t.Run("500 is retried", func(t *testing.T) {
		task := createTestTask(t, q, "work", wfID)
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n := attempts.Add(1)
			if n < 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		n := notify.New(srv.URL, "", time.Hour, q)
		runNotifier(t, n)
		n.Publish("task.needs_human", map[string]any{"task_id": task.ID, "message": "retry me"})

		waitFor(t, func() bool { return attempts.Load() == 3 })
	})

	t.Run("400 is not retried", func(t *testing.T) {
		task := createTestTask(t, q, "work", wfID)
		var attempts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer srv.Close()

		n := notify.New(srv.URL, "", time.Hour, q)
		runNotifier(t, n)
		n.Publish("task.needs_human", map[string]any{"task_id": task.ID, "message": "do not retry"})

		waitFor(t, func() bool { return attempts.Load() >= 1 })
		// Give any (incorrect) retry a chance to land, then assert it never did.
		time.Sleep(300 * time.Millisecond)
		if got := attempts.Load(); got != 1 {
			t.Errorf("expected exactly 1 attempt for a 400 response, got %d", got)
		}
	})
}

func TestNotifier_LabelChanged_OnlyFiresForHumanGateLabel(t *testing.T) {
	db := setupTestDB(t)
	q := gen.New(db.SQL())
	wfID := defaultWorkflowID(t, q)

	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.New(srv.URL, "", time.Hour, q)
	runNotifier(t, n)

	// "work" is not a human gate in the default seeded workflow.
	nonGateTask := createTestTask(t, q, "plan", wfID)
	n.Publish("task.label_changed", map[string]any{
		"task_id": nonGateTask.ID,
		"from":    "plan",
		"to":      "work",
	})
	time.Sleep(200 * time.Millisecond)
	if got := received.Load(); got != 0 {
		t.Fatalf("expected no delivery for non-gate label, got %d", got)
	}

	// "review-plan" is a human gate (both outgoing transitions are human).
	gateTask := createTestTask(t, q, "plan", wfID)
	n.Publish("task.label_changed", map[string]any{
		"task_id": gateTask.ID,
		"from":    "plan",
		"to":      "review-plan",
	})
	waitFor(t, func() bool { return received.Load() == 1 })
}

func TestNotifier_Publish_DoesNotBlockWhenQueueFullAndRunNotDraining(t *testing.T) {
	db := setupTestDB(t)
	q := gen.New(db.SQL())
	wfID := defaultWorkflowID(t, q)
	task := createTestTask(t, q, "work", wfID)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// No Run goroutine started: the queue will fill up and stay full.
	n := notify.New(srv.URL, "", time.Hour, q)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			n.Publish("task.needs_human", map[string]any{"task_id": task.ID, "run_id": uuid.NewString()})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked with a full queue and no draining Run goroutine")
	}
}

func TestNotifier_DisabledWhenURLEmpty(t *testing.T) {
	db := setupTestDB(t)
	q := gen.New(db.SQL())
	n := notify.New("", "", time.Hour, q)
	// Should not panic, and Run should return immediately.
	done := make(chan struct{})
	go func() {
		n.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return immediately for a disabled Notifier")
	}
	n.Publish("task.needs_human", map[string]any{"task_id": "x"})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	// 8s comfortably covers the deliver retry backoff (1s + 4s = 5s minimum
	// wall time for a 3-attempt retry sequence in TestNotifier_Retry_*).
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
