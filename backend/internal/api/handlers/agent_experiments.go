package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// minExperimentTasksPerArm is the done-task count per arm below which the
// results are flagged "not enough data".
const minExperimentTasksPerArm = 10

type experimentArmStats struct {
	Tasks                       int64   `json:"tasks"`
	TasksDone                   int64   `json:"tasks_done"`
	FirstPassSuccessRatePercent float64 `json:"first_pass_success_rate_percent"`
	ReworkRatePercent           float64 `json:"rework_rate_percent"`
	HumanTouchRatePercent       float64 `json:"human_touch_rate_percent"`
	AvgCostToDoneUSD            float64 `json:"avg_cost_to_done_usd"`
	AvgDurationSecs             float64 `json:"avg_duration_secs"`
	NotEnoughData               bool    `json:"not_enough_data"`
}

type experimentResults struct {
	Control        experimentArmStats `json:"control"`
	Candidate      experimentArmStats `json:"candidate"`
	MinTasksPerArm int                `json:"min_tasks_per_arm"`
}

type agentExperimentView struct {
	ID                string             `json:"id"`
	AgentConfigID     string             `json:"agent_config_id"`
	CandidateSnapshot json.RawMessage    `json:"candidate_snapshot"`
	BaseVersion       *int64             `json:"base_version"`
	SharePercent      int64              `json:"share_percent"`
	Status            string             `json:"status"`
	Note              *string            `json:"note"`
	ActorID           *string            `json:"actor_id"`
	PromotedVersion   *int64             `json:"promoted_version"`
	StartedAt         time.Time          `json:"started_at"`
	StoppedAt         *time.Time         `json:"stopped_at"`
	Results           *experimentResults `json:"results,omitempty"`
}

type runningExperimentSummary struct {
	ID             string    `json:"id"`
	SharePercent   int64     `json:"share_percent"`
	StartedAt      time.Time `json:"started_at"`
	Note           *string   `json:"note"`
	ControlTasks   int64     `json:"control_tasks"`
	CandidateTasks int64     `json:"candidate_tasks"`
}

func experimentView(e gen.AgentExperiment) agentExperimentView {
	snap := json.RawMessage(e.CandidateSnapshot)
	if !json.Valid(snap) {
		snap = json.RawMessage("{}")
	}
	return agentExperimentView{
		ID: e.ID, AgentConfigID: e.AgentConfigID, CandidateSnapshot: snap,
		BaseVersion: e.BaseVersion, SharePercent: e.SharePercent, Status: e.Status,
		Note: e.Note, ActorID: e.ActorID, PromotedVersion: e.PromotedVersion,
		StartedAt: e.StartedAt, StoppedAt: e.StoppedAt,
	}
}

// runningSummary returns the running-experiment summary for a config, or nil.
func (h *AgentsHandler) runningSummary(ctx context.Context, cfgID string) *runningExperimentSummary {
	exp, err := h.q.GetRunningAgentExperiment(ctx, cfgID)
	if err != nil {
		return nil
	}
	s := &runningExperimentSummary{ID: exp.ID, SharePercent: exp.SharePercent, StartedAt: exp.StartedAt, Note: exp.Note}
	if counts, err := h.q.CountAgentExperimentAssignments(ctx, exp.ID); err == nil {
		for _, c := range counts {
			switch c.Arm {
			case agent.ArmControl:
				s.ControlTasks = c.TaskCount
			case agent.ArmCandidate:
				s.CandidateTasks = c.TaskCount
			}
		}
	}
	return s
}

func (h *AgentsHandler) experimentWithResults(ctx context.Context, e gen.AgentExperiment) (agentExperimentView, error) {
	v := experimentView(e)
	tasks, err := h.q.ListAgentExperimentTasks(ctx, e.ID)
	if err != nil {
		return v, err
	}
	runs, err := h.q.ListAgentExperimentTaskRuns(ctx, e.ID)
	if err != nil {
		return v, err
	}
	hist, err := h.q.ListAgentExperimentLabelHistory(ctx, e.ID)
	if err != nil {
		return v, err
	}
	res := computeExperimentResults(e.ID, e.AgentConfigID, tasks, runs, hist)
	v.Results = &res
	return v, nil
}

type armAcc struct {
	tasks, done, firstPass, rework, human int64
	costSum                               float64
	durSum                                float64
	durN                                  int64
}

func (a *armAcc) stats() experimentArmStats {
	s := experimentArmStats{Tasks: a.tasks, TasksDone: a.done, NotEnoughData: a.done < minExperimentTasksPerArm}
	if a.done > 0 {
		d := float64(a.done)
		s.FirstPassSuccessRatePercent = float64(a.firstPass) / d * 100
		s.ReworkRatePercent = float64(a.rework) / d * 100
		s.HumanTouchRatePercent = float64(a.human) / d * 100
		s.AvgCostToDoneUSD = a.costSum / d
	}
	if a.durN > 0 {
		s.AvgDurationSecs = a.durSum / float64(a.durN)
	}
	return s
}

// computeExperimentResults aggregates per-arm metrics over the experiment's
// assigned, non-archived tasks. Pure so it can be unit-tested.
func computeExperimentResults(expID, agentConfigID string, tasks []gen.ListAgentExperimentTasksRow, runs []gen.ListAgentExperimentTaskRunsRow, history []gen.ListAgentExperimentLabelHistoryRow) experimentResults {
	accs := map[string]*armAcc{agent.ArmControl: {}, agent.ArmCandidate: {}}
	armOf := map[string]string{}
	doneTask := map[string]bool{}
	for _, t := range tasks {
		if t.Archived != 0 {
			continue
		}
		a, ok := accs[t.Arm]
		if !ok {
			continue
		}
		armOf[t.TaskID] = t.Arm
		a.tasks++
		if t.IsTerminal != 0 {
			a.done++
			doneTask[t.TaskID] = true
		}
	}

	type taskRuns struct {
		agentRuns   int
		firstStatus string
		cost        float64
	}
	byTask := map[string]*taskRuns{}
	for _, r := range runs {
		arm, ok := armOf[r.TaskID]
		if !ok {
			continue
		}
		tr := byTask[r.TaskID]
		if tr == nil {
			tr = &taskRuns{}
			byTask[r.TaskID] = tr
		}
		tr.cost += r.CostUsd
		if r.AgentConfigID != nil && *r.AgentConfigID == agentConfigID {
			if tr.agentRuns == 0 {
				tr.firstStatus = r.Status
			}
			tr.agentRuns++
		}
		if r.ExperimentID != nil && *r.ExperimentID == expID && r.ExperimentArm != nil && *r.ExperimentArm == arm &&
			r.StartedAt != nil && r.CompletedAt != nil {
			accs[arm].durSum += r.CompletedAt.Sub(*r.StartedAt).Seconds()
			accs[arm].durN++
		}
	}
	for id := range doneTask {
		a := accs[armOf[id]]
		if tr := byTask[id]; tr != nil {
			a.costSum += tr.cost
			if tr.agentRuns == 1 && tr.firstStatus == "completed" {
				a.firstPass++
			}
		}
	}

	visited := map[string]map[string]bool{}
	reworked := map[string]bool{}
	humanTouched := map[string]bool{}
	for _, hEntry := range history {
		if !doneTask[hEntry.TaskID] {
			continue
		}
		if hEntry.Trigger == "human" {
			humanTouched[hEntry.TaskID] = true
		}
		v := visited[hEntry.TaskID]
		if v == nil {
			v = map[string]bool{}
			visited[hEntry.TaskID] = v
		}
		if v[hEntry.ToLabel] {
			reworked[hEntry.TaskID] = true
		}
		v[hEntry.ToLabel] = true
	}
	for id := range reworked {
		accs[armOf[id]].rework++
	}
	for id := range humanTouched {
		accs[armOf[id]].human++
	}
	return experimentResults{
		Control:        accs[agent.ArmControl].stats(),
		Candidate:      accs[agent.ArmCandidate].stats(),
		MinTasksPerArm: minExperimentTasksPerArm,
	}
}

// ListExperiments handles GET /agents/{id}/experiments (newest first).
func (h *AgentsHandler) ListExperiments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.q.GetAgentConfig(r.Context(), id); err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	exps, err := h.q.ListAgentExperiments(r.Context(), id)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]agentExperimentView, len(exps))
	for i, e := range exps {
		out[i] = experimentView(e)
	}
	JSON(w, http.StatusOK, out)
}

// StartExperiment handles POST /agents/{id}/experiments.
func (h *AgentsHandler) StartExperiment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var body struct {
		Candidate    agentConfigSnapshot `json:"candidate"`
		SharePercent int64               `json:"share_percent"`
		Note         string              `json:"note"`
	}
	if err := decode(r, &body); err != nil {
		Err(w, http.StatusBadRequest, "invalid request body")
		return
	}
	existing, err := h.q.GetAgentConfig(ctx, id)
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	if body.SharePercent < 1 || body.SharePercent > 90 {
		Err(w, http.StatusBadRequest, "share_percent must be between 1 and 90")
		return
	}
	fallbacks, ladderJSON, msg := h.validateSnapshot(r, body.Candidate)
	if msg != "" {
		Err(w, http.StatusBadRequest, "invalid candidate: "+msg)
		return
	}
	snap := body.Candidate
	snap.FallbackProviderConfigIDs = json.RawMessage(fallbacks)
	snap.EscalationLadder = json.RawMessage(ladderJSON)
	snap.EnabledPlugins = jsonTextOrEmptyArray(string(snap.EnabledPlugins))
	snap.EnabledMcpServers = jsonTextOrEmptyArray(string(snap.EnabledMcpServers))
	snap.CommandAllowlist = jsonTextOrEmptyArray(string(snap.CommandAllowlist))
	snap.CommandDenylist = jsonTextOrEmptyArray(string(snap.CommandDenylist))
	if !behaviourChanged(existing, snap.ApplyTo(existing)) {
		Err(w, http.StatusBadRequest, "candidate is identical to the current config")
		return
	}
	if _, err := h.q.GetRunningAgentExperiment(ctx, id); err == nil {
		Err(w, http.StatusConflict, "an experiment is already running for this agent")
		return
	}
	b, err := json.Marshal(snap)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)
	if err := ensureBaselineVersion(ctx, tq, existing); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	var base *int64
	if n, err := tq.GetLatestAgentConfigVersionNumber(ctx, id); err == nil && n > 0 {
		base = &n
	}
	exp, err := tq.CreateAgentExperiment(ctx, gen.CreateAgentExperimentParams{
		ID: uuid.NewString(), AgentConfigID: id, CandidateSnapshot: string(b), BaseVersion: base,
		SharePercent: body.SharePercent, Note: nilIfEmpty(strings.TrimSpace(body.Note)),
		ActorID: nilIfEmpty(middleware.ActorFromContext(ctx)),
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			Err(w, http.StatusConflict, "an experiment is already running for this agent")
			return
		}
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, experimentView(exp))
}

// loadExperiment fetches the experiment from the URL and checks it belongs to
// the agent in the URL.
func (h *AgentsHandler) loadExperiment(w http.ResponseWriter, r *http.Request) (gen.AgentExperiment, bool) {
	exp, err := h.q.GetAgentExperiment(r.Context(), chi.URLParam(r, "experimentId"))
	if err != nil || exp.AgentConfigID != chi.URLParam(r, "id") {
		Err(w, http.StatusNotFound, "experiment not found")
		return gen.AgentExperiment{}, false
	}
	return exp, true
}

// GetExperiment handles GET /agents/{id}/experiments/{experimentId}.
func (h *AgentsHandler) GetExperiment(w http.ResponseWriter, r *http.Request) {
	exp, ok := h.loadExperiment(w, r)
	if !ok {
		return
	}
	v, err := h.experimentWithResults(r.Context(), exp)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, v)
}

// StopExperiment handles POST /agents/{id}/experiments/{experimentId}/stop.
func (h *AgentsHandler) StopExperiment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exp, ok := h.loadExperiment(w, r)
	if !ok {
		return
	}
	n, err := h.q.StopAgentExperiment(ctx, exp.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		Err(w, http.StatusConflict, "experiment is not running")
		return
	}
	exp, _ = h.q.GetAgentExperiment(ctx, exp.ID)
	v, err := h.experimentWithResults(ctx, exp)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, v)
}

// PromoteExperiment handles POST /agents/{id}/experiments/{experimentId}/promote.
// It writes the candidate as the agent's new version, marks the experiment
// promoted and backfills the candidate runs' version tag.
func (h *AgentsHandler) PromoteExperiment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &body); err != nil {
			Err(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	exp, ok := h.loadExperiment(w, r)
	if !ok {
		return
	}
	if exp.Status != "running" {
		Err(w, http.StatusConflict, "experiment is not running")
		return
	}
	existing, err := h.q.GetAgentConfig(ctx, exp.AgentConfigID)
	if err != nil {
		Err(w, http.StatusNotFound, "agent config not found")
		return
	}
	var snap agentConfigSnapshot
	if err := json.Unmarshal([]byte(exp.CandidateSnapshot), &snap); err != nil {
		Err(w, http.StatusInternalServerError, "stored candidate is corrupt")
		return
	}
	fallbacks, ladderJSON, msg := h.validateSnapshot(r, snap)
	if msg != "" {
		Err(w, http.StatusBadRequest, "cannot promote: "+msg)
		return
	}
	note := strings.TrimSpace(body.Note)
	if note == "" {
		short := exp.ID
		if len(short) > 8 {
			short = short[:8]
		}
		note = fmt.Sprintf("Promoted from experiment %s", short)
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()
	tq := h.q.WithTx(tx)
	cfg, _, err := h.applySnapshot(ctx, existing, snap, fallbacks, ladderJSON, note, tq)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The latest version is the candidate's (just written, or already equal
	// to it when the config was edited to match meanwhile).
	latest, err := tq.GetLatestAgentConfigVersionNumber(ctx, cfg.ID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	promoted := latest
	n, err := tq.PromoteAgentExperiment(ctx, gen.PromoteAgentExperimentParams{PromotedVersion: &promoted, ID: exp.ID})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		Err(w, http.StatusConflict, "experiment is not running")
		return
	}
	if err := tq.BackfillExperimentCandidateVersion(ctx, gen.BackfillExperimentCandidateVersionParams{
		AgentConfigVersion: &promoted, ExperimentID: &exp.ID,
	}); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	exp, _ = h.q.GetAgentExperiment(ctx, exp.ID)
	v, err := h.experimentWithResults(ctx, exp)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, struct {
		Experiment agentExperimentView `json:"experiment"`
		Config     agentConfigView     `json:"config"`
	}{v, h.safeConfig(r, cfg)})
}
