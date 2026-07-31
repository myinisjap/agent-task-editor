package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// openReplayTestDB creates a temp SQLite DB seeded with the default workflow,
// mirroring internal/api/handlers' openTestDB helper (duplicated here rather
// than imported to avoid a package coupling from ws -> api/handlers).
func openReplayTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "ws-replay-test-*.db")
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

// seedTaskWithLogs creates a repo, task, and agent run with the given log
// entries (oldest first), sets the task's current_agent_run_id to that run
// (replayTaskLogs looks the run up via the task), and returns the task id.
func seedTaskWithLogs(t *testing.T, q *gen.Queries, logs []string) (taskID string) {
	t.Helper()
	ctx := context.Background()

	workflows, err := q.ListWorkflows(ctx)
	if err != nil || len(workflows) == 0 {
		t.Fatalf("expected a seeded workflow, got %v (err=%v)", workflows, err)
	}
	wfID := workflows[0].ID

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "replay-repo", Path: t.TempDir()}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	taskID = uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "replay task", WorkflowID: wfID, RepoID: repoID, Label: "work",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	runID := uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: taskID}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID,
		ActiveAgentRunID:  &runID,
		ID:                taskID,
	}); err != nil {
		t.Fatalf("set active run: %v", err)
	}

	base := time.Now().Add(-time.Duration(len(logs)) * time.Second)
	for i, content := range logs {
		if err := q.CreateAgentLog(ctx, gen.CreateAgentLogParams{
			ID:         uuid.NewString(),
			AgentRunID: runID,
			Timestamp:  base.Add(time.Duration(i) * time.Second),
			Type:       "stdout",
			Content:    content,
		}); err != nil {
			t.Fatalf("create agent log: %v", err)
		}
	}
	return taskID
}

func newTestWSServerWithQueries(t *testing.T, hub *Hub, q *gen.Queries) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r, "", "*", q)
	}))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	return srv, wsURL
}

// TestReplayTaskLogs_SubscribeSendsBatchedReplay verifies that subscribing to
// a task with existing agent-run logs triggers a single agent.log_replay
// message carrying the logs in chronological order with has_more=false when
// the log count is under replayLimit.
func TestReplayTaskLogs_SubscribeSendsBatchedReplay(t *testing.T) {
	db := openReplayTestDB(t)
	q := gen.New(db.SQL())
	taskID := seedTaskWithLogs(t, q, []string{"first line", "second line", "third line"})

	hub := NewHub()
	_, wsURL := newTestWSServerWithQueries(t, hub, q)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	send(t, ctx, conn, inboundMsg{Type: "subscribe", TaskID: taskID})

	readCtx, readCancel := context.WithTimeout(ctx, 3*time.Second)
	defer readCancel()
	_, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("expected a log_replay message, got read error: %v", err)
	}

	var evt Event
	if err := json.Unmarshal(data, &evt); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if evt.Type != "agent.log_replay" {
		t.Fatalf("expected type agent.log_replay, got %q (payload=%v)", evt.Type, evt.Payload)
	}

	payload := evt.Payload
	if payload["task_id"] != taskID {
		t.Errorf("expected task_id %q, got %v", taskID, payload["task_id"])
	}
	if hasMore, _ := payload["has_more"].(bool); hasMore {
		t.Errorf("expected has_more=false for a short log, got true")
	}
	entries, ok := payload["entries"].([]any)
	if !ok {
		t.Fatalf("expected entries slice, got %T", payload["entries"])
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 replayed entries, got %d: %v", len(entries), entries)
	}
	first, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("expected entry to be a map, got %T", entries[0])
	}
	if first["content"] != "first line" {
		t.Errorf("expected chronological order (oldest first), got first entry %v", first)
	}
	last, ok := entries[2].(map[string]any)
	if !ok {
		t.Fatalf("expected entry to be a map, got %T", entries[2])
	}
	if last["content"] != "third line" {
		t.Errorf("expected chronological order (oldest first), got last entry %v", last)
	}
}

// TestReplayTaskLogs_UnknownTask_NoReplay verifies that subscribing to a task
// id with no matching row (or no current run) doesn't crash and simply sends
// nothing (replayTaskLogs returns early on GetTask error).
func TestReplayTaskLogs_UnknownTask_NoReplay(t *testing.T) {
	db := openReplayTestDB(t)
	q := gen.New(db.SQL())

	hub := NewHub()
	_, wsURL := newTestWSServerWithQueries(t, hub, q)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	send(t, ctx, conn, inboundMsg{Type: "subscribe", TaskID: "does-not-exist"})

	readCtx, readCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer readCancel()
	_, _, err = conn.Read(readCtx)
	if err == nil {
		t.Error("expected no replay message for an unknown task, but got one")
	}
}
