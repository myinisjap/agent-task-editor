package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// SubtasksHandler creates child tasks under a parent (Mechanism 2). The
// create_subtask MCP tool posts here live during a planning run so children
// appear on the board mid-run and the agent gets real task ids back. Humans can
// also call it directly. Each child inherits the parent's workflow (and its repo
// unless repo_id names another one, ADR 0041), lands
// on a human-gate (agent_ignore) label by default, and gets an auto-created
// parent→child dependency edge (reusing Mechanism 1) so the parent isn't
// dispatched until every child finishes.
type SubtasksHandler struct {
	q   *gen.Queries
	db  *sql.DB
	pub TaskEventPublisher
}

func NewSubtasksHandler(q *gen.Queries, db *sql.DB, pub TaskEventPublisher) *SubtasksHandler {
	return &SubtasksHandler{q: q, db: db, pub: pub}
}

// defaultSubtaskCap is used when no creating run/config is in scope (e.g. a
// human calling the endpoint directly).
const defaultSubtaskCap = 10

// Create adds a child task under the parent named in the path.
func (h *SubtasksHandler) Create(w http.ResponseWriter, r *http.Request) {
	parentID := chi.URLParam(r, "id")
	var body struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Type        string   `json:"type"`
		Label       string   `json:"label"`
		FileScope   []string `json:"file_scope"`
		RepoID      string   `json:"repo_id"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Title == "" {
		Err(w, http.StatusBadRequest, "title is required")
		return
	}
	if body.Type == "" {
		body.Type = "feature"
	}
	scope, serr := agent.NormalizeFileScope(body.FileScope)
	if serr != nil {
		Err(w, http.StatusBadRequest, serr.Error())
		return
	}

	ctx := r.Context()
	parent, err := h.q.GetTask(ctx, parentID)
	if err != nil {
		Err(w, http.StatusNotFound, "parent task not found")
		return
	}

	// Depth limit 1: a task that is itself a subtask can't create subtasks — the
	// fan-out circuit breaker.
	if parent.ParentTaskID != nil {
		Err(w, http.StatusBadRequest, "a subtask cannot itself create subtasks (depth limit 1)")
		return
	}

	// repo_id (ADR 0041): omitted or equal to the parent's repo is a plain home
	// child; another repo makes a foreign child that lands via its own PR.
	foreign := agent.IsForeignRepo(parent.RepoID, body.RepoID)
	if body.RepoID != "" {
		if st, msg := validateSubtaskRepo(ctx, h.q, body.RepoID); st != 0 {
			Err(w, st, msg)
			return
		}
	}
	childRepoID := parent.RepoID
	if foreign {
		childRepoID = body.RepoID
	}

	// Determine the creating run + its cap / opt-in from the parent's active
	// run. An agent decomposing its parent is that parent's in-flight run.
	subtaskCap := int64(defaultSubtaskCap)
	var createdByRun *string
	if parent.ActiveAgentRunID != nil {
		createdByRun = parent.ActiveAgentRunID
		if run, rerr := h.q.GetAgentRun(ctx, *parent.ActiveAgentRunID); rerr == nil && run.AgentConfigID != nil {
			if cfg, cerr := h.q.GetAgentConfig(ctx, *run.AgentConfigID); cerr == nil {
				if cfg.SubtasksEnabled == 0 {
					Err(w, http.StatusForbidden, "this agent config is not permitted to create subtasks")
					return
				}
				if foreign && cfg.CrossRepoSubtasksEnabled == 0 {
					// Per-task opt-in (ADR 0041 stage 4): a repo the task's creator named is allowed.
					if n, nerr := h.q.TaskRelatedRepoExists(ctx, gen.TaskRelatedRepoExistsParams{TaskID: parentID, RepoID: body.RepoID}); nerr != nil || n == 0 {
						Err(w, http.StatusForbidden, "this agent config is not permitted to create cross-repo subtasks (and the task does not name this repo)")
						return
					}
				}
				if cfg.MaxSubtasks > 0 {
					subtaskCap = cfg.MaxSubtasks
				}
			}
		}
	}

	// Enforce the per-parent cap.
	if n, cerr := h.q.CountSubtasks(ctx, &parentID); cerr != nil {
		Err(w, http.StatusInternalServerError, cerr.Error())
		return
	} else if n >= subtaskCap {
		Err(w, http.StatusUnprocessableEntity, "subtask cap reached for this parent")
		return
	}

	// Resolve the child's landing label. It must be an agent_ignore (human-gate)
	// label — dropping children straight into an agent column would defeat the
	// gate. Default to the first agent_ignore label, else the first label.
	labels, err := h.q.ListWorkflowLabels(ctx, parent.WorkflowID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	label, lst, lmsg := resolveSubtaskLabel(labels, body.Label)
	if lst != 0 {
		Err(w, lst, lmsg)
		return
	}

	// Create the child and the parent→child dependency edge in one transaction.
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)

	child, overlaps, err := insertSubtaskTx(ctx, tq, parent, subtaskInsert{
		Title: body.Title, Description: body.Description, Type: body.Type,
		Label: label, RepoID: childRepoID, Scope: scope, CreatedByRun: createdByRun,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	publishSubtaskCreated(h.pub, child, parentID, overlaps)

	resp := toTaskResponse(child)
	resp.FileScope = append([]string{}, scope...)
	if overlaps == nil {
		overlaps = []overlapDependency{}
	}
	JSON(w, http.StatusCreated, struct {
		taskResponse
		OverlapDependencies []overlapDependency `json:"overlap_dependencies"`
	}{resp, overlaps})
}

// overlapDependency names a sibling a new subtask was serialized behind.
type overlapDependency struct {
	TaskID string   `json:"task_id"`
	Title  string   `json:"title"`
	Globs  []string `json:"globs"`
}

// addOverlapDependencies makes the new child depend on every live, scoped
// sibling whose file scope overlaps its own (so they run sequentially). An
// unscoped child is treated as unknown and gets no edges. Cannot form a cycle:
// the new child has no dependents besides the parent.
func addOverlapDependencies(ctx context.Context, tq *gen.Queries, childID, parentID, childRepoID string, scope []string) ([]overlapDependency, error) {
	if len(scope) == 0 {
		return nil, nil
	}
	siblings, err := tq.ListSubtasks(ctx, &parentID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, s := range siblings {
		// Only same-repo siblings serialize (ADR 0041): different repos cannot conflict.
		if s.ID != childID && s.RepoID == childRepoID {
			ids = append(ids, s.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := tq.ListTaskFanoutForTasks(ctx, ids)
	if err != nil {
		return nil, err
	}
	scopes := map[string][]string{}
	for _, r := range rows {
		scopes[r.TaskID] = splitScope(r.FileScope)
	}
	labels, err := tq.ListWorkflowLabels(ctx, siblings[0].WorkflowID)
	if err != nil {
		return nil, err
	}
	terminal := map[string]bool{}
	for _, l := range labels {
		if l.IsTerminal != 0 {
			terminal[l.Name] = true
		}
	}
	var out []overlapDependency
	for _, s := range siblings {
		if s.ID == childID || s.RepoID != childRepoID || s.Archived != 0 || terminal[s.Label] {
			continue
		}
		sc := scopes[s.ID]
		if len(sc) == 0 || !agent.GlobsOverlap(scope, sc) {
			continue
		}
		if err := tq.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{TaskID: childID, DependsOnTaskID: s.ID}); err != nil {
			return nil, err
		}
		out = append(out, overlapDependency{TaskID: s.ID, Title: s.Title, Globs: agent.OverlappingGlobs(sc, scope)})
	}
	return out, nil
}

// gateLabel returns the human-gate landing label (the agent_ignore label with
// the lowest sort_order) and the first label overall (lowest sort_order),
// used as a fallback when the workflow has no agent_ignore label. Thin wrapper
// over workflow.GateLabel, which owns the label-semantics.
func gateLabel(labels []gen.WorkflowLabel) (gate, first string) {
	return workflow.GateLabel(labels)
}

// isAgentIgnoreLabel reports whether name is an agent_ignore label in the set.
func isAgentIgnoreLabel(labels []gen.WorkflowLabel, name string) bool {
	for _, l := range labels {
		if l.Name == name {
			return l.AgentIgnore != 0
		}
	}
	return false
}

// validateSubtaskRepo returns a non-zero status when repoID can't be used.
func validateSubtaskRepo(ctx context.Context, q *gen.Queries, repoID string) (int, string) {
	if _, err := q.GetRepo(ctx, repoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return http.StatusBadRequest, "repo_id does not reference an existing repo"
		}
		return http.StatusInternalServerError, err.Error()
	}
	return 0, ""
}

// resolveSubtaskLabel picks a child's landing label: it must be an agent_ignore
// (human-gate) label — dropping children straight into an agent column would
// defeat the gate. Default to the first agent_ignore label, else the first label.
func resolveSubtaskLabel(labels []gen.WorkflowLabel, requested string) (string, int, string) {
	gate, first := gateLabel(labels)
	label := requested
	if label == "" {
		label = gate
		if label == "" {
			label = first
		}
	} else if !isAgentIgnoreLabel(labels, label) {
		return "", http.StatusBadRequest, "subtasks may only be created on an agent_ignore (human-gate) label"
	}
	if label == "" {
		return "", http.StatusBadRequest, "workflow has no labels"
	}
	return label, 0, ""
}

// subtaskInsert is the per-child input of insertSubtaskTx.
type subtaskInsert struct {
	Title, Description, Type, Label, RepoID string
	Scope                                   []string
	CreatedByRun                            *string
}

// insertSubtaskTx creates a child, its parent→child dependency edge, its file
// scope and overlap edges, and resets the parent's integration status. tq must
// be tx-bound; shared by create_subtask and POST /tasks children.
func insertSubtaskTx(ctx context.Context, tq *gen.Queries, parent gen.Task, in subtaskInsert) (gen.Task, []overlapDependency, error) {
	childID := uuid.NewString()
	child, err := tq.CreateSubtask(ctx, gen.CreateSubtaskParams{
		ID:             childID,
		Title:          in.Title,
		Description:    in.Description,
		Type:           in.Type,
		Label:          in.Label,
		RepoID:         in.RepoID,
		WorkflowID:     parent.WorkflowID,
		ParentTaskID:   &parent.ID,
		CreatedByRunID: in.CreatedByRun,
	})
	if err != nil {
		return gen.Task{}, nil, err
	}
	if err := tq.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{
		TaskID:          parent.ID,
		DependsOnTaskID: childID,
	}); err != nil {
		return gen.Task{}, nil, err
	}
	if len(in.Scope) > 0 {
		if err := tq.UpsertTaskFileScope(ctx, gen.UpsertTaskFileScopeParams{TaskID: childID, FileScope: strings.Join(in.Scope, "\n")}); err != nil {
			return gen.Task{}, nil, err
		}
	}
	overlaps, err := addOverlapDependencies(ctx, tq, childID, parent.ID, in.RepoID, in.Scope)
	if err != nil {
		return gen.Task{}, nil, err
	}
	// A new child means the parent has to integrate again.
	if err := tq.SetTaskIntegrationStatus(ctx, gen.SetTaskIntegrationStatusParams{TaskID: parent.ID, IntegrationStatus: ""}); err != nil {
		return gen.Task{}, nil, err
	}
	return child, overlaps, nil
}

// publishSubtaskCreated emits the board events for a newly committed child.
func publishSubtaskCreated(pub TaskEventPublisher, child gen.Task, parentID string, overlaps []overlapDependency) {
	if pub == nil {
		return
	}
	pub.Publish(wsevent.TypeTaskCreated, wsevent.TaskCreated{
		ID:       child.ID,
		Title:    child.Title,
		Label:    child.Label,
		RepoID:   child.RepoID,
		ParentID: parentID,
	})
	pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: parentID})
	for _, o := range overlaps {
		pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: o.TaskID})
	}
}
