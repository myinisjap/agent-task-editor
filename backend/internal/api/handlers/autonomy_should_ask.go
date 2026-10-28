package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/escape"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

type autonomySuggestionsResponse struct {
	TaskID          string                    `json:"task_id"`
	WorkflowID      string                    `json:"workflow_id"`
	RepoID          string                    `json:"repo_id"`
	Label           string                    `json:"label"`
	PolicyApproved  bool                      `json:"policy_approved"`
	DecisionID      *string                   `json:"decision_id"`
	DiffAvailable   bool                      `json:"diff_available"`
	ChangedFiles    int                       `json:"changed_files"`
	ChangedLines    int                       `json:"changed_lines"`
	PathGlobs       []autonomy.GlobSuggestion `json:"path_globs"`
	TaskType        *string                   `json:"task_type"`
	MinChangedLines *int                      `json:"min_changed_lines"`
}

// diffFor returns the (cached) diff of a task, or an empty result.
func (h *TasksHandler) diffFor(ctx context.Context, task gen.Task) reviewbrief.DiffResult {
	if h.brief == nil {
		return reviewbrief.DiffResult{}
	}
	res, err := h.brief.Diff(ctx, h.q, task)
	if err != nil {
		slog.Warn("autonomy: diff failed", "task_id", task.ID, "err", err)
		return reviewbrief.DiffResult{}
	}
	return res
}

// AutonomySuggestions returns candidate ask-rule conditions derived from a task.
//
// GET /api/v1/tasks/{id}/autonomy/suggestions
func (h *TasksHandler) AutonomySuggestions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	task, err := h.q.GetTask(ctx, chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	res := h.diffFor(ctx, task)
	hasDiff := res.Available && res.Changed.Files > 0
	lines := res.Changed.Additions + res.Changed.Deletions
	resp := autonomySuggestionsResponse{
		TaskID: task.ID, WorkflowID: task.WorkflowID, RepoID: task.RepoID, Label: task.Label,
		DiffAvailable: hasDiff, ChangedFiles: res.Changed.Files, ChangedLines: lines,
		PathGlobs: []autonomy.GlobSuggestion{},
	}
	if d, err := h.q.GetLatestPolicyApprovedDecision(ctx, task.ID); err == nil {
		resp.PolicyApproved = true
		resp.Label = d.Label
		id := d.ID
		resp.DecisionID = &id
	}
	var s autonomy.Suggestions
	if hasDiff {
		s = autonomy.Suggest(res.Paths, lines, task.Type)
		resp.PathGlobs = s.PathGlobs
		resp.MinChangedLines = s.MinChangedLines
	} else {
		s = autonomy.Suggest(nil, 0, task.Type)
	}
	resp.TaskType = s.TaskType
	if resp.PathGlobs == nil {
		resp.PathGlobs = []autonomy.GlobSuggestion{}
	}
	JSON(w, http.StatusOK, resp)
}

type shouldHaveAskedLesson struct {
	Body string  `json:"body"`
	Kind *string `json:"kind"`
}

type shouldHaveAskedRequest struct {
	Rule       *askRuleInput          `json:"rule"`
	ScopeRepo  bool                   `json:"scope_repo"`
	LabelScope string                 `json:"label_scope"`
	SendBack   string                 `json:"send_back_note"`
	Lesson     *shouldHaveAskedLesson `json:"lesson"`
}

type shouldHaveAskedResponse struct {
	Escape        autonomyEscapeResponse  `json:"escape"`
	Rule          *askRuleResponse        `json:"rule"`
	Lesson        *lessonResponse         `json:"lesson"`
	SentBack      bool                    `json:"sent_back"`
	SendBackError string                  `json:"send_back_error,omitempty"`
	StepDown      *autonomyChangeResponse `json:"step_down"`
	Task          taskResponse            `json:"task"`
}

// sendBack moves the task along its "failure" human transition, saving the
// note as run feedback — the same steps as the Reject endpoint.
func (h *TasksHandler) sendBack(ctx context.Context, task gen.Task, note, actor string) error {
	toLabel, err := h.humanPathTarget(ctx, task, "failure")
	if err != nil {
		return err
	}
	if h.taskRunIsLive(ctx, task) {
		return errRunLive
	}
	if note != "" && task.CurrentAgentRunID != nil {
		if err := h.q.SetAgentRunFeedback(ctx, gen.SetAgentRunFeedbackParams{Feedback: &note, ID: *task.CurrentAgentRunID}); err != nil {
			return fmt.Errorf("failed to save feedback")
		}
	}
	return h.engine.Transition(ctx, task.ID, toLabel, workflow.TriggerHuman, actor, note)
}

// ShouldHaveAsked records that a policy-approved task should have been put in
// front of a person: an escape, optionally a rule, a send-back and a lesson.
//
// POST /api/v1/tasks/{id}/autonomy/should-have-asked
func (h *TasksHandler) ShouldHaveAsked(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body shouldHaveAskedRequest
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if h.db == nil {
		Err(w, http.StatusInternalServerError, "autonomy escapes are not configured")
		return
	}
	task, err := h.q.GetTask(ctx, chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "task not found")
		return
	}
	if task.Archived != 0 {
		Err(w, http.StatusConflict, "task is archived")
		return
	}
	dec, err := h.q.GetLatestPolicyApprovedDecision(ctx, task.ID)
	if err != nil {
		Err(w, http.StatusConflict, "task was not approved by the autonomy policy; create an ask rule with POST /autonomy/ask-rules instead")
		return
	}
	switch body.LabelScope {
	case "", "this_gate", "all_gates":
	default:
		Err(w, http.StatusBadRequest, "label_scope must be this_gate or all_gates")
		return
	}
	actor := middleware.ActorFromContext(ctx)

	var rule *autonomy.AskRule
	if body.Rule != nil {
		ru := autonomy.AskRule{PathGlobs: body.Rule.PathGlobs, TaskType: emptyToNil(body.Rule.TaskType), MinChangedLines: body.Rule.MinChangedLines}
		if err := ru.Validate(); err != nil {
			Err(w, http.StatusBadRequest, err.Error())
			return
		}
		rule = &ru
	}
	var lessonParams *gen.UpdateRepoLessonParams
	if body.Lesson != nil {
		kind := "trap"
		if body.Lesson.Kind != nil && *body.Lesson.Kind != "" {
			kind = *body.Lesson.Kind
		}
		lb := lessonBody{Kind: &kind, Body: &body.Lesson.Body}
		p := gen.UpdateRepoLessonParams{Citations: "[]", Status: "active", Source: "human"}
		if msg := lb.apply(&p); msg != "" {
			Err(w, http.StatusBadRequest, "lesson: "+msg)
			return
		}
		lessonParams = &p
	}
	sendBack := false
	if body.SendBack != "" {
		labels, err := h.q.ListWorkflowLabels(ctx, task.WorkflowID)
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		terminal := false
		for _, l := range labels {
			if l.Name == task.Label && l.IsTerminal != 0 {
				terminal = true
			}
		}
		if !terminal {
			if _, err := h.humanPathTarget(ctx, task, "failure"); err != nil {
				Err(w, http.StatusConflict, "cannot send back: "+err.Error())
				return
			}
			if h.taskRunIsLive(ctx, task) {
				Err(w, http.StatusConflict, errRunLive.Error())
				return
			}
			sendBack = true
		}
	}

	now := time.Now().UTC()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback() //nolint:errcheck
	tq := gen.New(tx)
	var ruleRow *gen.AutonomyAskRule
	var ruleID *string
	if rule != nil {
		var label, repo *string
		if body.LabelScope != "all_gates" {
			l := dec.Label
			label = &l
		}
		if body.ScopeRepo {
			rp := task.RepoID
			repo = &rp
		}
		src := task.ID
		row, err := tq.CreateAskRule(ctx, gen.CreateAskRuleParams{
			ID: uuid.NewString(), RepoID: repo, WorkflowID: dec.WorkflowID, Label: label,
			PathGlobs: globsJSON(rule.PathGlobs), TaskType: rule.TaskType, MinChangedLines: ptrInt64(rule.MinChangedLines),
			CreatedBy: actor, SourceTaskID: &src, CreatedAt: now,
		})
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		ruleRow, ruleID = &row, &row.ID
	}
	note := body.SendBack
	esc, err := h.recorder().Insert(ctx, tq, escape.Input{
		TaskID: task.ID, WorkflowID: dec.WorkflowID, Label: dec.Label, Source: autonomy.EscapeSourceShouldHaveAsked,
		DecisionID: &dec.ID, Actor: &actor, Note: &note, RuleID: ruleID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	var lessonRow *gen.RepoLesson
	if lessonParams != nil {
		var repo *string
		if body.ScopeRepo {
			rp := task.RepoID
			repo = &rp
		}
		globs := ""
		if rule != nil {
			globs = strings.Join(rule.PathGlobs, "\n")
		}
		src := task.ID
		l, err := tq.CreateRepoLesson(ctx, gen.CreateRepoLessonParams{
			ID: uuid.NewString(), RepoID: repo, Kind: lessonParams.Kind, Body: lessonParams.Body,
			PathGlobs: globs, Stages: "", Citations: "[]", Source: "human", SourceTaskID: &src, Status: "active",
		})
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		lessonRow = &l
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := shouldHaveAskedResponse{Escape: toAutonomyEscapeResponse(esc, task.Title, true)}
	if ruleRow != nil {
		rr := toAskRuleResponse(*ruleRow)
		resp.Rule = &rr
	}
	if lessonRow != nil {
		lr := toLessonResponse(*lessonRow)
		resp.Lesson = &lr
	}
	// Send back first: the policy's automatic rejected_later detection then
	// sees this escape for the decision and skips it (no double count).
	if sendBack {
		if err := h.sendBack(ctx, task, body.SendBack, actor); err != nil {
			resp.SendBackError = err.Error()
			if errors.Is(err, workflow.ErrRunLive) {
				resp.SendBackError = errRunLive.Error()
			}
		} else {
			resp.SentBack = true
		}
	}
	if ch, err := h.recorder().CheckStepDown(ctx, dec.WorkflowID, dec.Label); err != nil {
		slog.Warn("should-have-asked: step-down check failed", "task_id", task.ID, "err", err)
	} else if ch != nil {
		c := toAutonomyChangeResponse(*ch)
		resp.StepDown = &c
	}
	updated, err := h.q.GetTask(ctx, task.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to fetch updated task")
		return
	}
	if h.pub != nil {
		h.pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: task.ID})
	}
	resp.Task = toTaskResponse(updated)
	JSON(w, http.StatusOK, resp)
}
