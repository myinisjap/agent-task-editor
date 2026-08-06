package handlers

import (
	"context"
	"encoding/json"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
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
	// CumulativeCostUsd is the task's lifetime recorded cost across every run
	// regardless of status (SumTaskCost), matching how the dispatcher's
	// cost-budget guard counts spend. Only populated on the single-task GET
	// (Get), since GET /tasks/{id}/runs is now paginated and a client-side sum
	// over one page of runs would silently undercount once a task has more
	// runs than fit on a page (see TaskHeader's cost badge). Omitted (zero
	// value) on list responses to avoid an extra query per row.
	CumulativeCostUsd float64 `json:"cumulative_cost_usd"`
	// BlockReason is a derived, read-time explanation of why this task isn't
	// currently being dispatched (e.g. paused, rate-limited, cost budget
	// exhausted — see agent.BlockReason). Nil when the task isn't currently a
	// dispatch candidate, or is a candidate with no active block (i.e. it's
	// simply next in line — see QueuePosition, a separate concern). Only the
	// first reason the dispatcher would hit is reported, not the full set.
	BlockReason *agent.BlockReason `json:"block_reason,omitempty"`
	// MatchedRuleName is the display name of the intake rule referenced by
	// MatchedRuleID (embedded via gen.Task), resolved at read time so the UI
	// can show "Created by rule <name>" without a second round trip. Nil
	// when MatchedRuleID is nil, or if the rule was since deleted (the
	// column's ON DELETE SET NULL means that combination shouldn't persist,
	// but a lookup miss degrades gracefully to omitting the name rather than
	// erroring the whole task response). Only populated on the single-task
	// GET, mirroring CumulativeCostUsd.
	MatchedRuleName *string `json:"matched_rule_name,omitempty"`
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

// dependencyCountMap fetches derived dependency counts for the given task
// ids, keyed by task id. Tasks absent from the map have zero of both. One
// query serves the whole set of ids (a page, or a single task) so the board
// avoids N+1 without scanning tasks outside the current response.
func (h *TasksHandler) dependencyCountMap(ctx context.Context, ids []string) map[string]depCounts {
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.q.ListTaskDependencyCountsForTasks(ctx, ids)
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

// subtaskRollupMap fetches per-parent child rollups keyed by parent id, for
// the given set of (potential parent) task ids. Parents absent from the map
// have no children. One query serves the whole set of ids (a page, or a
// single task) instead of self-joining the entire tasks table.
func (h *TasksHandler) subtaskRollupMap(ctx context.Context, ids []string) map[string]subtaskRollup {
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.q.ListSubtaskRollupsForParents(ctx, ids)
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
//
// Unlike dependencyCountMap/subtaskRollupMap, this is intentionally NOT
// scoped to the requested page's ids: a task's queue position is its rank
// among *all* currently pickup-eligible tasks, so computing it requires the
// full ordered candidate set regardless of which page is being rendered.
// This keeps ListAgentPickupTasks unfiltered by design; the indexes added in
// migration 048 (plus the existing task_dependencies/workflow_labels/
// workflow_transitions indexes) keep that query's per-candidate subqueries
// cheap. Narrowing this is a separate, larger change (e.g. capping/paginating
// the queue itself) and is out of scope here.
//
// The map is only populated when the worker pool has no free slot (i.e. the
// task would actually have to wait its turn). When the pool has idle
// capacity — or no pool is wired at all (h.canceller nil, e.g. in tests) —
// an eligible task will be picked up on the next sweep rather than sitting
// in a real queue, so it's not surfaced as "queued" and this returns nil.
func (h *TasksHandler) queuePositionMap(ctx context.Context) map[string]int {
	if h.canceller == nil || !h.canceller.Saturated() {
		return nil
	}
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

// blockReasonMap resolves the derived BlockReason for each of the given
// tasks in one shared-state pass (see agent.BlockReasonResolver), keyed by
// task id. Tasks absent from the map either aren't dispatch candidates or
// have no active block. Nil when no resolver is wired (h.blockReasons ==
// nil, e.g. some tests) — mirrors the h.canceller == nil guard elsewhere in
// this file.
func (h *TasksHandler) blockReasonMap(ctx context.Context, tasks []gen.Task) map[string]*agent.BlockReason {
	if h.blockReasons == nil || len(tasks) == 0 {
		return nil
	}
	m, err := h.blockReasons.ResolveMany(ctx, tasks)
	if err != nil {
		return nil
	}
	return m
}

// applyBlockReason sets the derived block reason on a response from the map,
// leaving it nil when the task has none.
func applyBlockReason(resp taskResponse, reasons map[string]*agent.BlockReason) taskResponse {
	if reason, ok := reasons[resp.ID]; ok {
		resp.BlockReason = reason
	}
	return resp
}
