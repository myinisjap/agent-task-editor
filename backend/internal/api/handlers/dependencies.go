package handlers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TaskEventPublisher publishes task lifecycle events to connected WebSocket
// clients. Satisfied by *ws.Hub; may be nil in tests.
type TaskEventPublisher interface {
	Publish(eventType string, payload map[string]any)
}

// DependenciesHandler manages peer task dependency edges (Mechanism 1). Edges
// are a pure dispatch gate: a task with an unsatisfied blocker is never picked
// up by the dispatcher, but humans can still move it anywhere. Blocked-ness is
// derived at read time from the blocker's label/archived state, never stored.
type DependenciesHandler struct {
	q   *gen.Queries
	db  *sql.DB
	pub TaskEventPublisher
}

func NewDependenciesHandler(q *gen.Queries, db *sql.DB, pub TaskEventPublisher) *DependenciesHandler {
	return &DependenciesHandler{q: q, db: db, pub: pub}
}

// dependencyEdge is the wire shape for one blocker or dependent.
type dependencyEdge struct {
	TaskID    string `json:"task_id"`
	Title     string `json:"title"`
	Label     string `json:"label"`
	Archived  bool   `json:"archived"`
	Satisfied bool   `json:"satisfied"`
}

// List returns both directions of a task's dependency edges: the tasks it is
// blocked by (with per-edge satisfaction state) and the tasks that depend on it.
func (h *DependenciesHandler) List(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if _, err := h.q.GetTask(r.Context(), taskID); err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}

	blockers, err := h.q.ListTaskBlockers(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	dependents, err := h.q.ListTaskDependents(r.Context(), taskID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	blockedBy := make([]dependencyEdge, 0, len(blockers))
	unmet := 0
	for _, b := range blockers {
		if b.Satisfied == 0 {
			unmet++
		}
		blockedBy = append(blockedBy, dependencyEdge{
			TaskID:    b.TaskID,
			Title:     b.Title,
			Label:     b.Label,
			Archived:  b.Archived != 0,
			Satisfied: b.Satisfied != 0,
		})
	}
	blocking := make([]dependencyEdge, 0, len(dependents))
	for _, d := range dependents {
		blocking = append(blocking, dependencyEdge{
			TaskID:   d.TaskID,
			Title:    d.Title,
			Label:    d.Label,
			Archived: d.Archived != 0,
			// A dependent's satisfaction is relative to *its* other blockers, not
			// meaningful here; report false so the field is present but unused.
			Satisfied: false,
		})
	}

	JSON(w, http.StatusOK, map[string]any{
		"blocked_by":       blockedBy,
		"blocking":         blocking,
		"blocked_by_count": unmet,
		"blocking_count":   len(blocking),
	})
}

// Add creates a dependency edge (task_id depends on depends_on_task_id). The
// edge is rejected (409) if it would close a cycle or already exists, and
// (400) if the two tasks are in different workflows, are the same task, or the
// blocker's workflow has no terminal label (an edge there could never satisfy).
func (h *DependenciesHandler) Add(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	var body struct {
		DependsOnTaskID string `json:"depends_on_task_id"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.DependsOnTaskID == "" {
		Err(w, http.StatusBadRequest, "depends_on_task_id is required")
		return
	}
	if body.DependsOnTaskID == taskID {
		Err(w, http.StatusBadRequest, "a task cannot depend on itself")
		return
	}

	ctx := r.Context()
	task, err := h.q.GetTask(ctx, taskID)
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	blocker, err := h.q.GetTask(ctx, body.DependsOnTaskID)
	if err != nil {
		Err(w, http.StatusBadRequest, "depends_on task not found")
		return
	}
	if task.WorkflowID != blocker.WorkflowID {
		Err(w, http.StatusBadRequest, "dependencies must be within the same workflow")
		return
	}

	// A blocker whose workflow has no terminal label could never satisfy, which
	// would deadlock the dependent silently. Forbid the edge.
	labels, err := h.q.ListWorkflowLabels(ctx, blocker.WorkflowID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	hasTerminal := false
	for _, l := range labels {
		if l.IsTerminal != 0 {
			hasTerminal = true
			break
		}
	}
	if !hasTerminal {
		Err(w, http.StatusBadRequest, "blocker's workflow has no terminal label; the dependency could never be satisfied")
		return
	}

	// Validate + insert transactionally so a concurrent insert can't jointly
	// form a cycle with this one.
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)

	if _, err := tq.GetTaskDependency(ctx, gen.GetTaskDependencyParams{
		TaskID:          taskID,
		DependsOnTaskID: body.DependsOnTaskID,
	}); err == nil {
		Err(w, http.StatusConflict, "dependency already exists")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	edges, err := tq.ListWorkflowDependencyEdges(ctx, task.WorkflowID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Adding task_id -> depends_on means task_id waits for depends_on. It closes
	// a cycle iff depends_on can already reach task_id along existing edges.
	if path, cyclic := findDependencyPath(edges, body.DependsOnTaskID, taskID); cyclic {
		Err(w, http.StatusConflict, "dependency would create a cycle: "+cyclePathString(taskID, path))
		return
	}

	if err := tq.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{
		TaskID:          taskID,
		DependsOnTaskID: body.DependsOnTaskID,
	}); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.publishUpdated(taskID, body.DependsOnTaskID)
	w.WriteHeader(http.StatusNoContent)
}

// Remove deletes a dependency edge. Idempotent — removing a nonexistent edge
// still returns 204.
func (h *DependenciesHandler) Remove(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	depID := chi.URLParam(r, "dep_id")
	if err := h.q.DeleteTaskDependency(r.Context(), gen.DeleteTaskDependencyParams{
		TaskID:          taskID,
		DependsOnTaskID: depID,
	}); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.publishUpdated(taskID, depID)
	w.WriteHeader(http.StatusNoContent)
}

// publishUpdated emits a task.updated event for each affected task so the board
// re-renders blocked/blocking badges without a manual refresh.
func (h *DependenciesHandler) publishUpdated(ids ...string) {
	if h.pub == nil {
		return
	}
	for _, id := range ids {
		h.pub.Publish("task.updated", map[string]any{"id": id})
	}
}

// findDependencyPath walks the depends-on graph from `start`, returning the
// chain of node ids from start to target (inclusive) if target is reachable.
// Used for cycle detection: if the depended-on task can already reach the
// dependent, adding the new edge would close a loop.
func findDependencyPath(edges []gen.ListWorkflowDependencyEdgesRow, start, target string) ([]string, bool) {
	adj := make(map[string][]string, len(edges))
	for _, e := range edges {
		adj[e.TaskID] = append(adj[e.TaskID], e.DependsOnTaskID)
	}
	visited := make(map[string]bool)
	var dfs func(node string, path []string) ([]string, bool)
	dfs = func(node string, path []string) ([]string, bool) {
		if node == target {
			return path, true
		}
		if visited[node] {
			return nil, false
		}
		visited[node] = true
		for _, next := range adj[node] {
			if p, ok := dfs(next, append(path, next)); ok {
				return p, true
			}
		}
		return nil, false
	}
	return dfs(start, []string{start})
}

// cyclePathString renders the cycle that the new edge (dependent -> path[0])
// would close, e.g. "A → B → C → A".
func cyclePathString(dependent string, path []string) string {
	out := dependent
	for _, n := range path {
		out += " → " + n
	}
	out += " → " + dependent
	return out
}
