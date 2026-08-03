package handlers

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// outcomeQualityCacheTTL bounds how often GET /dashboard/outcome-quality
// recomputes its aggregates. These queries scan task_label_history and
// agent_runs in full (both grow without bound - log retention prunes
// agent_logs, not these), so unlike the main /dashboard handler this is
// deliberately its own endpoint with its own short-TTL cache rather than
// being folded into the WS-refetched GET /dashboard response (see #346:
// that endpoint already reads the entire tasks table and is refetched on
// four WS event types with no debounce - adding an unbounded history scan
// there would compound the problem). The numbers here don't need to be
// live; a human staring at a performance table cares about "close enough
// as of a few tens of seconds ago", not real-time.
const outcomeQualityCacheTTL = 30 * time.Second

// minSampleSizeForRate is the run/task count below which a rate (rework,
// human-touch, escalation) is considered too noisy to trust at face value.
// The UI still receives the raw rate and n so it can decide how to render
// it (e.g. greyed out / flagged), rather than the backend silently hiding
// data - see outcomeQualityConfigRow.LowSample.
const minSampleSizeForRate = 10

type OutcomeQualityHandler struct {
	q *gen.Queries

	mu       sync.Mutex
	cached   outcomeQualityResponse
	cachedAt time.Time
}

func NewOutcomeQualityHandler(q *gen.Queries) *OutcomeQualityHandler {
	return &OutcomeQualityHandler{q: q}
}

// outcomeQualityResponse is the full (unfiltered) computed result, cached
// for outcomeQualityCacheTTL. The repo filter (see Get) is applied on top
// of this cached snapshot rather than being part of the cache key, so a
// repo-filtered request never has to wait out a separate cache slot and a
// second request for a different repo never triggers a redundant recompute.
type outcomeQualityResponse struct {
	Configs []outcomeQualityConfigRow `json:"configs"`
}

// outcomeQualityConfigRow is a per-agent-config outcome-quality row. Every
// rate is reported alongside its own denominator (the *_n fields) so a
// small sample doesn't masquerade as a confident number - see
// minSampleSizeForRate. TasksDone is the shared denominator for
// cost-to-done, rework, human-touch, and review-burden (all computed only
// over tasks this config finished, i.e. whose last run was under this
// config and which reached a terminal label); escalation rate instead uses
// RunsFinished (completed + waiting_human runs) since it's a run-level, not
// task-level, outcome.
type outcomeQualityConfigRow struct {
	AgentConfigID string `json:"agent_config_id"`
	AgentName     string `json:"agent_name"`
	Provider      string `json:"provider"`

	TasksDone int64 `json:"tasks_done"`

	AvgCostToDoneUSD float64 `json:"avg_cost_to_done_usd"`

	ReworkRatePercent float64 `json:"rework_rate_percent"`
	ReworkN           int64   `json:"rework_n"`
	LowSampleRework   bool    `json:"low_sample_rework"`

	HumanTouchRatePercent float64 `json:"human_touch_rate_percent"`
	HumanTouchN           int64   `json:"human_touch_n"`
	LowSampleHumanTouch   bool    `json:"low_sample_human_touch"`

	AvgReviewComments float64 `json:"avg_review_comments"`

	RunsFinished          int64   `json:"runs_finished"`
	EscalationRatePercent float64 `json:"escalation_rate_percent"`
	LowSampleEscalation   bool    `json:"low_sample_escalation"`
}

// taskAgg accumulates per-task state while walking that task's runs and
// label-history rows in chronological order.
type taskAgg struct {
	repoID         string
	isTerminal     bool
	totalCost      float64
	lastConfigID   *string
	visitedLabels  map[string]bool
	reworkEvents   int
	reworkConfigs  map[string]int // agent_config_id -> rework events attributed to it
	humanTouched   bool
	reviewComments int64
}

// Get returns the cached (or freshly computed) per-agent-config
// outcome-quality aggregates, optionally scoped to a single repo via
// ?repo_id=. See docs/api.md#dashboard for the full metric definitions and
// caveats.
func (h *OutcomeQualityHandler) Get(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("repo_id")

	full, err := h.getCached(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}

	if repoID == "" {
		JSON(w, http.StatusOK, full)
		return
	}

	filtered, err := h.compute(r.Context(), repoID)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, filtered)
}

func (h *OutcomeQualityHandler) getCached(ctx context.Context) (outcomeQualityResponse, error) {
	h.mu.Lock()
	if time.Since(h.cachedAt) < outcomeQualityCacheTTL {
		cached := h.cached
		h.mu.Unlock()
		return cached, nil
	}
	h.mu.Unlock()

	result, err := h.compute(ctx, "")
	if err != nil {
		return outcomeQualityResponse{}, err
	}

	h.mu.Lock()
	h.cached = result
	h.cachedAt = time.Now()
	h.mu.Unlock()

	return result, nil
}

// compute builds the full per-agent-config outcome-quality table. When
// repoID is non-empty, only tasks belonging to that repo are included -
// segmenting by repo matters because a config can be excellent on one
// codebase and poor on another (the same confounding problem as #355).
func (h *OutcomeQualityHandler) compute(ctx context.Context, repoID string) (outcomeQualityResponse, error) {
	tasks, err := h.q.ListOutcomeQualityTasks(ctx)
	if err != nil {
		return outcomeQualityResponse{}, err
	}
	runs, err := h.q.ListOutcomeQualityRuns(ctx)
	if err != nil {
		return outcomeQualityResponse{}, err
	}
	history, err := h.q.ListOutcomeQualityLabelHistory(ctx)
	if err != nil {
		return outcomeQualityResponse{}, err
	}
	configs, err := h.q.ListOutcomeQualityAgentConfigs(ctx)
	if err != nil {
		return outcomeQualityResponse{}, err
	}
	reviewCounts, err := h.q.CountReviewCommentsByTask(ctx)
	if err != nil {
		return outcomeQualityResponse{}, err
	}

	aggs := make(map[string]*taskAgg, len(tasks))
	for _, t := range tasks {
		if t.Archived != 0 {
			continue
		}
		if repoID != "" && t.RepoID != repoID {
			continue
		}
		aggs[t.TaskID] = &taskAgg{
			repoID:        t.RepoID,
			isTerminal:    t.IsTerminal != 0,
			visitedLabels: map[string]bool{},
			reworkConfigs: map[string]int{},
		}
	}

	for _, rc := range reviewCounts {
		if a, ok := aggs[rc.TaskID]; ok {
			a.reviewComments = rc.CommentCount
		}
	}

	// Runs arrive sorted (task_id, created_at, id) ascending (see
	// ListOutcomeQualityRuns), so a single pass accumulates total cost and
	// tracks the most-recent-so-far config per task - both the running
	// "last config" (used once we reach the end of a task's runs) and, via
	// runsByTask below, the config attribution needed per label-history
	// transition.
	runsByTask := make(map[string][]gen.ListOutcomeQualityRunsRow)
	for _, run := range runs {
		a, ok := aggs[run.TaskID]
		if !ok {
			continue
		}
		a.totalCost += run.CostUsd
		if run.AgentConfigID != nil {
			a.lastConfigID = run.AgentConfigID
		}
		runsByTask[run.TaskID] = append(runsByTask[run.TaskID], run)
	}

	// Escalation rate is a run-level (not task-level) stat: of the runs that
	// reached a terminal run status (completed or waiting_human - a run
	// still pending/running hasn't "ended" yet), what fraction ended
	// waiting_human rather than completed. Attributed to the run's own
	// agent_config_id, independent of the rework/cost-to-done task-level
	// attribution above.
	type escAcc struct {
		finished  int64
		escalated int64
	}
	escByConfig := make(map[string]*escAcc)
	for _, run := range runs {
		// Scope escalation rate to the same task set as everything else
		// (repo filter + non-archived) rather than every run in the DB.
		if _, ok := aggs[run.TaskID]; !ok {
			continue
		}
		if run.AgentConfigID == nil {
			continue
		}
		if run.Status != "completed" && run.Status != "waiting_human" {
			continue
		}
		acc, ok := escByConfig[*run.AgentConfigID]
		if !ok {
			acc = &escAcc{}
			escByConfig[*run.AgentConfigID] = acc
		}
		acc.finished++
		if run.Status == "waiting_human" {
			acc.escalated++
		}
	}

	// Walk each task's label history in chronological order (rows arrive
	// sorted (task_id, created_at, id) ascending - see
	// ListOutcomeQualityLabelHistory), tracking which labels the task has
	// already occupied. A transition into an already-visited label is
	// "rework" - see the package doc comment on rework's definition. Rework
	// is attributed to whichever run most recently preceded the backward
	// transition (the config whose work triggered the bounce-back), not the
	// config that eventually cleaned it up.
	for _, hEntry := range history {
		a, ok := aggs[hEntry.TaskID]
		if !ok {
			continue
		}
		if hEntry.Trigger == string(workflow.TriggerHuman) {
			a.humanTouched = true
		}
		if a.visitedLabels[hEntry.ToLabel] {
			a.reworkEvents++
			if cfg := precedingRunConfig(runsByTask[hEntry.TaskID], hEntry.CreatedAt); cfg != "" {
				a.reworkConfigs[cfg]++
			}
		}
		a.visitedLabels[hEntry.ToLabel] = true
	}

	// configAcc's tasksDone is the shared denominator for every rate below
	// except escalation (see outcomeQualityConfigRow's doc comment): a task
	// counts toward its *last* run's config, matching the existing
	// avg_turns_to_done attribution convention in agentConfigStats. Rework's
	// numerator (reworkEvents) is the one exception - it is attributed to
	// whichever config's run preceded the backward transition, which can
	// differ from the task's last-run config (see the loop below) - but its
	// denominator still uses this same last-run tasksDone count, consistent
	// with every other rate on this row.
	type configAcc struct {
		name              string
		provider          string
		tasksDone         int64
		totalCost         float64
		reworkEvents      int64 // numerator: rework events attributed to this config
		humanTouchedTasks int64
		reviewComments    int64
	}
	accByConfig := make(map[string]*configAcc, len(configs))
	for _, c := range configs {
		accByConfig[c.AgentConfigID] = &configAcc{name: c.AgentName, provider: c.Provider}
	}

	for _, a := range aggs {
		if !a.isTerminal || a.lastConfigID == nil {
			continue
		}
		acc, ok := accByConfig[*a.lastConfigID]
		if !ok {
			continue
		}
		acc.tasksDone++
		acc.totalCost += a.totalCost
		if a.humanTouched {
			acc.humanTouchedTasks++
		}
		acc.reviewComments += a.reviewComments
	}
	// Rework events are attributed to the run that immediately preceded the
	// backward transition, which may be a different config than the task's
	// last run (e.g. agent A caused the bounce-back, agent B later finished
	// the task cleanly) - see the loop above. Fold those events into the
	// numerator of whichever config actually caused them; the denominator
	// (tasksDone) stays last-run-attributed like every other rate here.
	for _, a := range aggs {
		if !a.isTerminal {
			continue
		}
		for cfgID, n := range a.reworkConfigs {
			if acc, ok := accByConfig[cfgID]; ok {
				acc.reworkEvents += int64(n)
			}
		}
	}

	rows := make([]outcomeQualityConfigRow, 0, len(accByConfig))
	for id, acc := range accByConfig {
		row := outcomeQualityConfigRow{
			AgentConfigID: id,
			AgentName:     acc.name,
			Provider:      acc.provider,
			TasksDone:     acc.tasksDone,
		}
		if acc.tasksDone > 0 {
			row.AvgCostToDoneUSD = acc.totalCost / float64(acc.tasksDone)
			row.AvgReviewComments = float64(acc.reviewComments) / float64(acc.tasksDone)

			row.ReworkRatePercent = float64(acc.reworkEvents) / float64(acc.tasksDone) * 100
			row.ReworkN = acc.tasksDone
			row.LowSampleRework = acc.tasksDone < minSampleSizeForRate

			row.HumanTouchRatePercent = float64(acc.humanTouchedTasks) / float64(acc.tasksDone) * 100
			row.HumanTouchN = acc.tasksDone
			row.LowSampleHumanTouch = acc.tasksDone < minSampleSizeForRate
		}
		if esc, ok := escByConfig[id]; ok && esc.finished > 0 {
			row.RunsFinished = esc.finished
			row.EscalationRatePercent = float64(esc.escalated) / float64(esc.finished) * 100
			row.LowSampleEscalation = esc.finished < minSampleSizeForRate
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].TasksDone > rows[j].TasksDone })

	return outcomeQualityResponse{Configs: rows}, nil
}

// precedingRunConfig returns the agent_config_id of the last run in runs
// (sorted ascending by created_at) whose created_at is <= at, i.e. the run
// that was in flight or most recently finished when this label-history
// transition happened. Returns "" if no such run exists or its config was
// later deleted (agent_config_id NULL).
func precedingRunConfig(runs []gen.ListOutcomeQualityRunsRow, at time.Time) string {
	var last *string
	for _, r := range runs {
		if r.CreatedAt.After(at) {
			break
		}
		if r.AgentConfigID != nil {
			last = r.AgentConfigID
		}
	}
	if last == nil {
		return ""
	}
	return *last
}
