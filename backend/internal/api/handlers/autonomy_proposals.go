package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/propose"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// AutonomyProposalsHandler serves model-proposed ask rules (ADR 0040): list,
// accept, dismiss, start a run and list runs.
type AutonomyProposalsHandler struct {
	q   *gen.Queries
	svc *propose.Service
}

func NewAutonomyProposalsHandler(q *gen.Queries, svc *propose.Service) *AutonomyProposalsHandler {
	return &AutonomyProposalsHandler{q: q, svc: svc}
}

type ruleProposalResponse struct {
	ID               string                `json:"id"`
	WorkflowID       string                `json:"workflow_id"`
	Label            string                `json:"label"`
	RepoID           *string               `json:"repo_id"`
	PathGlobs        []string              `json:"path_globs"`
	TaskType         *string               `json:"task_type"`
	MinChangedLines  *int64                `json:"min_changed_lines"`
	Summary          string                `json:"summary"`
	Rationale        string                `json:"rationale"`
	Evidence         []propose.EvidenceRef `json:"evidence"`
	Caught           int64                 `json:"caught"`
	EvidenceTotal    int64                 `json:"evidence_total"`
	Cost             int64                 `json:"cost"`
	CleanApprovals   int64                 `json:"clean_approvals"`
	Unknown          int64                 `json:"unknown"`
	Status           string                `json:"status"`
	RunID            *string               `json:"run_id"`
	ProviderConfigID *string               `json:"provider_config_id"`
	AcceptedRuleID   *string               `json:"accepted_rule_id"`
	DecidedBy        string                `json:"decided_by"`
	DecidedAt        *time.Time            `json:"decided_at"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

func toRuleProposalResponse(p gen.AutonomyRuleProposal) ruleProposalResponse {
	rule := propose.ProposalRule(p)
	ev := []propose.EvidenceRef{}
	_ = json.Unmarshal([]byte(p.Evidence), &ev)
	if ev == nil {
		ev = []propose.EvidenceRef{}
	}
	return ruleProposalResponse{
		ID: p.ID, WorkflowID: p.WorkflowID, Label: p.Label, RepoID: p.RepoID, PathGlobs: rule.PathGlobs,
		TaskType: p.TaskType, MinChangedLines: p.MinChangedLines, Summary: rule.Summary(), Rationale: p.Rationale,
		Evidence: ev, Caught: p.Caught, EvidenceTotal: p.EvidenceTotal, Cost: p.Cost, CleanApprovals: p.CleanApprovals,
		Unknown: p.Unknown, Status: p.Status, RunID: p.RunID, ProviderConfigID: p.ProviderConfigID,
		AcceptedRuleID: p.AcceptedRuleID, DecidedBy: p.DecidedBy, DecidedAt: p.DecidedAt, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

type proposalRunResponse struct {
	ID               string             `json:"id"`
	WorkflowID       string             `json:"workflow_id"`
	Label            string             `json:"label"`
	Trigger          string             `json:"trigger"`
	Actor            string             `json:"actor"`
	Status           string             `json:"status"`
	ProviderConfigID *string            `json:"provider_config_id"`
	EvidenceCount    int64              `json:"evidence_count"`
	ProposalCount    int64              `json:"proposal_count"`
	FlagIdeas        []propose.FlagIdea `json:"flag_ideas"`
	Error            string             `json:"error"`
	CostUSD          float64            `json:"cost_usd"`
	InputTokens      int64              `json:"input_tokens"`
	OutputTokens     int64              `json:"output_tokens"`
	StartedAt        time.Time          `json:"started_at"`
	FinishedAt       *time.Time         `json:"finished_at"`
}

func toProposalRunResponse(r gen.AutonomyProposalRun) proposalRunResponse {
	ideas := []propose.FlagIdea{}
	_ = json.Unmarshal([]byte(r.FlagIdeas), &ideas)
	if ideas == nil {
		ideas = []propose.FlagIdea{}
	}
	return proposalRunResponse{
		ID: r.ID, WorkflowID: r.WorkflowID, Label: r.Label, Trigger: r.Trigger, Actor: r.Actor, Status: r.Status,
		ProviderConfigID: r.ProviderConfigID, EvidenceCount: r.EvidenceCount, ProposalCount: r.ProposalCount,
		FlagIdeas: ideas, Error: r.Error, CostUSD: r.CostUsd, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
}

const proposalListLimit = 200

// List returns rule proposals. status defaults to "proposed"; "all" lists
// every status.
//
// GET /api/v1/autonomy/rule-proposals?workflow_id=&label=&status=
func (h *AutonomyProposalsHandler) List(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	status := qs.Get("status")
	switch status {
	case "":
		status = propose.StatusProposed
	case "all":
		status = ""
	case propose.StatusProposed, propose.StatusAccepted, propose.StatusDismissed:
	default:
		Err(w, http.StatusBadRequest, "status must be proposed, accepted, dismissed or all")
		return
	}
	rows, err := h.q.ListRuleProposals(r.Context(), gen.ListRuleProposalsParams{
		Column1: qs.Get("workflow_id"), Column2: qs.Get("label"), Column3: status, Limit: proposalListLimit,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]ruleProposalResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toRuleProposalResponse(row))
	}
	JSON(w, http.StatusOK, out)
}

func (h *AutonomyProposalsHandler) decisionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, propose.ErrNotFound):
		Err(w, http.StatusNotFound, "rule proposal not found")
	case errors.Is(err, propose.ErrDecided), errors.Is(err, propose.ErrDuplicate), errors.Is(err, propose.ErrInvalidRule):
		Err(w, http.StatusConflict, err.Error())
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}

// Accept creates an ask rule from the proposal (scoped to its gate, created by
// the caller) and marks it accepted.
//
// POST /api/v1/autonomy/rule-proposals/{id}/accept
func (h *AutonomyProposalsHandler) Accept(w http.ResponseWriter, r *http.Request) {
	p, rule, err := h.svc.Accept(r.Context(), chi.URLParam(r, "id"), middleware.ActorFromContext(r.Context()))
	if err != nil {
		h.decisionErr(w, err)
		return
	}
	JSON(w, http.StatusCreated, map[string]any{"proposal": toRuleProposalResponse(p), "rule": toAskRuleResponse(rule)})
}

// Dismiss marks the proposal dismissed; it is not offered again.
//
// POST /api/v1/autonomy/rule-proposals/{id}/dismiss
func (h *AutonomyProposalsHandler) Dismiss(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Dismiss(r.Context(), chi.URLParam(r, "id"), middleware.ActorFromContext(r.Context()))
	if err != nil {
		h.decisionErr(w, err)
		return
	}
	JSON(w, http.StatusOK, toRuleProposalResponse(p))
}

// StartRun starts a proposal run for a gate in the background and returns
// the running row.
//
// POST /api/v1/workflows/{id}/labels/{label}/autonomy/proposals/run
func (h *AutonomyProposalsHandler) StartRun(w http.ResponseWriter, r *http.Request) {
	run, err := h.svc.Start(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "label"), middleware.ActorFromContext(r.Context()))
	switch {
	case err == nil:
		JSON(w, http.StatusAccepted, toProposalRunResponse(run))
	case errors.Is(err, propose.ErrNotFound):
		Err(w, http.StatusNotFound, "workflow or label not found")
	case errors.Is(err, propose.ErrNotGate), errors.Is(err, propose.ErrNoProvider), errors.Is(err, propose.ErrNoModel):
		Err(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, propose.ErrRunning):
		Err(w, http.StatusConflict, err.Error())
	default:
		Err(w, http.StatusInternalServerError, err.Error())
	}
}

// ListRuns returns a gate's recent proposal runs, newest first.
//
// GET /api/v1/workflows/{id}/labels/{label}/autonomy/proposal-runs?limit=
func (h *AutonomyProposalsHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	limit := int64(10)
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1 || n > 100 {
			Err(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	rows, err := h.q.ListProposalRuns(r.Context(), gen.ListProposalRunsParams{
		WorkflowID: chi.URLParam(r, "id"), Label: chi.URLParam(r, "label"), Limit: limit,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]proposalRunResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toProposalRunResponse(row))
	}
	JSON(w, http.StatusOK, out)
}
