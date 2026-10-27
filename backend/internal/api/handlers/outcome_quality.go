package handlers

import (
	"context"
	"net/http"
	"sort"
	"strconv"
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

// minSampleTasksForRow is the tasks_done count below which a whole row is
// flagged low_sample (the UI greys it out). Distinct from
// minSampleSizeForRate, which flags individual rates.
const minSampleTasksForRow = 5

// Supported group_by values.
const (
	groupByConfig         = "config"
	groupByVersion        = "version"
	groupByProviderConfig = "provider_config"
	groupByRepo           = "repo"
	groupByTaskType       = "task_type"
)

type OutcomeQualityHandler struct {
	q *gen.Queries

	mu       sync.Mutex
	cached   map[string]outcomeQualityResponse // keyed by group_by
	cachedAt map[string]time.Time
}

func NewOutcomeQualityHandler(q *gen.Queries) *OutcomeQualityHandler {
	return &OutcomeQualityHandler{q: q, cached: map[string]outcomeQualityResponse{}, cachedAt: map[string]time.Time{}}
}

// outcomeQualityResponse is the computed result, cached per group_by for
// outcomeQualityCacheTTL. Repo-filtered requests are computed fresh and
// never cached.
type outcomeQualityResponse struct {
	GroupBy string                    `json:"group_by"`
	Configs []outcomeQualityConfigRow `json:"configs"`
}

// outcomeQualityConfigRow is a per-agent-config (optionally per
// config x dimension) outcome-quality row. Every rate is reported alongside
// its own denominator (the *_n fields) so a small sample doesn't masquerade
// as a confident number - see minSampleSizeForRate. TasksDone is the shared
// denominator for cost-to-done, rework, human-touch, and review-burden (all
// computed only over tasks this group finished, i.e. whose last run fell in
// this group and which reached a terminal label); escalation rate instead
// uses RunsFinished (completed + waiting_human runs) since it's a run-level,
// not task-level, outcome.
//
// With group_by != config, rows are keyed by (agent config, dimension):
// version and provider_config come from the run, repo and task_type from the
// task. Task-level metrics follow the task's last run, rework follows the run
// preceding the backward transition, escalation follows each run itself.
type outcomeQualityConfigRow struct {
	// GroupKey uniquely identifies the row (config id + dimension value).
	GroupKey string `json:"group_key"`
	// GroupValue is the display label of the dimension ("v3", "unversioned",
	// a provider config name, repo name or task type); empty for config.
	GroupValue string `json:"group_value,omitempty"`
	Version    *int64 `json:"version,omitempty"`
	// ProviderConfigID / RepoID / TaskType carry the raw dimension value.
	ProviderConfigID string `json:"provider_config_id,omitempty"`
	RepoID           string `json:"repo_id,omitempty"`
	TaskType         string `json:"task_type,omitempty"`

	AgentConfigID string `json:"agent_config_id"`
	AgentName     string `json:"agent_name"`
	Provider      string `json:"provider"`

	TasksDone int64 `json:"tasks_done"`
	// LowSample is true when TasksDone < minSampleTasksForRow.
	LowSample bool `json:"low_sample"`

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

	// LadderRungs is the per-finishing-rung breakdown for groups whose runs
	// carry a ladder_rung. Independent of the last-run attribution of TasksDone:
	// a task counts under each group for the rung of that group's last laddered
	// run on the task, when the task is terminal.
	LadderRungs []ladderRungStat `json:"ladder_rungs,omitempty"`
}

// ladderRungStat counts tasks finished at a ladder rung.
type ladderRungStat struct {
	Rung             int64   `json:"rung"`
	TasksDone        int64   `json:"tasks_done"`
	AvgCostToDoneUSD float64 `json:"avg_cost_to_done_usd"`
}

// groupInfo identifies one (config, dimension) group.
type groupInfo struct {
	key      string
	cfgID    string
	dim      string // raw dimension value ("" for config mode)
	version  *int64
	unversed bool
}

// taskAgg accumulates per-task state while walking that task's runs and
// label-history rows in chronological order.
type taskAgg struct {
	repoID         string
	taskType       string
	isTerminal     bool
	totalCost      float64
	lastRun        *gen.ListOutcomeQualityRunsRow // last run with a config
	visitedLabels  map[string]bool
	reworkEvents   int
	reworkGroups   map[string]int // group key -> rework events attributed to it
	humanTouched   bool
	groupRung      map[string]int64 // group key -> rung of its last laddered run
	reviewComments int64
}

func validGroupBy(g string) bool {
	switch g {
	case groupByConfig, groupByVersion, groupByProviderConfig, groupByRepo, groupByTaskType:
		return true
	}
	return false
}

// Get returns the cached (or freshly computed) outcome-quality aggregates,
// optionally scoped to a single repo via ?repo_id= and broken down via
// ?group_by= (config - default, version, provider_config, repo, task_type).
// See docs/api.md#dashboard for the full metric definitions and caveats.
func (h *OutcomeQualityHandler) Get(w http.ResponseWriter, r *http.Request) {
	repoID := r.URL.Query().Get("repo_id")
	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = groupByConfig
	}
	if !validGroupBy(groupBy) {
		Err(w, http.StatusBadRequest, "group_by must be one of: config, version, provider_config, repo, task_type")
		return
	}

	if repoID == "" {
		full, err := h.getCached(r.Context(), groupBy)
		if err != nil {
			Err(w, http.StatusInternalServerError, err.Error())
			return
		}
		JSON(w, http.StatusOK, full)
		return
	}

	filtered, err := h.compute(r.Context(), repoID, groupBy)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, filtered)
}

func (h *OutcomeQualityHandler) getCached(ctx context.Context, groupBy string) (outcomeQualityResponse, error) {
	h.mu.Lock()
	if at, ok := h.cachedAt[groupBy]; ok && time.Since(at) < outcomeQualityCacheTTL {
		cached := h.cached[groupBy]
		h.mu.Unlock()
		return cached, nil
	}
	h.mu.Unlock()

	result, err := h.compute(ctx, "", groupBy)
	if err != nil {
		return outcomeQualityResponse{}, err
	}

	h.mu.Lock()
	h.cached[groupBy] = result
	h.cachedAt[groupBy] = time.Now()
	h.mu.Unlock()

	return result, nil
}

// compute builds the outcome-quality table. When repoID is non-empty, only
// tasks belonging to that repo are included - segmenting by repo matters
// because a config can be excellent on one codebase and poor on another (the
// same confounding problem as #355).
func (h *OutcomeQualityHandler) compute(ctx context.Context, repoID, groupBy string) (outcomeQualityResponse, error) {
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
			taskType:      t.TaskType,
			isTerminal:    t.IsTerminal != 0,
			visitedLabels: map[string]bool{},
			reworkGroups:  map[string]int{},
			groupRung:     map[string]int64{},
		}
	}

	for _, rc := range reviewCounts {
		if a, ok := aggs[rc.TaskID]; ok {
			a.reviewComments = rc.CommentCount
		}
	}

	// groupOf resolves a run (on a task) to its (config, dimension) group.
	groupOf := func(run *gen.ListOutcomeQualityRunsRow, a *taskAgg) groupInfo {
		g := groupInfo{cfgID: *run.AgentConfigID}
		switch groupBy {
		case groupByVersion:
			if run.AgentConfigVersion != nil {
				g.version = run.AgentConfigVersion
				g.dim = strconv.FormatInt(*run.AgentConfigVersion, 10)
			} else {
				g.unversed = true
				g.dim = "unversioned"
			}
		case groupByProviderConfig:
			g.dim = run.ProviderConfigID
		case groupByRepo:
			g.dim = a.repoID
		case groupByTaskType:
			g.dim = a.taskType
		}
		g.key = g.cfgID + "\x00" + g.dim
		return g
	}
	infos := map[string]groupInfo{}
	groupFor := func(run *gen.ListOutcomeQualityRunsRow, a *taskAgg) groupInfo {
		g := groupOf(run, a)
		infos[g.key] = g
		return g
	}

	// Runs arrive sorted (task_id, created_at, id) ascending (see
	// ListOutcomeQualityRuns), so a single pass accumulates total cost and
	// tracks the most-recent-so-far run per task - both the running "last
	// run" (used once we reach the end of a task's runs) and, via runsByTask
	// below, the attribution needed per label-history transition.
	runsByTask := make(map[string][]gen.ListOutcomeQualityRunsRow)
	for i := range runs {
		run := &runs[i]
		a, ok := aggs[run.TaskID]
		if !ok {
			continue
		}
		a.totalCost += run.CostUsd
		if run.AgentConfigID != nil {
			a.lastRun = run
			if run.LadderRung != nil {
				a.groupRung[groupFor(run, a).key] = *run.LadderRung
			}
		}
		runsByTask[run.TaskID] = append(runsByTask[run.TaskID], *run)
	}

	// Escalation rate is a run-level (not task-level) stat: of the runs that
	// reached a terminal run status (completed or waiting_human - a run
	// still pending/running hasn't "ended" yet), what fraction ended
	// waiting_human rather than completed. Attributed to the run's own group,
	// independent of the rework/cost-to-done task-level attribution above.
	type escAcc struct {
		finished  int64
		escalated int64
	}
	escByGroup := make(map[string]*escAcc)
	for i := range runs {
		run := &runs[i]
		// Scope escalation rate to the same task set as everything else
		// (repo filter + non-archived) rather than every run in the DB.
		a, ok := aggs[run.TaskID]
		if !ok {
			continue
		}
		if run.AgentConfigID == nil {
			continue
		}
		if run.Status != "completed" && run.Status != "waiting_human" {
			continue
		}
		g := groupFor(run, a)
		acc, ok := escByGroup[g.key]
		if !ok {
			acc = &escAcc{}
			escByGroup[g.key] = acc
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
	// "rework". Rework is attributed to whichever run most recently preceded
	// the backward transition (the group whose work triggered the
	// bounce-back), not the one that eventually cleaned it up.
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
			if run := precedingRun(runsByTask[hEntry.TaskID], hEntry.CreatedAt); run != nil {
				a.reworkGroups[groupFor(run, a).key]++
			}
		}
		a.visitedLabels[hEntry.ToLabel] = true
	}

	type configAcc struct {
		ladder            map[int64]*ladderRungStat
		info              groupInfo
		tasksDone         int64
		totalCost         float64
		reworkEvents      int64 // numerator: rework events attributed to this group
		humanTouchedTasks int64
		reviewComments    int64
	}
	configMeta := make(map[string]struct{ name, provider string }, len(configs))
	for _, c := range configs {
		configMeta[c.AgentConfigID] = struct{ name, provider string }{c.AgentName, c.Provider}
	}
	accs := map[string]*configAcc{}
	if groupBy == groupByConfig {
		// Config mode emits a (possibly zeroed) row for every config.
		for _, c := range configs {
			key := c.AgentConfigID + "\x00"
			accs[key] = &configAcc{info: groupInfo{key: key, cfgID: c.AgentConfigID}}
		}
	}
	accFor := func(key string) *configAcc {
		if acc, ok := accs[key]; ok {
			return acc
		}
		info, ok := infos[key]
		if !ok {
			return nil
		}
		if _, known := configMeta[info.cfgID]; !known {
			return nil
		}
		acc := &configAcc{info: info}
		accs[key] = acc
		return acc
	}

	for _, a := range aggs {
		if !a.isTerminal || a.lastRun == nil {
			continue
		}
		acc := accFor(groupFor(a.lastRun, a).key)
		if acc == nil {
			continue
		}
		acc.tasksDone++
		acc.totalCost += a.totalCost
		if a.humanTouched {
			acc.humanTouchedTasks++
		}
		acc.reviewComments += a.reviewComments
	}
	// Rework events fold into the numerator of whichever group caused them;
	// the denominator (tasksDone) stays last-run-attributed.
	for _, a := range aggs {
		if !a.isTerminal {
			continue
		}
		for key, rung := range a.groupRung {
			acc := accFor(key)
			if acc == nil {
				continue
			}
			if acc.ladder == nil {
				acc.ladder = map[int64]*ladderRungStat{}
			}
			st := acc.ladder[rung]
			if st == nil {
				st = &ladderRungStat{Rung: rung}
				acc.ladder[rung] = st
			}
			st.TasksDone++
			st.AvgCostToDoneUSD += a.totalCost // summed here, averaged below
		}
		for key, n := range a.reworkGroups {
			if acc := accFor(key); acc != nil {
				acc.reworkEvents += int64(n)
			}
		}
	}
	// Groups that only have escalation data still get a row.
	if groupBy != groupByConfig {
		for key := range escByGroup {
			accFor(key)
		}
	}

	// Display names for dimension values.
	providerNames := map[string]string{}
	repoNames := map[string]string{}
	switch groupBy {
	case groupByProviderConfig:
		pcs, err := h.q.ListProviderConfigs(ctx)
		if err != nil {
			return outcomeQualityResponse{}, err
		}
		for _, pc := range pcs {
			providerNames[pc.ID] = pc.Name
		}
	case groupByRepo:
		rs, err := h.q.ListRepos(ctx)
		if err != nil {
			return outcomeQualityResponse{}, err
		}
		for _, rp := range rs {
			repoNames[rp.ID] = rp.Name
		}
	}

	rows := make([]outcomeQualityConfigRow, 0, len(accs))
	for key, acc := range accs {
		meta := configMeta[acc.info.cfgID]
		row := outcomeQualityConfigRow{
			GroupKey:      key,
			AgentConfigID: acc.info.cfgID,
			AgentName:     meta.name,
			Provider:      meta.provider,
			TasksDone:     acc.tasksDone,
			LowSample:     acc.tasksDone < minSampleTasksForRow,
		}
		switch groupBy {
		case groupByVersion:
			row.Version = acc.info.version
			if acc.info.unversed {
				row.GroupValue = "unversioned"
			} else {
				row.GroupValue = "v" + acc.info.dim
			}
		case groupByProviderConfig:
			row.ProviderConfigID = acc.info.dim
			row.GroupValue = providerNames[acc.info.dim]
			if row.GroupValue == "" {
				row.GroupValue = acc.info.dim
			}
		case groupByRepo:
			row.RepoID = acc.info.dim
			row.GroupValue = repoNames[acc.info.dim]
			if row.GroupValue == "" {
				row.GroupValue = acc.info.dim
			}
		case groupByTaskType:
			row.TaskType = acc.info.dim
			row.GroupValue = acc.info.dim
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
		if esc, ok := escByGroup[key]; ok && esc.finished > 0 {
			row.RunsFinished = esc.finished
			row.EscalationRatePercent = float64(esc.escalated) / float64(esc.finished) * 100
			row.LowSampleEscalation = esc.finished < minSampleSizeForRate
		}
		for _, st := range acc.ladder {
			out := *st
			out.AvgCostToDoneUSD = st.AvgCostToDoneUSD / float64(st.TasksDone)
			row.LadderRungs = append(row.LadderRungs, out)
		}
		sort.Slice(row.LadderRungs, func(i, j int) bool { return row.LadderRungs[i].Rung < row.LadderRungs[j].Rung })
		rows = append(rows, row)
	}

	if groupBy == groupByConfig {
		sort.Slice(rows, func(i, j int) bool { return rows[i].TasksDone > rows[j].TasksDone })
	} else {
		// Keep a config's groups adjacent so before/after rows sit together.
		sort.Slice(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			if a.AgentName != b.AgentName {
				return a.AgentName < b.AgentName
			}
			if a.AgentConfigID != b.AgentConfigID {
				return a.AgentConfigID < b.AgentConfigID
			}
			if groupBy == groupByVersion {
				switch {
				case a.Version == nil && b.Version == nil:
					return false
				case a.Version == nil:
					return false // unversioned last
				case b.Version == nil:
					return true
				}
				return *a.Version > *b.Version
			}
			return a.GroupValue < b.GroupValue
		})
	}

	return outcomeQualityResponse{GroupBy: groupBy, Configs: rows}, nil
}

// precedingRun returns the last run with a config in runs (sorted ascending
// by created_at) whose created_at is <= at, i.e. the run that was in flight
// or most recently finished when this label-history transition happened.
// Returns nil if no such run exists or its config was later deleted
// (agent_config_id NULL).
func precedingRun(runs []gen.ListOutcomeQualityRunsRow, at time.Time) *gen.ListOutcomeQualityRunsRow {
	var last *gen.ListOutcomeQualityRunsRow
	for i := range runs {
		if runs[i].CreatedAt.After(at) {
			break
		}
		if runs[i].AgentConfigID != nil {
			last = &runs[i]
		}
	}
	return last
}
