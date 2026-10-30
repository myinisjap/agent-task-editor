// Package propose turns a gate's history (autonomy escapes and human
// rejections) into model-proposed ask rules (ADR 0040). It runs offline, per
// gate, on a schedule or on demand. A proposal is inert until a person accepts
// it, which creates a normal ask rule; nothing here feeds autonomy.Evaluate.
package propose

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/needsme"
	"github.com/myinisjap/agent-task-editor/backend/internal/pathglob"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Limits and defaults.
const (
	WindowDays        = 30
	MinEvidence       = 3
	MaxEvidence       = 40
	MaxCleanApprovals = 500
	MaxProposals      = 5
	MaxFlagIdeas      = 3
	MaxRationale      = 300
	RunRetentionDays  = 90
	ModelTimeout      = 10 * time.Minute
	defaultTick       = time.Hour
)

// Run triggers and statuses (autonomy_proposal_runs).
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	RunRunning      = "running"
	RunSucceeded    = "succeeded"
	RunFailed       = "failed"
	RunSkipped      = "skipped"
	StatusProposed  = "proposed"
	StatusAccepted  = "accepted"
	StatusDismissed = "dismissed"
	KindEscape      = "escape"
	KindRejected    = "rejected"
)

// Errors returned to API callers.
var (
	ErrRunning     = errors.New("a proposal run is already in progress for this gate")
	ErrNoProvider  = errors.New("no provider config is set for rule proposals (PUT /settings/autonomy)")
	ErrNoModel     = errors.New("the proposal provider config's provider is not available")
	ErrNotGate     = errors.New("label is not a human gate")
	ErrNotFound    = errors.New("not found")
	ErrDecided     = errors.New("proposal was already accepted or dismissed")
	ErrDuplicate   = errors.New("an ask rule with the same conditions already exists for this gate")
	ErrInvalidRule = errors.New("proposal is no longer valid")
)

// Completion is one model answer.
type Completion struct {
	Text         string
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64
}

// Model answers one prompt.
type Model interface {
	Complete(ctx context.Context, prompt string) (Completion, error)
}

// FactsFunc recomputes a task's rule facts as of a moment. ok=false when the
// diff is unavailable (facts then carry only the task type).
type FactsFunc func(ctx context.Context, task gen.Task, at time.Time) (autonomy.Facts, bool)

// Service runs proposal jobs and records accept/dismiss decisions.
type Service struct {
	DB *sql.DB
	Q  *gen.Queries
	// ModelFor builds the model for a provider config; nil (or a nil result)
	// means the provider cannot be used.
	ModelFor func(pc gen.ProviderConfig) Model
	// Facts defaults to reviewbrief.DiffAsOf.
	Facts FactsFunc
	// Tick is how often the scheduler looks for due gates (default 1h).
	Tick time.Duration

	now     func() time.Time
	mu      sync.Mutex
	running map[string]bool
	baseCtx context.Context
}

// New creates a Service.
func New(db *sql.DB, q *gen.Queries, modelFor func(gen.ProviderConfig) Model) *Service {
	return &Service{DB: db, Q: q, ModelFor: modelFor}
}

// SetNow overrides the clock (tests).
func (s *Service) SetNow(f func() time.Time) { s.now = f }

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) facts(ctx context.Context, task gen.Task, at time.Time) (autonomy.Facts, bool) {
	if s.Facts != nil {
		return s.Facts(ctx, task, at)
	}
	f := autonomy.Facts{TaskType: task.Type}
	res, err := reviewbrief.DiffAsOf(ctx, s.Q, task, at)
	if err != nil || !res.Available {
		return f, false
	}
	if res.Changed.Files > 0 {
		f.HasDiff = true
		f.Files = res.Paths
		f.ChangedLines = res.Changed.Additions + res.Changed.Deletions
	}
	return f, true
}

func gateKey(wfID, label string) string { return wfID + "\x00" + label }

// gateInfo loads the workflow and checks label is a human gate.
func gateInfo(ctx context.Context, q *gen.Queries, wfID, label string) (gen.Workflow, gen.WorkflowLabel, error) {
	wf, err := q.GetWorkflow(ctx, wfID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return wf, gen.WorkflowLabel{}, ErrNotFound
		}
		return wf, gen.WorkflowLabel{}, err
	}
	labels, err := q.ListWorkflowLabels(ctx, wfID)
	if err != nil {
		return wf, gen.WorkflowLabel{}, err
	}
	transitions, err := q.ListWorkflowTransitions(ctx, wfID)
	if err != nil {
		return wf, gen.WorkflowLabel{}, err
	}
	for _, l := range labels {
		if l.Name == label {
			if !needsme.IsHumanGate(needsme.WorkflowDef{Labels: labels, Transitions: transitions}, label) {
				return wf, l, ErrNotGate
			}
			return wf, l, nil
		}
	}
	return wf, gen.WorkflowLabel{}, ErrNotFound
}

// prepared is a started run with everything execute needs.
type prepared struct {
	run   gen.AutonomyProposalRun
	model Model
	wf    gen.Workflow
	label gen.WorkflowLabel
}

// begin validates the gate and settings, claims the gate and inserts a
// running row. The caller must call execute (which releases the claim).
func (s *Service) begin(ctx context.Context, wfID, label, trigger, actor string) (prepared, error) {
	wf, lbl, err := gateInfo(ctx, s.Q, wfID, label)
	if err != nil {
		return prepared{}, err
	}
	settings, err := s.Q.GetAutonomySettings(ctx)
	if err != nil {
		return prepared{}, err
	}
	if settings.ProposalProviderConfigID == nil || *settings.ProposalProviderConfigID == "" {
		return prepared{}, ErrNoProvider
	}
	pc, err := s.Q.GetProviderConfig(ctx, *settings.ProposalProviderConfigID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prepared{}, ErrNoProvider
		}
		return prepared{}, err
	}
	var model Model
	if s.ModelFor != nil {
		model = s.ModelFor(pc)
	}
	if model == nil {
		return prepared{}, ErrNoModel
	}
	key := gateKey(wfID, label)
	s.mu.Lock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running[key] {
		s.mu.Unlock()
		return prepared{}, ErrRunning
	}
	s.running[key] = true
	s.mu.Unlock()
	pcID := pc.ID
	run, err := s.Q.CreateProposalRun(ctx, gen.CreateProposalRunParams{
		ID: uuid.NewString(), WorkflowID: wfID, Label: label, Trigger: trigger, Actor: actor,
		Status: RunRunning, ProviderConfigID: &pcID, StartedAt: s.clock(),
	})
	if err != nil {
		s.release(key)
		return prepared{}, err
	}
	return prepared{run: run, model: model, wf: wf, label: lbl}, nil
}

func (s *Service) release(key string) {
	s.mu.Lock()
	delete(s.running, key)
	s.mu.Unlock()
}

// Start begins a manual run for a gate and executes it in the background.
// The returned row is still running.
func (s *Service) Start(ctx context.Context, wfID, label, actor string) (gen.AutonomyProposalRun, error) {
	p, err := s.begin(ctx, wfID, label, TriggerManual, actor)
	if err != nil {
		return gen.AutonomyProposalRun{}, err
	}
	s.mu.Lock()
	base := s.baseCtx
	s.mu.Unlock()
	if base == nil {
		base = context.Background()
	}
	go s.execute(base, p)
	return p.run, nil
}

// RunGate runs a gate synchronously (scheduler, tests).
func (s *Service) RunGate(ctx context.Context, wfID, label, trigger, actor string) (gen.AutonomyProposalRun, error) {
	p, err := s.begin(ctx, wfID, label, trigger, actor)
	if err != nil {
		return gen.AutonomyProposalRun{}, err
	}
	return s.execute(ctx, p), nil
}

// result accumulates what execute writes on the run row.
type result struct {
	status    string
	evidence  int
	proposals int
	flagIdeas []FlagIdea
	errMsg    string
	comp      Completion
}

// execute gathers the evidence, asks the model, validates and replays each
// candidate and stores the survivors. It always finishes the run row.
func (s *Service) execute(ctx context.Context, p prepared) gen.AutonomyProposalRun {
	defer s.release(gateKey(p.run.WorkflowID, p.run.Label))
	log := slog.With("component", "autonomy_propose", "workflow_id", p.run.WorkflowID, "label", p.run.Label, "run_id", p.run.ID)
	res := s.work(ctx, p)
	if res.flagIdeas == nil {
		res.flagIdeas = []FlagIdea{}
	}
	ideas, _ := json.Marshal(res.flagIdeas)
	fin := s.clock()
	// Finish on a fresh context so a cancelled run is still recorded.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	run, err := s.Q.FinishProposalRun(fctx, gen.FinishProposalRunParams{
		Status: res.status, EvidenceCount: int64(res.evidence), ProposalCount: int64(res.proposals),
		FlagIdeas: string(ideas), Error: res.errMsg, CostUsd: res.comp.CostUSD,
		InputTokens: res.comp.InputTokens, OutputTokens: res.comp.OutputTokens, FinishedAt: &fin, ID: p.run.ID,
	})
	if err != nil {
		log.Error("finish proposal run", "err", err)
		return p.run
	}
	switch res.status {
	case RunFailed:
		log.Warn("rule proposal run failed", "err", res.errMsg)
	default:
		log.Info("rule proposal run finished", "status", res.status, "evidence", res.evidence, "proposals", res.proposals, "cost_usd", res.comp.CostUSD)
	}
	return run
}

func failed(err error) result { return result{status: RunFailed, errMsg: err.Error()} }

func (s *Service) work(ctx context.Context, p prepared) result {
	wfID, label := p.run.WorkflowID, p.run.Label
	since := s.clock().AddDate(0, 0, -WindowDays)
	ev, escaped, err := s.gatherEvidence(ctx, wfID, label, since)
	if err != nil {
		return failed(err)
	}
	if len(ev) < MinEvidence {
		return result{status: RunSkipped, evidence: len(ev),
			errMsg: fmt.Sprintf("not enough history: %d escapes or rejections in the last %d days (need %d)", len(ev), WindowDays, MinEvidence)}
	}
	existing, err := s.existingRules(ctx, wfID, label)
	if err != nil {
		return failed(err)
	}
	clean, err := s.cleanApprovals(ctx, wfID, label, since, escaped)
	if err != nil {
		return failed(err)
	}
	prompt := BuildPrompt(Gate{Workflow: p.wf.Name, Label: label, Mode: p.label.AutonomyMode, Approved: len(clean)}, ev, existing)
	mctx, cancel := context.WithTimeout(ctx, ModelTimeout)
	comp, err := p.model.Complete(mctx, prompt)
	cancel()
	if err != nil {
		r := failed(fmt.Errorf("model: %w", err))
		r.evidence, r.comp = len(ev), comp
		return r
	}
	ans, err := Parse(comp.Text)
	if err != nil {
		r := failed(err)
		r.evidence, r.comp = len(ev), comp
		return r
	}
	known := map[string]bool{}
	for _, r := range existing {
		known[ruleKey(r)] = true
	}
	kept := 0
	m := pathglob.NewMatcher()
	for i, c := range ans.Rules {
		if i >= MaxProposals {
			break
		}
		rule, ok := s.candidateRule(ctx, c)
		if !ok {
			continue
		}
		key := ruleKey(rule)
		if known[key] {
			continue
		}
		known[key] = true
		rp := replay(rule, ev, clean, m)
		if rp.caught == 0 {
			continue
		}
		if err := s.store(ctx, p, rule, key, clip(oneLine(stripMarkers(c.Rationale)), MaxRationale), rp, len(ev), len(clean)); err != nil {
			r := failed(fmt.Errorf("store proposal: %w", err))
			r.evidence, r.comp = len(ev), comp
			return r
		}
		kept++
	}
	return result{status: RunSucceeded, evidence: len(ev), proposals: kept, flagIdeas: cleanFlagIdeas(ans.FlagIdeas), comp: comp}
}

// candidateRule validates one model candidate into a rule scoped to the gate
// (label is set by the caller on accept). Invalid candidates are dropped.
func (s *Service) candidateRule(ctx context.Context, c Candidate) (autonomy.AskRule, bool) {
	r := autonomy.AskRule{PathGlobs: c.PathGlobs, TaskType: c.TaskType, MinChangedLines: c.MinChangedLines}
	if c.RepoID != nil && *c.RepoID != "" {
		if _, err := s.Q.GetRepo(ctx, *c.RepoID); err != nil {
			return r, false
		}
		id := *c.RepoID
		r.RepoID = &id
	}
	if err := r.Validate(); err != nil {
		return r, false
	}
	return r, true
}

// existingRules are the stored rules that apply at the gate (gate-scoped and
// all-gates, any repo).
func (s *Service) existingRules(ctx context.Context, wfID, label string) ([]autonomy.AskRule, error) {
	rows, err := s.Q.ListAskRules(ctx, gen.ListAskRulesParams{Column1: wfID, Column2: label, Column3: ""})
	if err != nil {
		return nil, err
	}
	out := make([]autonomy.AskRule, 0, len(rows))
	for _, row := range rows {
		out = append(out, ruleFromRow(row))
	}
	return out, nil
}

func ruleFromRow(row gen.AutonomyAskRule) autonomy.AskRule {
	var globs []string
	_ = json.Unmarshal([]byte(row.PathGlobs), &globs)
	r := autonomy.AskRule{ID: row.ID, RepoID: row.RepoID, Label: row.Label, PathGlobs: globs, TaskType: row.TaskType}
	if row.MinChangedLines != nil {
		n := int(*row.MinChangedLines)
		r.MinChangedLines = &n
	}
	_ = r.Validate()
	return r
}

// ProposalRule returns a proposal's conditions as an ask rule scoped to its
// gate.
func ProposalRule(p gen.AutonomyRuleProposal) autonomy.AskRule {
	var globs []string
	_ = json.Unmarshal([]byte(p.PathGlobs), &globs)
	if globs == nil {
		globs = []string{}
	}
	label := p.Label
	r := autonomy.AskRule{RepoID: p.RepoID, Label: &label, PathGlobs: globs, TaskType: p.TaskType}
	if p.MinChangedLines != nil {
		n := int(*p.MinChangedLines)
		r.MinChangedLines = &n
	}
	return r
}

// EvidenceRef is one caught task stored on a proposal.
type EvidenceRef struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

// replayResult is what a candidate would have done over the window.
type replayResult struct {
	caught  int
	cost    int
	unknown int
	refs    []EvidenceRef
}

// replay matches the rule against every evidence task (what it would have
// caught) and every clean approval (what extra asking it would cost), with
// the same matcher Evaluate uses.
func replay(r autonomy.AskRule, ev []Evidence, clean []Approval, m *pathglob.Matcher) replayResult {
	out := replayResult{refs: []EvidenceRef{}}
	inScope := func(repo string) bool { return r.RepoID == nil || *r.RepoID == repo }
	for _, e := range ev {
		if !inScope(e.RepoID) {
			continue
		}
		if r.NeedsDiff() && !e.FactsOK {
			out.unknown++
			continue
		}
		if r.Matches(e.Facts, m) {
			out.caught++
			out.refs = append(out.refs, EvidenceRef{TaskID: e.TaskID, Title: e.Title, Kind: e.Kind, Source: e.Source})
		}
	}
	for _, a := range clean {
		if !inScope(a.RepoID) {
			continue
		}
		if r.NeedsDiff() && !a.FactsOK {
			out.unknown++
			continue
		}
		if r.Matches(a.Facts, m) {
			out.cost++
		}
	}
	return out
}

// store inserts a new proposal or refreshes the one with the same
// conditions. A dismissed proposal stays dismissed; an accepted one is left
// alone.
func (s *Service) store(ctx context.Context, p prepared, r autonomy.AskRule, key, rationale string, rp replayResult, evTotal, cleanTotal int) error {
	refs, _ := json.Marshal(rp.refs)
	globs, _ := json.Marshal(r.PathGlobs)
	var min *int64
	if r.MinChangedLines != nil {
		v := int64(*r.MinChangedLines)
		min = &v
	}
	now := s.clock()
	cur, err := s.Q.GetRuleProposalByKey(ctx, gen.GetRuleProposalByKeyParams{WorkflowID: p.run.WorkflowID, Label: p.run.Label, RuleKey: key})
	switch {
	case err == nil:
		if cur.Status == StatusAccepted {
			return nil
		}
		_, err = s.Q.RefreshRuleProposal(ctx, gen.RefreshRuleProposalParams{
			Rationale: rationale, Evidence: string(refs), Caught: int64(rp.caught), EvidenceTotal: int64(evTotal),
			Cost: int64(rp.cost), CleanApprovals: int64(cleanTotal), Unknown: int64(rp.unknown),
			RunID: &p.run.ID, ProviderConfigID: p.run.ProviderConfigID, UpdatedAt: now, ID: cur.ID,
		})
		return err
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.Q.CreateRuleProposal(ctx, gen.CreateRuleProposalParams{
			ID: uuid.NewString(), WorkflowID: p.run.WorkflowID, Label: p.run.Label, RepoID: r.RepoID,
			PathGlobs: string(globs), TaskType: r.TaskType, MinChangedLines: min, RuleKey: key, Rationale: rationale,
			Evidence: string(refs), Caught: int64(rp.caught), EvidenceTotal: int64(evTotal), Cost: int64(rp.cost),
			CleanApprovals: int64(cleanTotal), Unknown: int64(rp.unknown), RunID: &p.run.ID,
			ProviderConfigID: p.run.ProviderConfigID, CreatedAt: now, UpdatedAt: now,
		})
		return err
	default:
		return err
	}
}

// Accept turns a proposal into an ask rule scoped to its gate, created by
// actor, and marks the proposal accepted. One transaction.
func (s *Service) Accept(ctx context.Context, id, actor string) (gen.AutonomyRuleProposal, gen.AutonomyAskRule, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return gen.AutonomyRuleProposal{}, gen.AutonomyAskRule{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.Q.WithTx(tx)
	p, err := q.GetRuleProposal(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = ErrNotFound
		}
		return p, gen.AutonomyAskRule{}, err
	}
	if p.Status != StatusProposed {
		return p, gen.AutonomyAskRule{}, ErrDecided
	}
	if _, _, err := gateInfo(ctx, q, p.WorkflowID, p.Label); err != nil && !errors.Is(err, ErrNotGate) {
		if errors.Is(err, ErrNotFound) {
			err = fmt.Errorf("%w: workflow or label no longer exists", ErrInvalidRule)
		}
		return p, gen.AutonomyAskRule{}, err
	}
	rule := ProposalRule(p)
	if rule.RepoID != nil {
		if _, err := q.GetRepo(ctx, *rule.RepoID); err != nil {
			return p, gen.AutonomyAskRule{}, fmt.Errorf("%w: repo no longer exists", ErrInvalidRule)
		}
	}
	if err := rule.Validate(); err != nil {
		return p, gen.AutonomyAskRule{}, fmt.Errorf("%w: %s", ErrInvalidRule, err.Error())
	}
	rows, err := q.ListAskRules(ctx, gen.ListAskRulesParams{Column1: p.WorkflowID, Column2: p.Label, Column3: ""})
	if err != nil {
		return p, gen.AutonomyAskRule{}, err
	}
	key := ruleKey(rule)
	for _, row := range rows {
		if ruleKey(ruleFromRow(row)) == key {
			return p, gen.AutonomyAskRule{}, ErrDuplicate
		}
	}
	var min *int64
	if rule.MinChangedLines != nil {
		v := int64(*rule.MinChangedLines)
		min = &v
	}
	globs, _ := json.Marshal(rule.PathGlobs)
	now := s.clock()
	label := p.Label
	created, err := q.CreateAskRule(ctx, gen.CreateAskRuleParams{
		ID: uuid.NewString(), RepoID: rule.RepoID, WorkflowID: p.WorkflowID, Label: &label,
		PathGlobs: string(globs), TaskType: rule.TaskType, MinChangedLines: min, CreatedBy: actor, CreatedAt: now,
	})
	if err != nil {
		return p, gen.AutonomyAskRule{}, err
	}
	n, err := q.DecideRuleProposal(ctx, gen.DecideRuleProposalParams{Status: StatusAccepted, AcceptedRuleID: &created.ID, DecidedBy: actor, DecidedAt: &now, ID: id})
	if err != nil {
		return p, gen.AutonomyAskRule{}, err
	}
	if n == 0 {
		return p, gen.AutonomyAskRule{}, ErrDecided
	}
	p, err = q.GetRuleProposal(ctx, id)
	if err != nil {
		return p, gen.AutonomyAskRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return p, gen.AutonomyAskRule{}, err
	}
	return p, created, nil
}

// Dismiss marks a proposal dismissed. A later run that finds the same
// conditions only refreshes its counts; it is not offered again.
func (s *Service) Dismiss(ctx context.Context, id, actor string) (gen.AutonomyRuleProposal, error) {
	now := s.clock()
	n, err := s.Q.DecideRuleProposal(ctx, gen.DecideRuleProposalParams{Status: StatusDismissed, DecidedBy: actor, DecidedAt: &now, ID: id})
	if err != nil {
		return gen.AutonomyRuleProposal{}, err
	}
	p, err := s.Q.GetRuleProposal(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return p, ErrNotFound
		}
		return p, err
	}
	if n == 0 {
		return p, ErrDecided
	}
	return p, nil
}
