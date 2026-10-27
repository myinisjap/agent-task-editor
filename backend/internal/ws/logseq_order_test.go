package ws

import (
	"context"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Entries sharing one second-resolution timestamp must come back in seq order
// regardless of their (random) ids, including across a paging boundary.
func TestListAgentLogsPage_SeqTiebreak(t *testing.T) {
	db := openReplayTestDB(t)
	q := gen.New(db.SQL())
	ctx := context.Background()
	taskID := seedTaskWithLogs(t, q, nil)
	task, err := q.GetTask(ctx, taskID)
	if err != nil || task.CurrentAgentRunID == nil {
		t.Fatalf("task/run: %v", err)
	}
	runID := *task.CurrentAgentRunID
	ts := time.Now().Truncate(time.Second)
	// id order (a<b<c) is the reverse of seq order.
	rows := []struct {
		id  string
		seq int64
	}{{"c-first", 1}, {"b-second", 2}, {"a-third", 3}}
	for _, r := range rows {
		if err := q.CreateAgentLog(ctx, gen.CreateAgentLogParams{
			ID: r.id, AgentRunID: runID, Timestamp: ts, Type: "system", Content: r.id, Seq: r.seq,
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := q.ListAgentLogsPage(ctx, gen.ListAgentLogsPageParams{AgentRunID: runID, Column2: "", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 3 || page[0].ID != "a-third" || page[2].ID != "c-first" {
		t.Fatalf("unexpected newest-first order: %+v", page)
	}
	older, err := q.ListAgentLogsPage(ctx, gen.ListAgentLogsPageParams{AgentRunID: runID, Column2: "a-third", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(older) != 2 || older[0].ID != "b-second" || older[1].ID != "c-first" {
		t.Fatalf("unexpected paged order: %+v", older)
	}
	all, err := q.ListAgentLogs(ctx, runID)
	if err != nil || len(all) != 3 || all[0].ID != "c-first" || all[2].ID != "a-third" {
		t.Fatalf("ListAgentLogs order: %+v %v", all, err)
	}
}
