package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/policy"
	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/pathglob"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const (
	backtestDefaultDays = 30
	backtestMaxDays     = 90
	backtestMaxPasses   = 500
	backtestTimeout     = 25 * time.Second
	sqlTimeFmt          = "2006-01-02 15:04:05"
)

type backtestRequest struct {
	Mode       string   `json:"mode"`
	AskOn      []string `json:"ask_on"`
	MaxCostUSD *float64 `json:"max_cost_usd"`
	Days       int      `json:"days"`
	// ExtraRules are draft ask rules to include in the replay (scoped to this
	// gate; an optional repo_id limits one to tasks of that repo).
	ExtraRules []askRuleInput `json:"extra_rules"`
}

type backtestRuleStop struct {
	TaskID      string    `json:"task_id"`
	Title       string    `json:"title"`
	At          time.Time `json:"at"`
	RuleIndexes []int     `json:"rule_indexes"`
}

// backtestRules reports how many recent policy approvals the draft rules
// would have stopped.
type backtestRules struct {
	PolicyApprovals int                `json:"policy_approvals"`
	Evaluated       int                `json:"evaluated"`
	Unknown         int                `json:"unknown"`
	WouldStop       int                `json:"would_stop"`
	Stopped         []backtestRuleStop `json:"stopped"`
}

type backtestPass struct {
	TaskID    string    `json:"task_id"`
	Title     string    `json:"title"`
	At        time.Time `json:"at"`
	Outcome   string    `json:"outcome"`
	Flags     []string  `json:"flags"`
	WouldSkip *bool     `json:"would_skip"`
	Unknown   bool      `json:"unknown"`
}

type backtestResponse struct {
	Total     int            `json:"total"`
	Evaluated int            `json:"evaluated"`
	Unknown   int            `json:"unknown"`
	WouldSkip int            `json:"would_skip"`
	WouldAsk  int            `json:"would_ask"`
	Rejected  int            `json:"rejected"`
	Escaped   int            `json:"escaped"`
	Truncated bool           `json:"truncated"`
	TimedOut  bool           `json:"timed_out"`
	Passes    []backtestPass `json:"passes"`
	Rules     *backtestRules `json:"rules"`
}

// Backtest replays recent passes through a gate against a proposed autonomy
// setting. Strictly read-only.
func (h *AutonomyHandler) Backtest(w http.ResponseWriter, r *http.Request) {
	var body backtestRequest
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Days == 0 {
		body.Days = backtestDefaultDays
	}
	if body.Days < 0 || body.Days > backtestMaxDays {
		Err(w, http.StatusBadRequest, "days must be between 1 and 90")
		return
	}
	cfg := autonomy.Config{Mode: autonomy.Mode(body.Mode), AskOn: body.AskOn, MaxCostUSD: body.MaxCostUSD}
	if err := cfg.Validate(reviewbrief.FlagCodes); err != nil {
		Err(w, http.StatusBadRequest, err.Error())
		return
	}
	var extra []autonomy.AskRule
	for i, in := range body.ExtraRules {
		ru := in.toRule()
		ru.ID = fmt.Sprintf("extra:%d", i)
		ru.Label = nil
		if err := ru.Validate(); err != nil {
			Err(w, http.StatusBadRequest, fmt.Sprintf("extra_rules[%d]: %s", i, err.Error()))
			return
		}
		extra = append(extra, ru)
	}

	ctx, cancel := context.WithTimeout(r.Context(), backtestTimeout)
	defer cancel()

	wfID, label := chi.URLParam(r, "id"), chi.URLParam(r, "label")
	if _, err := h.q.GetWorkflow(ctx, wfID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			Err(w, http.StatusNotFound, "workflow not found")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	labels, err := h.q.ListWorkflowLabels(ctx, wfID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	transitions, err := h.q.ListWorkflowTransitions(ctx, wfID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	found := false
	for _, l := range labels {
		if l.Name == label {
			found = true
		}
	}
	if !found {
		Err(w, http.StatusNotFound, "label not found")
		return
	}
	if !needsme.IsHumanGate(needsme.WorkflowDef{Labels: labels, Transitions: transitions}, label) {
		Err(w, http.StatusBadRequest, "label is not a human gate")
		return
	}
	success, failure := map[string]bool{}, map[string]bool{}
	for _, t := range transitions {
		if t.FromLabel != label || t.TriggerType != "human" || t.Path == nil {
			continue
		}
		switch *t.Path {
		case "success":
			success[t.ToLabel] = true
		case "failure":
			failure[t.ToLabel] = true
		}
	}

	since := time.Now().UTC().AddDate(0, 0, -body.Days).Format(sqlTimeFmt)
	lbl := label
	exits, err := h.q.ListGateExits(ctx, gen.ListGateExitsParams{
		WorkflowID: wfID, Label: &lbl, Since: since, MaxRows: backtestMaxPasses*2 + 1,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	// exits are newest first; keep classified passes, cap at the most recent N,
	// then reverse to oldest first.
	type pass struct {
		row     gen.ListGateExitsRow
		outcome string
	}
	var picked []pass
	for _, e := range exits {
		var outcome string
		switch {
		case success[e.ToLabel]:
			outcome = "approved"
		case failure[e.ToLabel]:
			outcome = "rejected"
		default:
			continue
		}
		picked = append(picked, pass{e, outcome})
	}
	resp := backtestResponse{Passes: []backtestPass{}}
	if len(picked) > backtestMaxPasses {
		picked = picked[:backtestMaxPasses]
		resp.Truncated = true
	}
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 {
		picked[i], picked[j] = picked[j], picked[i]
	}

	ruleCache := map[string][]autonomy.AskRule{}
	for _, p := range picked {
		bp := backtestPass{TaskID: p.row.TaskID, Title: p.row.Title, At: p.row.CreatedAt, Outcome: p.outcome, Flags: []string{}}
		skip, flags, ok := h.backtestOne(ctx, cfg, wfID, label, extra, ruleCache, p.row)
		if !ok {
			bp.Unknown = true
			if ctx.Err() != nil {
				resp.TimedOut = true
			}
		} else {
			bp.Flags = flags
			bp.WouldSkip = &skip
		}
		resp.Passes = append(resp.Passes, bp)
	}
	resp.Total = len(resp.Passes)
	for _, p := range resp.Passes {
		if p.Unknown {
			resp.Unknown++
			continue
		}
		resp.Evaluated++
		if *p.WouldSkip {
			resp.WouldSkip++
		} else {
			resp.WouldAsk++
		}
		if p.Outcome == "rejected" {
			resp.Rejected++
			if *p.WouldSkip {
				resp.Escaped++
			}
		}
	}
	if len(extra) > 0 {
		resp.Rules = h.backtestRules(ctx, wfID, label, since, extra)
	}
	JSON(w, http.StatusOK, resp)
}

// backtestRules replays the draft rules over the recent policy approvals at
// the gate. Only the extra rules are matched: "how many would it have stopped".
func (h *AutonomyHandler) backtestRules(ctx context.Context, wfID, label, since string, extra []autonomy.AskRule) *backtestRules {
	out := &backtestRules{Stopped: []backtestRuleStop{}}
	needDiff := false
	for _, ru := range extra {
		needDiff = needDiff || ru.NeedsDiff()
	}
	sinceT, _ := time.Parse(sqlTimeFmt, since)
	rows, err := h.q.ListPolicyApprovalsAtGate(ctx, gen.ListPolicyApprovalsAtGateParams{
		WorkflowID: wfID, Label: label, OutcomeAt: &sinceT, Limit: backtestMaxPasses,
	})
	if err != nil {
		return out
	}
	m := pathglob.NewMatcher()
	for _, d := range rows {
		out.PolicyApprovals++
		if ctx.Err() != nil {
			out.Unknown++
			continue
		}
		task, err := h.q.GetTask(ctx, d.TaskID)
		if err != nil {
			out.Unknown++
			continue
		}
		at := time.Now()
		if d.OutcomeAt != nil {
			at = *d.OutcomeAt
		}
		facts := autonomy.Facts{TaskType: task.Type}
		if needDiff {
			res, err := reviewbrief.DiffAsOf(ctx, h.q, task, at)
			if err == nil && res.Available && res.Changed.Files > 0 {
				facts.HasDiff = true
				facts.Files = res.Paths
				facts.ChangedLines = res.Changed.Additions + res.Changed.Deletions
			}
		}
		out.Evaluated++
		var idx []int
		for i, ru := range extra {
			if ru.RepoID != nil && *ru.RepoID != task.RepoID {
				continue
			}
			if ru.Matches(facts, m) {
				idx = append(idx, i)
			}
		}
		if len(idx) > 0 {
			out.WouldStop++
			out.Stopped = append(out.Stopped, backtestRuleStop{TaskID: d.TaskID, Title: d.TaskTitle, At: at, RuleIndexes: idx})
		}
	}
	return out
}

// backtestOne recomputes Evaluate's inputs as of the pass. ok=false means the
// pass cannot be evaluated (unknown).
func (h *AutonomyHandler) backtestOne(ctx context.Context, cfg autonomy.Config, wfID, label string, extra []autonomy.AskRule, cache map[string][]autonomy.AskRule, e gen.ListGateExitsRow) (skip bool, flags []string, ok bool) {
	flags = []string{}
	if ctx.Err() != nil {
		return false, nil, false
	}
	task, err := h.q.GetTask(ctx, e.TaskID)
	if err != nil {
		return false, nil, false
	}
	until := e.CreatedAt.UTC().Format(sqlTimeFmt)
	in := autonomy.Input{Mode: cfg.Mode, AskOn: cfg.AskOn, MaxCostUSD: cfg.MaxCostUSD, AlwaysAsk: task.AlwaysAsk != 0}
	cost, err := h.q.SumTaskCostBefore(ctx, gen.SumTaskCostBeforeParams{TaskID: task.ID, Until: until})
	if err != nil {
		return false, nil, false
	}
	in.CostUSD = cost
	runID, err := h.q.GetLastRunBefore(ctx, gen.GetLastRunBeforeParams{TaskID: task.ID, Until: until})
	switch {
	case err == nil:
		v, err := h.q.GetRunVerification(ctx, runID)
		switch {
		case err == nil:
			in.VerifyStatus = v.Status
		case errors.Is(err, sql.ErrNoRows):
		default:
			return false, nil, false
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return false, nil, false
	}
	// Stored rules applicable at this gate for the task's repo, plus drafts.
	rules, cached := cache[task.RepoID]
	if !cached {
		rl, rid := label, task.RepoID
		rows, err := h.q.ListApplicableAskRules(ctx, gen.ListApplicableAskRulesParams{WorkflowID: wfID, Label: &rl, RepoID: &rid})
		if err != nil {
			return false, nil, false
		}
		rules = policy.RulesFromRows(rows)
		cache[task.RepoID] = rules
	}
	for _, ru := range extra {
		if ru.RepoID == nil || *ru.RepoID == task.RepoID {
			rules = append(rules[:len(rules):len(rules)], ru)
		}
	}
	in.Rules = rules
	needDiff := cfg.Mode == autonomy.ModeOnFlags
	for _, ru := range rules {
		needDiff = needDiff || ru.NeedsDiff()
	}
	in.Facts.TaskType = task.Type
	if needDiff {
		res, err := reviewbrief.DiffAsOf(ctx, h.q, task, e.CreatedAt)
		if cfg.Mode == autonomy.ModeOnFlags && (err != nil || !res.Available) {
			return false, nil, false
		}
		if err == nil && res.Available && res.Changed.Files > 0 {
			in.Facts.HasDiff = true
			in.Facts.Files = res.Paths
			in.Facts.ChangedLines = res.Changed.Additions + res.Changed.Deletions
			if cfg.Mode == autonomy.ModeOnFlags {
				in.HasDiff = true
				for _, f := range res.Flags {
					in.Flags = append(in.Flags, f.Code)
				}
				flags = append(flags, in.Flags...)
			}
		}
	}
	d := autonomy.Evaluate(in)
	return strings.EqualFold(d.Decision, autonomy.DecisionApprove), flags, true
}
