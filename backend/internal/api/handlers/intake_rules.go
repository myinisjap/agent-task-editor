package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/intake"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// IntakeRulesHandler manages intake_rules: the match->apply table evaluated
// at task-creation time for the 'issue' and 'schedule' sources (see
// internal/intake and migration 051).
type IntakeRulesHandler struct {
	q *gen.Queries
}

func NewIntakeRulesHandler(q *gen.Queries) *IntakeRulesHandler {
	return &IntakeRulesHandler{q: q}
}

// intakeRuleResponse is the wire representation of an intake rule: it
// mirrors gen.IntakeRule but decodes the JSON-encoded match_labels /
// match_author_assoc columns into real arrays so API clients don't need to
// parse a JSON-string-within-JSON.
type intakeRuleResponse struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Enabled           bool      `json:"enabled"`
	SortOrder         int64     `json:"sort_order"`
	MatchSource       string    `json:"match_source"`
	MatchRepoID       *string   `json:"match_repo_id"`
	MatchLabels       []string  `json:"match_labels"`
	MatchTitlePattern string    `json:"match_title_pattern"`
	MatchBodyPattern  string    `json:"match_body_pattern"`
	MatchAuthorAssoc  []string  `json:"match_author_assoc"`
	ApplyTemplateID   *string   `json:"apply_template_id"`
	ApplyPriority     *int64    `json:"apply_priority"`
	ApplyTargetLabel  string    `json:"apply_target_label"`
	ApplyWorkflowID   *string   `json:"apply_workflow_id"`
	ApplyMaxCostUsd   *float64  `json:"apply_max_cost_usd"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func toIntakeRuleResponse(r gen.IntakeRule) intakeRuleResponse {
	return intakeRuleResponse{
		ID:                r.ID,
		Name:              r.Name,
		Enabled:           r.Enabled,
		SortOrder:         r.SortOrder,
		MatchSource:       r.MatchSource,
		MatchRepoID:       r.MatchRepoID,
		MatchLabels:       intake.DecodeStringList(r.MatchLabels),
		MatchTitlePattern: r.MatchTitlePattern,
		MatchBodyPattern:  r.MatchBodyPattern,
		MatchAuthorAssoc:  intake.DecodeStringList(r.MatchAuthorAssoc),
		ApplyTemplateID:   r.ApplyTemplateID,
		ApplyPriority:     r.ApplyPriority,
		ApplyTargetLabel:  r.ApplyTargetLabel,
		ApplyWorkflowID:   r.ApplyWorkflowID,
		ApplyMaxCostUsd:   r.ApplyMaxCostUsd,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

func (h *IntakeRulesHandler) List(w http.ResponseWriter, r *http.Request) {
	rules, err := h.q.ListIntakeRules(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]intakeRuleResponse, 0, len(rules))
	for _, rule := range rules {
		resp = append(resp, toIntakeRuleResponse(rule))
	}
	JSON(w, http.StatusOK, resp)
}

func (h *IntakeRulesHandler) Get(w http.ResponseWriter, r *http.Request) {
	rule, err := h.q.GetIntakeRule(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "intake rule not found")
		return
	}
	JSON(w, http.StatusOK, toIntakeRuleResponse(rule))
}

// intakeRuleBody is the create/update request payload. Match/apply fields
// mirror the intake_rules columns 1:1 — see migration 051's doc comment and
// internal/intake's package doc for their semantics.
type intakeRuleBody struct {
	Name              string   `json:"name"`
	Enabled           *bool    `json:"enabled"`
	SortOrder         int64    `json:"sort_order"`
	MatchSource       string   `json:"match_source"`
	MatchRepoID       *string  `json:"match_repo_id"`
	MatchLabels       []string `json:"match_labels"`
	MatchTitlePattern string   `json:"match_title_pattern"`
	MatchBodyPattern  string   `json:"match_body_pattern"`
	MatchAuthorAssoc  []string `json:"match_author_assoc"`
	ApplyTemplateID   *string  `json:"apply_template_id"`
	ApplyPriority     *int64   `json:"apply_priority"`
	ApplyTargetLabel  string   `json:"apply_target_label"`
	ApplyWorkflowID   *string  `json:"apply_workflow_id"`
	ApplyMaxCostUsd   *float64 `json:"apply_max_cost_usd"`
}

var validMatchSources = map[string]bool{
	"":         true, // any
	"manual":   true,
	"issue":    true,
	"schedule": true,
	"subtask":  true,
}

// validate checks intakeRuleBody in isolation (syntax/enum/regexp
// validity) and returns the effective workflow id to validate
// apply_target_label against: apply_workflow_id if the rule sets one, else
// the repo pointed to by match_repo_id (if any and it has a workflow), else
// "" (skip the target-label-exists check — a repo-agnostic rule with no
// apply_workflow_id override can't be checked against a single workflow).
func (h *IntakeRulesHandler) validate(ctx context.Context, body intakeRuleBody) (workflowID string, status int, msg string) {
	if body.Name == "" {
		return "", http.StatusBadRequest, "name is required"
	}
	if !validMatchSources[body.MatchSource] {
		return "", http.StatusBadRequest, "match_source must be one of manual, issue, schedule, subtask, or empty for any"
	}
	if body.MatchRepoID != nil && *body.MatchRepoID != "" {
		if _, err := h.q.GetRepo(ctx, *body.MatchRepoID); err != nil {
			return "", http.StatusNotFound, "match_repo_id: repo not found"
		}
	}
	if body.ApplyTemplateID != nil && *body.ApplyTemplateID != "" {
		if _, err := h.q.GetTaskTemplate(ctx, *body.ApplyTemplateID); err != nil {
			return "", http.StatusNotFound, "apply_template_id: template not found"
		}
		// apply_template_id is a silent no-op for match_source == "schedule":
		// internal/schedule.fireIfDue always shapes the created task from the
		// schedule's own gen.TaskSchedule.TemplateID (the template a human
		// picked when creating the schedule) and never reads
		// intake.Decision.TemplateID — a schedule's template is not
		// "optional" the way an issue's shaping is, so there is no sensible
		// "apply this template on top of the schedule's own" semantics to
		// fall back to. Reject at write time rather than let this be
		// configured and silently never take effect (see docs/task-sources.md).
		if body.MatchSource == "schedule" {
			return "", http.StatusBadRequest, "apply_template_id has no effect for match_source \"schedule\": scheduled tasks are always shaped from the schedule's own template. Leave apply_template_id empty for schedule rules, or set match_source to \"issue\"/\"\" to shape imported issues instead."
		}
	}
	if body.ApplyPriority != nil && !validPriority(int(*body.ApplyPriority)) {
		return "", http.StatusBadRequest, "apply_priority must be -1 (low), 0 (normal), 1 (high), or 2 (urgent)"
	}
	if body.ApplyMaxCostUsd != nil && *body.ApplyMaxCostUsd < 0 {
		return "", http.StatusBadRequest, "apply_max_cost_usd must be >= 0"
	}
	if err := intake.ValidatePattern(body.MatchTitlePattern); err != nil {
		return "", http.StatusBadRequest, "match_title_pattern: invalid regexp: " + err.Error()
	}
	if err := intake.ValidatePattern(body.MatchBodyPattern); err != nil {
		return "", http.StatusBadRequest, "match_body_pattern: invalid regexp: " + err.Error()
	}

	effectiveWorkflowID := ""
	if body.ApplyWorkflowID != nil && *body.ApplyWorkflowID != "" {
		wf, err := h.q.GetWorkflow(ctx, *body.ApplyWorkflowID)
		if err != nil {
			return "", http.StatusNotFound, "apply_workflow_id: workflow not found"
		}
		effectiveWorkflowID = wf.ID
	} else if body.MatchRepoID != nil && *body.MatchRepoID != "" {
		repo, err := h.q.GetRepo(ctx, *body.MatchRepoID)
		if err == nil && repo.WorkflowID != nil {
			effectiveWorkflowID = *repo.WorkflowID
		}
	}

	if body.ApplyTargetLabel != "" && effectiveWorkflowID != "" {
		labels, err := h.q.ListWorkflowLabels(ctx, effectiveWorkflowID)
		if err != nil {
			return "", http.StatusInternalServerError, err.Error()
		}
		found := false
		isGate := false
		for _, l := range labels {
			if l.Name == body.ApplyTargetLabel {
				found = true
				isGate = l.AgentIgnore != 0
				break
			}
		}
		if !found {
			return "", http.StatusBadRequest, "apply_target_label is not a label in the effective workflow"
		}
		// The auto-start safety gate: a rule whose apply_target_label is not
		// an agent_ignore (human-gate) label bypasses the human-review step
		// that protects against untrusted imported issue content (see #331).
		// Only allow it when the rule also constrains match_author_assoc to
		// exclusively trusted associations — see intake.AutoStartAllowed,
		// the single place this predicate lives (this handler defers to it
		// rather than re-implementing the trust list).
		//
		// This gate only applies to rules that can match an 'issue' —
		// i.e. match_source is "issue" or "" (any, since an any-source
		// rule can still match an issue). It is deliberately NOT enforced
		// for match_source == "schedule": a schedule's target_label is
		// already human-configured, validated content (see
		// internal/schedule's fireIfDue doc comment and
		// docs/task-sources.md), not third-party imported text, so
		// requiring an author-association constraint there would be
		// nonsensical (schedules have no author) and would make an
		// intentionally-supported configuration impossible to save.
		if body.MatchSource != "schedule" {
			probe := gen.IntakeRule{MatchAuthorAssoc: encodeOrEmpty(body.MatchAuthorAssoc)}
			if !intake.AutoStartAllowed(probe, isGate) {
				return "", http.StatusBadRequest, "apply_target_label targets an agent-triggerable label, which lets this rule auto-start a task from untrusted imported content. Restrict match_author_assoc to " + trustedAssocList() + " before this can be saved, or target the workflow's human-review (agent_ignore) label instead."
			}
		}
	}

	return effectiveWorkflowID, 0, ""
}

func trustedAssocList() string {
	s := ""
	for i, a := range intake.TrustedAssociations {
		if i > 0 {
			s += "/"
		}
		s += a
	}
	return s
}

func encodeOrEmpty(values []string) string {
	enc, err := intake.EncodeStringList(values)
	if err != nil {
		return "[]"
	}
	return enc
}

func (h *IntakeRulesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body intakeRuleBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, status, msg := h.validate(r.Context(), body); status != 0 {
		Err(w, status, msg)
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	matchLabels, err := intake.EncodeStringList(body.MatchLabels)
	if err != nil {
		Err(w, http.StatusBadRequest, "match_labels: "+err.Error())
		return
	}
	matchAuthorAssoc, err := intake.EncodeStringList(body.MatchAuthorAssoc)
	if err != nil {
		Err(w, http.StatusBadRequest, "match_author_assoc: "+err.Error())
		return
	}

	rule, err := h.q.CreateIntakeRule(r.Context(), gen.CreateIntakeRuleParams{
		ID:                uuid.NewString(),
		Name:              body.Name,
		Enabled:           enabled,
		SortOrder:         body.SortOrder,
		MatchSource:       body.MatchSource,
		MatchRepoID:       body.MatchRepoID,
		MatchLabels:       matchLabels,
		MatchTitlePattern: body.MatchTitlePattern,
		MatchBodyPattern:  body.MatchBodyPattern,
		MatchAuthorAssoc:  matchAuthorAssoc,
		ApplyTemplateID:   body.ApplyTemplateID,
		ApplyPriority:     body.ApplyPriority,
		ApplyTargetLabel:  body.ApplyTargetLabel,
		ApplyWorkflowID:   body.ApplyWorkflowID,
		ApplyMaxCostUsd:   body.ApplyMaxCostUsd,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, toIntakeRuleResponse(rule))
}

func (h *IntakeRulesHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body intakeRuleBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, err := h.q.GetIntakeRule(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusNotFound, "intake rule not found")
		return
	}
	if _, status, msg := h.validate(r.Context(), body); status != 0 {
		Err(w, status, msg)
		return
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	matchLabels, err := intake.EncodeStringList(body.MatchLabels)
	if err != nil {
		Err(w, http.StatusBadRequest, "match_labels: "+err.Error())
		return
	}
	matchAuthorAssoc, err := intake.EncodeStringList(body.MatchAuthorAssoc)
	if err != nil {
		Err(w, http.StatusBadRequest, "match_author_assoc: "+err.Error())
		return
	}

	rule, err := h.q.UpdateIntakeRule(r.Context(), gen.UpdateIntakeRuleParams{
		Name:              body.Name,
		Enabled:           enabled,
		SortOrder:         body.SortOrder,
		MatchSource:       body.MatchSource,
		MatchRepoID:       body.MatchRepoID,
		MatchLabels:       matchLabels,
		MatchTitlePattern: body.MatchTitlePattern,
		MatchBodyPattern:  body.MatchBodyPattern,
		MatchAuthorAssoc:  matchAuthorAssoc,
		ApplyTemplateID:   body.ApplyTemplateID,
		ApplyPriority:     body.ApplyPriority,
		ApplyTargetLabel:  body.ApplyTargetLabel,
		ApplyWorkflowID:   body.ApplyWorkflowID,
		ApplyMaxCostUsd:   body.ApplyMaxCostUsd,
		ID:                chi.URLParam(r, "id"),
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "intake rule not found")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, toIntakeRuleResponse(rule))
}

func (h *IntakeRulesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.q.DeleteIntakeRule(r.Context(), chi.URLParam(r, "id")); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// previewLimit caps how many already-imported tasks a preview request scans,
// so an oversized/misused limit can't turn this into an unbounded query.
const previewMaxLimit = 50

// previewBody is the request payload for POST /intake-rules/preview: an
// unsaved (or being-edited) rule body plus the repo to preview it against.
type previewBody struct {
	RepoID string         `json:"repo_id"`
	Rule   intakeRuleBody `json:"rule"`
	Limit  int            `json:"limit"`
}

// previewMatch is one previewed candidate task and whether/how the rule
// would have shaped it.
type previewMatch struct {
	TaskID      string   `json:"task_id"`
	Title       string   `json:"title"`
	Matched     bool     `json:"matched"`
	TargetLabel string   `json:"target_label,omitempty"`
	Priority    *int64   `json:"priority,omitempty"`
	MaxCostUsd  *float64 `json:"max_cost_usd,omitempty"`
}

// Preview reports which of the most recently imported tasks for a repo the
// given (not-necessarily-saved) rule body would have matched, and what it
// would have applied. This previews against already-imported tasks for the
// repo rather than making a live forge API call from the request handler
// (an alternative, more invasive design that fetches directly from the
// forge on every preview request — deliberately not built for v1: it would
// add an unbounded-latency external call to a request handler and duplicate
// tasksource's source-resolution logic here). A repo with no imported
// history yet previews as an empty list rather than an error.
//
// Uses intake.Match directly (the same function tasksource.Importer and
// schedule.Scheduler call) so preview and runtime can never disagree about
// whether a rule matches.
func (h *IntakeRulesHandler) Preview(w http.ResponseWriter, r *http.Request) {
	var body previewBody
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.RepoID == "" {
		Err(w, http.StatusBadRequest, "repo_id is required")
		return
	}
	if _, err := h.q.GetRepo(r.Context(), body.RepoID); err != nil {
		Err(w, http.StatusNotFound, "repo not found")
		return
	}
	limit := body.Limit
	if limit <= 0 || limit > previewMaxLimit {
		limit = previewMaxLimit
	}

	matchLabels, err := intake.EncodeStringList(body.Rule.MatchLabels)
	if err != nil {
		Err(w, http.StatusBadRequest, "match_labels: "+err.Error())
		return
	}
	matchAuthorAssoc, err := intake.EncodeStringList(body.Rule.MatchAuthorAssoc)
	if err != nil {
		Err(w, http.StatusBadRequest, "match_author_assoc: "+err.Error())
		return
	}
	rule := gen.IntakeRule{
		ID:                "preview",
		Enabled:           true,
		MatchSource:       body.Rule.MatchSource,
		MatchRepoID:       body.Rule.MatchRepoID,
		MatchLabels:       matchLabels,
		MatchTitlePattern: body.Rule.MatchTitlePattern,
		MatchBodyPattern:  body.Rule.MatchBodyPattern,
		MatchAuthorAssoc:  matchAuthorAssoc,
		ApplyPriority:     body.Rule.ApplyPriority,
		ApplyTargetLabel:  body.Rule.ApplyTargetLabel,
		ApplyMaxCostUsd:   body.Rule.ApplyMaxCostUsd,
	}

	tasks, err := h.q.ListTasksBySourceRepo(r.Context(), gen.ListTasksBySourceRepoParams{
		Source: "github",
		RepoID: body.RepoID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	giteaTasks, err := h.q.ListTasksBySourceRepo(r.Context(), gen.ListTasksBySourceRepoParams{
		Source: "gitea",
		RepoID: body.RepoID,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	tasks = append(tasks, giteaTasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.After(tasks[j].CreatedAt) })
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}

	results := make([]previewMatch, 0, len(tasks))
	for _, t := range tasks {
		decision, ok := intake.Match([]gen.IntakeRule{rule}, intake.Candidate{
			Source: "issue",
			RepoID: t.RepoID,
			Title:  t.Title,
			Body:   t.Description,
		})
		pm := previewMatch{TaskID: t.ID, Title: t.Title, Matched: ok}
		if ok {
			pm.TargetLabel = decision.TargetLabel
			pm.Priority = decision.Priority
			pm.MaxCostUsd = decision.MaxCostUsd
		}
		results = append(results, pm)
	}
	JSON(w, http.StatusOK, map[string]any{"matches": results})
}
