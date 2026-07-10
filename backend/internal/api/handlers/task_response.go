package handlers

import (
	"context"
	"encoding/json"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// taskResponse is a JSON-serialization wrapper for gen.Task that ensures the
// Attachments field is emitted as a JSON array ([]string) rather than a raw
// JSON string.  gen.Task stores Attachments as a string column containing a
// JSON-encoded array; embedding the struct and shadowing the field with
// json.RawMessage lets us pass the stored JSON bytes through as-is.
type taskResponse struct {
	gen.Task
	Attachments json.RawMessage `json:"attachments"`
	Paused      bool            `json:"paused"`
	Archived    bool            `json:"archived"`
	// Derived dependency counts (Mechanism 1). BlockedByCount is the number of
	// this task's blockers whose edges are still unsatisfied; BlockingCount is
	// the number of tasks that depend on it. Both are computed at read time.
	BlockedByCount int64 `json:"blocked_by_count"`
	BlockingCount  int64 `json:"blocking_count"`
	// Derived subtask rollup (Mechanism 2), non-zero only for parent tasks.
	// SubtaskTotal is the number of children; SubtaskDone is how many sit on a
	// terminal label; SubtaskConflicts is how many are in merge_conflict.
	SubtaskTotal     int64 `json:"subtask_total"`
	SubtaskDone      int64 `json:"subtask_done"`
	SubtaskConflicts int64 `json:"subtask_conflicts"`
	// QueuePosition is a derived, read-time 0-based rank in the current
	// agent-pickup queue (priority DESC, created_at ASC), among tasks
	// currently eligible for dispatch. Nil when the task is not currently
	// pickup-eligible (e.g. blocked, paused, archived, already running, or on
	// a non-agent-triggerable label).
	QueuePosition *int `json:"queue_position"`
}

// toTaskResponse converts a gen.Task to its wire representation.  If the
// stored attachments string is not valid JSON it falls back to an empty array
// so the frontend always receives a proper array. Paused is stored as a
// SQLite INTEGER (0/1) but shadowed here as a real JSON boolean since it is a
// primary, user-facing flag.
func toTaskResponse(t gen.Task) taskResponse {
	raw := json.RawMessage(t.Attachments)
	// Validate that the stored value is actually parseable JSON; fall back to
	// an empty array if it is not (e.g. the column was never set).
	var probe []string
	if err := json.Unmarshal(raw, &probe); err != nil {
		raw = json.RawMessage("[]")
	}
	return taskResponse{Task: t, Attachments: raw, Paused: t.Paused != 0, Archived: t.Archived != 0}
}

// toTaskResponses converts a slice of gen.Task values.
func toTaskResponses(tasks []gen.Task) []taskResponse {
	out := make([]taskResponse, len(tasks))
	for i, t := range tasks {
		out[i] = toTaskResponse(t)
	}
	return out
}

// depCounts pairs a task's derived dependency counts.
type depCounts struct {
	blockedBy int64
	blocking  int64
}

// dependencyCountMap fetches derived dependency counts for every task that
// participates in at least one edge, keyed by task id. Tasks absent from the
// map have zero of both. One query serves a whole page so the board avoids N+1.
func (h *TasksHandler) dependencyCountMap(ctx context.Context) map[string]depCounts {
	rows, err := h.q.ListTaskDependencyCounts(ctx)
	if err != nil {
		return nil
	}
	m := make(map[string]depCounts, len(rows))
	for _, row := range rows {
		m[row.TaskID] = depCounts{blockedBy: row.BlockedByCount, blocking: row.BlockingCount}
	}
	return m
}

// applyDepCounts sets the derived counts on a response from the map.
func applyDepCounts(resp taskResponse, counts map[string]depCounts) taskResponse {
	if c, ok := counts[resp.ID]; ok {
		resp.BlockedByCount = c.blockedBy
		resp.BlockingCount = c.blocking
	}
	return resp
}

// subtaskRollup pairs a parent's derived child counts.
type subtaskRollup struct {
	total     int64
	done      int64
	conflicts int64
}

// subtaskRollupMap fetches per-parent child rollups keyed by parent id. Parents
// absent from the map have no children. One query serves a whole page.
func (h *TasksHandler) subtaskRollupMap(ctx context.Context) map[string]subtaskRollup {
	rows, err := h.q.ListSubtaskRollups(ctx)
	if err != nil {
		return nil
	}
	m := make(map[string]subtaskRollup, len(rows))
	for _, row := range rows {
		m[row.ParentID] = subtaskRollup{
			total:     row.Total,
			done:      floatPtrToInt(row.Done),
			conflicts: floatPtrToInt(row.Conflicts),
		}
	}
	return m
}

// floatPtrToInt converts a nullable SQLite SUM (returned as *float64) to int64.
func floatPtrToInt(f *float64) int64 {
	if f == nil {
		return 0
	}
	return int64(*f)
}

// applyRollup sets the derived subtask rollup on a response from the map.
func applyRollup(resp taskResponse, rollups map[string]subtaskRollup) taskResponse {
	if r, ok := rollups[resp.ID]; ok {
		resp.SubtaskTotal = r.total
		resp.SubtaskDone = r.done
		resp.SubtaskConflicts = r.conflicts
	}
	return resp
}

// queuePositionMap fetches the current agent-pickup queue (already ordered by
// priority DESC, created_at ASC by ListAgentPickupTasks) and returns each
// task's 0-based rank in it, keyed by task id. Tasks not currently eligible
// for dispatch (blocked, paused, archived, already running, etc.) are absent
// from the map. One query serves a whole page, mirroring dependencyCountMap.
func (h *TasksHandler) queuePositionMap(ctx context.Context) map[string]int {
	tasks, err := h.q.ListAgentPickupTasks(ctx)
	if err != nil {
		return nil
	}
	m := make(map[string]int, len(tasks))
	for i, t := range tasks {
		m[t.ID] = i
	}
	return m
}

// applyQueuePosition sets the derived queue position on a response from the
// map, leaving it nil when the task is not currently pickup-eligible.
func applyQueuePosition(resp taskResponse, positions map[string]int) taskResponse {
	if p, ok := positions[resp.ID]; ok {
		pos := p
		resp.QueuePosition = &pos
	}
	return resp
}
