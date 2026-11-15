package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// childInput is one entry of POST /tasks "children" (ADR 0041 stage 4).
type childInput struct {
	RepoID      string
	Title       string
	Description string
}

// createTaskInput is the parsed body of POST /tasks, shared by the multipart
// and JSON branches.
type createTaskInput struct {
	Title, Description, Type, RepoID, WorkflowID, Label string
	Priority                                            int
	RelatedRepoIDs                                      []string
	Children                                            []childInput
	// hasMulti is true when related_repo_ids or children was supplied; it
	// switches on the extra validation, transaction and response shape.
	hasMulti bool
	// saveAttachments (multipart only) stores uploads once validation passed.
	taskID          string
	saveAttachments func() ([]string, bool)
}

// createdChild is a child reported in the POST /tasks response.
type createdChild struct {
	ID       string `json:"id"`
	RepoID   string `json:"repo_id"`
	RepoName string `json:"repo_name"`
	Title    string `json:"title"`
	Label    string `json:"label"`
}

const childrenKeysMsg = "children may only set repo_id, title and description (depth limit 1)"

// parseChildren decodes the children array strictly: unknown keys (children,
// parent_task_id, label, file_scope, ...) are rejected.
func parseChildren(raws []json.RawMessage) ([]childInput, string) {
	out := make([]childInput, 0, len(raws))
	for _, raw := range raws {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var c struct {
			RepoID      string `json:"repo_id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err := dec.Decode(&c); err != nil {
			if strings.Contains(err.Error(), "unknown field") {
				return nil, childrenKeysMsg
			}
			return nil, "invalid children entry"
		}
		out = append(out, childInput{RepoID: c.RepoID, Title: c.Title, Description: c.Description})
	}
	return out, ""
}

func (h *TasksHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in createTaskInput

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// Parse multipart form (max 50 MB total)
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			Err(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
			return
		}
		in.Title = r.FormValue("title")
		in.Description = r.FormValue("description")
		in.Type = r.FormValue("type")
		in.RepoID = r.FormValue("repo_id")
		in.WorkflowID = r.FormValue("workflow_id")
		in.Label = r.FormValue("label")
		in.Priority = PriorityNormal
		if raw := r.FormValue("priority"); raw != "" {
			p, perr := strconv.Atoi(raw)
			if perr != nil || !validPriority(p) {
				Err(w, http.StatusBadRequest, "priority must be one of -1 (low), 0 (normal), 1 (high), 2 (urgent)")
				return
			}
			in.Priority = p
		}
		if r.MultipartForm != nil {
			in.RelatedRepoIDs = r.MultipartForm.Value["related_repo_ids"]
			if in.RelatedRepoIDs != nil {
				in.hasMulti = true
			}
		}
		if raw := r.FormValue("children"); raw != "" {
			var raws []json.RawMessage
			if err := json.Unmarshal([]byte(raw), &raws); err != nil {
				Err(w, http.StatusBadRequest, "children must be a JSON array")
				return
			}
			kids, msg := parseChildren(raws)
			if msg != "" {
				Err(w, http.StatusBadRequest, msg)
				return
			}
			in.Children = kids
			in.hasMulti = true
		}
		in.taskID = uuid.NewString()
		in.saveAttachments = func() ([]string, bool) {
			return h.saveUploadedAttachments(w, r, in.taskID)
		}
		h.createTask(r.Context(), w, in)
		return
	}

	// JSON body (no attachments)
	var body struct {
		Title          string            `json:"title"`
		Description    string            `json:"description"`
		Type           string            `json:"type"`
		RepoID         string            `json:"repo_id"`
		WorkflowID     string            `json:"workflow_id"`
		Label          string            `json:"label"`
		Priority       int               `json:"priority"`
		RelatedRepoIDs []string          `json:"related_repo_ids"`
		Children       []json.RawMessage `json:"children"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in = createTaskInput{
		Title: body.Title, Description: body.Description, Type: body.Type, RepoID: body.RepoID,
		WorkflowID: body.WorkflowID, Label: body.Label, Priority: body.Priority,
		RelatedRepoIDs: body.RelatedRepoIDs,
		hasMulti:       len(body.RelatedRepoIDs) > 0 || len(body.Children) > 0,
	}
	if len(body.Children) > 0 {
		kids, msg := parseChildren(body.Children)
		if msg != "" {
			Err(w, http.StatusBadRequest, msg)
			return
		}
		in.Children = kids
	}
	h.createTask(r.Context(), w, in)
}

// createTask validates in and creates the task (and, when asked, its related
// repos and children in one transaction), then writes the 201 response.
func (h *TasksHandler) createTask(ctx context.Context, w http.ResponseWriter, in createTaskInput) {
	if in.Title == "" || in.RepoID == "" {
		Err(w, http.StatusBadRequest, "title and repo_id are required")
		return
	}
	if in.taskID == "" && in.saveAttachments == nil {
		in.taskID = uuid.NewString()
	}
	if in.WorkflowID == "" {
		resolved, werr := h.resolveDefaultWorkflowID(ctx)
		if werr != nil {
			Err(w, http.StatusBadRequest, werr.Error())
			return
		}
		in.WorkflowID = resolved
	}
	if in.Type == "" {
		in.Type = "feature"
	}
	if !validPriority(in.Priority) {
		Err(w, http.StatusBadRequest, "priority must be one of -1 (low), 0 (normal), 1 (high), 2 (urgent)")
		return
	}
	label, lerr := h.resolveInitialLabel(ctx, in.WorkflowID, in.Label)
	if lerr != nil {
		Err(w, http.StatusBadRequest, lerr.Error())
		return
	}

	var childLabel string
	var relatedToStore []string
	if in.hasMulti {
		if h.db == nil {
			Err(w, http.StatusInternalServerError, "database not configured")
			return
		}
		var st int
		var msg string
		childLabel, relatedToStore, st, msg = h.validateMultiRepo(ctx, in)
		if st != 0 {
			Err(w, st, msg)
			return
		}
	}

	attachments := []string{}
	if in.saveAttachments != nil {
		paths, ok := in.saveAttachments()
		if !ok {
			return
		}
		attachments = paths
	}
	attachmentsJSON, err := json.Marshal(attachments)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to marshal attachments")
		return
	}
	params := gen.CreateTaskParams{
		ID:          in.taskID,
		Title:       in.Title,
		Description: in.Description,
		Type:        in.Type,
		Label:       label,
		RepoID:      in.RepoID,
		WorkflowID:  in.WorkflowID,
		Attachments: string(attachmentsJSON),
		Priority:    int64(in.Priority),
	}

	if !in.hasMulti {
		task, err := h.q.CreateTask(ctx, params)
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		JSON(w, http.StatusCreated, h.withIntake(ctx, toTaskResponse(task), task))
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)
	task, err := tq.CreateTask(ctx, params)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, rid := range relatedToStore {
		if err := tq.AddTaskRelatedRepo(ctx, gen.AddTaskRelatedRepoParams{TaskID: task.ID, RepoID: rid}); err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	type made struct {
		child    gen.Task
		overlaps []overlapDependency
	}
	var kids []made
	for _, c := range in.Children {
		child, ov, err := insertSubtaskTx(ctx, tq, task, subtaskInsert{
			Title: c.Title, Description: c.Description, Type: in.Type, Label: childLabel, RepoID: c.RepoID,
		})
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		kids = append(kids, made{child, ov})
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := struct {
		taskResponse
		RelatedRepoIDs []string       `json:"related_repo_ids,omitempty"`
		Children       []createdChild `json:"children,omitempty"`
	}{taskResponse: h.withIntake(ctx, toTaskResponse(task), task), RelatedRepoIDs: relatedToStore}
	for _, k := range kids {
		publishSubtaskCreated(h.pub, k.child, task.ID, k.overlaps)
		name := k.child.RepoID
		if repo, rerr := h.q.GetRepo(ctx, k.child.RepoID); rerr == nil {
			name = repo.Name
		}
		resp.Children = append(resp.Children, createdChild{ID: k.child.ID, RepoID: k.child.RepoID, RepoName: name, Title: k.child.Title, Label: k.child.Label})
	}
	JSON(w, http.StatusCreated, resp)
}

// validateMultiRepo checks related_repo_ids and children before anything is
// written. It returns the children's landing label and the deduplicated repos
// to store in task_related_repos (related ids plus foreign children's repos).
func (h *TasksHandler) validateMultiRepo(ctx context.Context, in createTaskInput) (childLabel string, related []string, status int, msg string) {
	repoExists := func(id string) (int, string) {
		if _, err := h.q.GetRepo(ctx, id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return http.StatusBadRequest, "repo " + id + " does not reference an existing repo"
			}
			return http.StatusInternalServerError, err.Error()
		}
		return 0, ""
	}
	if st, m := repoExists(in.RepoID); st != 0 {
		return "", nil, st, m
	}
	seen := map[string]bool{}
	for _, id := range in.RelatedRepoIDs {
		switch {
		case id == "":
			return "", nil, http.StatusBadRequest, "related_repo_ids entries must be non-empty"
		case id == in.RepoID:
			return "", nil, http.StatusBadRequest, "related_repo_ids must not include the primary repo_id"
		case seen[id]:
			return "", nil, http.StatusBadRequest, "duplicate repo in related_repo_ids"
		}
		if st, m := repoExists(id); st != 0 {
			return "", nil, st, m
		}
		seen[id] = true
		related = append(related, id)
	}
	for _, c := range in.Children {
		if strings.TrimSpace(c.Title) == "" {
			return "", nil, http.StatusBadRequest, "each child requires a title"
		}
		if c.RepoID == "" {
			return "", nil, http.StatusBadRequest, "each child requires a repo_id"
		}
		if c.RepoID != in.RepoID {
			if st, m := repoExists(c.RepoID); st != 0 {
				return "", nil, st, m
			}
			if !seen[c.RepoID] {
				seen[c.RepoID] = true
				related = append(related, c.RepoID)
			}
		}
	}
	if len(in.Children) > defaultSubtaskCap {
		return "", nil, http.StatusUnprocessableEntity, "subtask cap reached: at most " + strconv.Itoa(defaultSubtaskCap) + " children per task"
	}
	if len(in.Children) > 0 {
		labels, err := h.q.ListWorkflowLabels(ctx, in.WorkflowID)
		if err != nil {
			return "", nil, http.StatusInternalServerError, err.Error()
		}
		gate, _ := gateLabel(labels)
		if gate == "" {
			return "", nil, http.StatusUnprocessableEntity, "workflow has no human-gate (agent_ignore) label for children"
		}
		childLabel = gate
	}
	return childLabel, related, 0, ""
}
