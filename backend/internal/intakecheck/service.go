package intakecheck

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Triggers.
const (
	TriggerManual = "manual"
	TriggerAuto   = "auto"
)

// Limits.
const (
	MaxQuestions     = 3
	maxQuestionLen   = 300
	candidateLimit   = 500
	ModelTimeout     = 10 * time.Minute
	autoLookback     = 24 * time.Hour
	autoBatch        = 3
	defaultTick      = time.Minute
	maxLessons       = 20
	maxDescPromptLen = 6000
)

// Errors returned to API callers.
var (
	ErrDisabled   = errors.New("the intake assistant is off (PUT /settings/intake-assistant)")
	ErrNoProvider = errors.New("no provider config is set for the intake assistant (PUT /settings/intake-assistant)")
	ErrNoModel    = errors.New("the intake assistant provider config's provider is not available")
	ErrRunning    = errors.New("an intake check is already in progress for this task")
	ErrNoTask     = errors.New("task not found")
)

// Completion is one model answer.
type Completion struct {
	Text         string
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64
}

// Model answers one prompt: no tools, no shell, scratch directory.
type Model interface {
	Complete(ctx context.Context, prompt string) (Completion, error)
}

// Service runs assistant checks. It only ever writes task_intake_checks rows;
// it never touches the task itself.
type Service struct {
	Q     *gen.Queries
	Store Store
	// ModelFor builds the model for a provider config; nil (or a nil result)
	// means the provider cannot be used.
	ModelFor func(pc gen.ProviderConfig) Model
	Tick     time.Duration

	mu      sync.Mutex
	running map[string]bool
	baseCtx context.Context
}

// New creates a Service.
func New(db *sql.DB, q *gen.Queries, modelFor func(gen.ProviderConfig) Model) *Service {
	return &Service{Q: q, Store: Store{DB: db}, ModelFor: modelFor}
}

// Duplicates is the deterministic layer: open, non-archived tasks in the same
// repo that look like the task. No model call; never stored.
func (s *Service) Duplicates(ctx context.Context, t gen.Task) []Match {
	cands, err := s.Store.OpenCandidates(ctx, t.RepoID, t.ID, candidateLimit)
	if err != nil {
		return []Match{}
	}
	return Find(Doc{ID: t.ID, Title: t.Title, Description: t.Description}, cands)
}

// Similar finds open tasks in a repo similar to a title typed in the New Task
// modal.
func (s *Service) Similar(ctx context.Context, repoID, q string) []Match {
	if strings.TrimSpace(q) == "" {
		return []Match{}
	}
	cands, err := s.Store.OpenCandidates(ctx, repoID, "", candidateLimit)
	if err != nil {
		return []Match{}
	}
	return Find(Doc{Title: q}, cands)
}

type prepared struct {
	check Check
	model Model
	task  gen.Task
}

func (s *Service) begin(ctx context.Context, taskID, trigger string) (prepared, error) {
	task, err := s.Q.GetTask(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prepared{}, ErrNoTask
		}
		return prepared{}, err
	}
	set, err := s.Store.GetSettings(ctx)
	if err != nil {
		return prepared{}, err
	}
	if !set.Enabled {
		return prepared{}, ErrDisabled
	}
	if set.ProviderConfigID == nil {
		return prepared{}, ErrNoProvider
	}
	pc, err := s.Q.GetProviderConfig(ctx, *set.ProviderConfigID)
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
	s.mu.Lock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running[taskID] {
		s.mu.Unlock()
		return prepared{}, ErrRunning
	}
	s.running[taskID] = true
	s.mu.Unlock()
	pcID := pc.ID
	c := Check{TaskID: taskID, Status: StatusRunning, Trigger: trigger, ProviderConfigID: &pcID, StartedAt: time.Now().UTC(),
		Questions: []string{}, DuplicateIDs: []string{}}
	if err := s.Store.SaveCheck(ctx, c); err != nil {
		s.release(taskID)
		return prepared{}, err
	}
	return prepared{check: c, model: model, task: task}, nil
}

func (s *Service) release(taskID string) {
	s.mu.Lock()
	delete(s.running, taskID)
	s.mu.Unlock()
}

// Start begins a check and runs it in the background; the returned row is
// still running.
func (s *Service) Start(ctx context.Context, taskID, trigger string) (Check, error) {
	p, err := s.begin(ctx, taskID, trigger)
	if err != nil {
		return Check{}, err
	}
	s.mu.Lock()
	base := s.baseCtx
	s.mu.Unlock()
	if base == nil {
		base = context.Background()
	}
	go s.execute(base, p)
	return p.check, nil
}

// RunNow runs a check synchronously (tests, auto sweep).
func (s *Service) RunNow(ctx context.Context, taskID, trigger string) (Check, error) {
	p, err := s.begin(ctx, taskID, trigger)
	if err != nil {
		return Check{}, err
	}
	return s.execute(ctx, p), nil
}

func (s *Service) execute(ctx context.Context, p prepared) Check {
	defer s.release(p.check.TaskID)
	c := p.check
	fail := func(msg string) Check {
		c.Status, c.Error = StatusFailed, msg
		now := time.Now().UTC()
		c.FinishedAt = &now
		if err := s.Store.SaveCheck(context.WithoutCancel(ctx), c); err != nil {
			slog.Warn("intakecheck: save failed check", "task_id", c.TaskID, "err", err)
		}
		return c
	}
	cands := s.Duplicates(ctx, p.task)
	in, err := s.promptInput(ctx, p.task, cands)
	if err != nil {
		return fail(err.Error())
	}
	rctx, cancel := context.WithTimeout(ctx, ModelTimeout)
	defer cancel()
	comp, err := p.model.Complete(rctx, BuildPrompt(in))
	c.CostUSD, c.InputTokens, c.OutputTokens = comp.CostUSD, comp.InputTokens, comp.OutputTokens
	if err != nil {
		return fail("model run failed: " + err.Error())
	}
	ans, err := ParseAnswer(comp.Text, in)
	if err != nil {
		return fail(err.Error())
	}
	c.Status, c.Verdict, c.Questions = StatusDone, ans.Verdict, ans.Questions
	c.SuggestedType, c.SuggestedWorkflowID, c.DuplicateIDs = ans.SuggestedType, ans.SuggestedWorkflowID, ans.DuplicateIDs
	now := time.Now().UTC()
	c.FinishedAt = &now
	if err := s.Store.SaveCheck(context.WithoutCancel(ctx), c); err != nil {
		slog.Warn("intakecheck: save check", "task_id", c.TaskID, "err", err)
	}
	return c
}

func (s *Service) promptInput(ctx context.Context, t gen.Task, cands []Match) (PromptInput, error) {
	in := PromptInput{Title: t.Title, Description: t.Description, Type: t.Type, Candidates: cands}
	repoID := t.RepoID
	if lessons, err := s.Q.ListActiveLessonsForRepo(ctx, &repoID); err == nil {
		for i, l := range lessons {
			if i >= maxLessons {
				break
			}
			in.Lessons = append(in.Lessons, l.Body)
		}
	}
	wfs, err := s.Q.ListWorkflows(ctx)
	if err != nil {
		return in, fmt.Errorf("load workflows: %w", err)
	}
	if len(wfs) > 1 {
		for _, w := range wfs {
			in.Workflows = append(in.Workflows, WorkflowOpt{ID: w.ID, Name: w.Name})
		}
	}
	return in, nil
}

// Run is the background loop: when the assistant and its auto mode are on, it
// checks recent tasks parked on an agent_ignore label (e.g. not_ready) that
// have no check yet. This covers every creation path (UI, API, MCP, issue
// import, webhook, schedule) without hooking each one.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()
	tick := s.Tick
	if tick <= 0 {
		tick = defaultTick
	}
	// A restart can leave rows stuck in running.
	_, _ = s.Store.DB.ExecContext(ctx, `UPDATE task_intake_checks SET status='failed', error='interrupted', finished_at=CURRENT_TIMESTAMP WHERE status='running'`)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep(ctx)
		}
	}
}

// Sweep does one automatic pass.
func (s *Service) Sweep(ctx context.Context) {
	set, err := s.Store.GetSettings(ctx)
	if err != nil || !set.Enabled || !set.AutoCheck || set.ProviderConfigID == nil {
		return
	}
	ids, err := s.Store.UncheckedParked(ctx, time.Now().Add(-autoLookback), autoBatch)
	if err != nil {
		return
	}
	for _, id := range ids {
		if _, err := s.Start(ctx, id, TriggerAuto); err != nil && !errors.Is(err, ErrRunning) {
			slog.Warn("intakecheck: auto check", "task_id", id, "err", err)
			// Record the failure so the task is not retried every tick.
			now := time.Now().UTC()
			_ = s.Store.SaveCheck(ctx, Check{TaskID: id, Status: StatusFailed, Trigger: TriggerAuto, Error: err.Error(),
				StartedAt: now, FinishedAt: &now, Questions: []string{}, DuplicateIDs: []string{}})
		}
	}
}

// Answer is the validated model output.
type Answer struct {
	Verdict             string
	Questions           []string
	SuggestedType       string
	SuggestedWorkflowID string
	DuplicateIDs        []string
}

var validTypes = map[string]bool{"feature": true, "bug": true, "chore": true, "spike": true}

// ParseAnswer extracts and validates the model's answer block. Anything that
// is not allowed (unknown type or workflow, ids that were not candidates) is
// dropped rather than trusted.
func ParseAnswer(text string, in PromptInput) (Answer, error) {
	end := strings.LastIndex(text, outputEnd)
	if end < 0 {
		return Answer{}, errors.New("model returned no answer block")
	}
	begin := strings.LastIndex(text[:end], outputBegin)
	if begin < 0 {
		return Answer{}, errors.New("model returned no answer block")
	}
	raw := strings.TrimSpace(text[begin+len(outputBegin) : end])
	var a struct {
		Verdict             string   `json:"verdict"`
		Questions           []string `json:"questions"`
		SuggestedType       string   `json:"suggested_type"`
		SuggestedWorkflowID string   `json:"suggested_workflow_id"`
		Duplicates          []string `json:"duplicates"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return Answer{}, errors.New("model answer is not valid JSON")
	}
	out := Answer{Questions: []string{}, DuplicateIDs: []string{}}
	switch a.Verdict {
	case VerdictReady, VerdictNeedsDetail:
		out.Verdict = a.Verdict
	default:
		return Answer{}, fmt.Errorf("model answer has an unknown verdict %q", a.Verdict)
	}
	for _, q := range a.Questions {
		q = clip(oneLine(q), maxQuestionLen)
		if q != "" && len(out.Questions) < MaxQuestions {
			out.Questions = append(out.Questions, q)
		}
	}
	if out.Verdict == VerdictReady {
		out.Questions = []string{}
	}
	if validTypes[a.SuggestedType] && a.SuggestedType != in.Type {
		out.SuggestedType = a.SuggestedType
	}
	for _, w := range in.Workflows {
		if w.ID == a.SuggestedWorkflowID {
			out.SuggestedWorkflowID = w.ID
		}
	}
	seen := map[string]bool{}
	for _, id := range a.Duplicates {
		for _, c := range in.Candidates {
			if c.ID == id && !seen[id] {
				seen[id] = true
				out.DuplicateIDs = append(out.DuplicateIDs, id)
			}
		}
	}
	return out, nil
}
