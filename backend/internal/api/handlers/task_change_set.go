package handlers

import (
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type changeSetParent struct {
	TaskID          string  `json:"task_id"`
	Title           string  `json:"title"`
	RepoID          string  `json:"repo_id"`
	Label           string  `json:"label"`
	RolledUpCostUsd float64 `json:"rolled_up_cost_usd"`
}

type changeSetChanged struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type changeSetChild struct {
	TaskID        string             `json:"task_id"`
	Title         string             `json:"title"`
	RepoID        string             `json:"repo_id"`
	Foreign       bool               `json:"foreign"`
	Label         string             `json:"label"`
	Branch        string             `json:"branch"`
	GitState      string             `json:"git_state"`
	PRURL         string             `json:"pr_url"`
	MergeStatus   string             `json:"merge_status"`
	Verification  string             `json:"verification"`
	Changed       *changeSetChanged  `json:"changed"`
	AutoMerge     *autoMergeResponse `json:"auto_merge"`
	MergePosition *int               `json:"merge_position"`
}

type changeSetResponse struct {
	MultiRepo  bool             `json:"multi_repo"`
	Parent     *changeSetParent `json:"parent,omitempty"`
	MergeOrder []string         `json:"merge_order"`
	Children   []changeSetChild `json:"children"`
}

// ChangeSet serves GET /tasks/{id}/change-set: the read-only change-set view of
// a multi-repo family (ADR 0041). {id} may be the parent or any child. All
// data is derived on read; a family with no foreign child reports
// multi_repo=false.
func (h *TasksHandler) ChangeSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, err := h.q.GetTask(ctx, chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	parent := task
	if task.ParentTaskID != nil && *task.ParentTaskID != "" {
		p, perr := h.q.GetTask(ctx, *task.ParentTaskID)
		if perr != nil {
			Err(w, http.StatusNotFound, "parent task not found")
			return
		}
		parent = p
	}
	kids, err := h.q.ListSubtasks(ctx, &parent.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	var children []gen.Task
	for i := len(kids) - 1; i >= 0; i-- { // ListSubtasks is newest first
		if kids[i].Archived == 0 {
			children = append(children, kids[i])
		}
	}
	resp := changeSetResponse{MergeOrder: []string{}, Children: []changeSetChild{}}
	if !slices.ContainsFunc(children, func(c gen.Task) bool { return agent.IsForeignChild(parent, c) }) {
		JSON(w, http.StatusOK, resp)
		return
	}
	resp.MultiRepo = true

	ids := taskIDs(children)
	positions := map[string]int{}
	if edges, eerr := h.q.ListWorkflowDependencyEdges(ctx, parent.WorkflowID); eerr == nil {
		if order, ok := agent.ChangeSetMergeOrder(children, edges); ok {
			resp.MergeOrder = order
			for i, id := range order {
				positions[id] = i + 1
			}
		}
	}
	verify := map[string]string{}
	if rows, verr := h.q.ListLatestVerifyStatusForTasks(ctx, ids); verr == nil {
		for _, v := range rows {
			verify[v.TaskID] = v.Status
		}
	}
	autoMerges := h.autoMergeMap(ctx, ids)

	for _, c := range children {
		item := changeSetChild{
			TaskID: c.ID, Title: c.Title, RepoID: c.RepoID,
			Foreign: agent.IsForeignChild(parent, c),
			Label:   c.Label, Branch: c.Branch, GitState: c.GitState,
			PRURL: c.PrUrl, MergeStatus: c.MergeStatus,
			Verification: "not_run",
			AutoMerge:    autoMerges[c.ID],
		}
		if v, ok := verify[c.ID]; ok && v != "" {
			item.Verification = v
		}
		if pos, ok := positions[c.ID]; ok {
			p := pos
			item.MergePosition = &p
		}
		if h.brief != nil && c.Branch != "" {
			if d, derr := h.brief.Diff(ctx, h.q, c); derr == nil && d.Available {
				item.Changed = &changeSetChanged{Files: d.Changed.Files, Additions: d.Changed.Additions, Deletions: d.Changed.Deletions}
			}
		}
		resp.Children = append(resp.Children, item)
	}

	cost, _ := h.q.SumTaskCost(ctx, parent.ID)
	kidsCost, _ := h.q.SumChildrenTaskCost(ctx, &parent.ID)
	resp.Parent = &changeSetParent{
		TaskID: parent.ID, Title: parent.Title, RepoID: parent.RepoID, Label: parent.Label,
		RolledUpCostUsd: cost + kidsCost,
	}
	JSON(w, http.StatusOK, resp)
}
