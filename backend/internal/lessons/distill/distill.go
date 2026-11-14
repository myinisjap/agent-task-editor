// Package distill drafts repo lessons from what went wrong on recent tasks. It
// follows the rule-proposal pattern (ADR 0040): a model may only propose.
// Drafts are stored as repo_lessons rows with status "proposed" and source
// "distiller"; only an active lesson is ever injected into a prompt, so a draft
// is inert until a person accepts it.
package distill

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

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/propose"
	"github.com/myinisjap/agent-task-editor/backend/internal/lessons"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Limits and defaults.
const (
	WindowDays       = 30
	MaxEvidence      = 40
	MaxProposals     = 5
	MaxBody          = lessons.MaxBodyLen
	MaxQuote         = 300
	RunRetentionDays = 90
	ModelTimeout     = 10 * time.Minute
	defaultTick      = time.Hour
)

// Run triggers and statuses (lesson_distill_runs).
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	RunRunning      = "running"
	RunSucceeded    = "succeeded"
	RunFailed       = "failed"
	RunSkipped      = "skipped"

	StatusProposed = "proposed"
	StatusActive   = "active"
	SourceDistill  = "distiller"
)

// Errors returned to API callers.
var (
	ErrRunning     = errors.New("a lesson run is already in progress for this repo")
	ErrNoProvider  = errors.New("no provider config is set for proposals (PUT /settings/autonomy)")
	ErrNoModel     = errors.New("the proposal provider config's provider is not available")
	ErrNotFound    = errors.New("not found")
	ErrNotProposal = errors.New("lesson is not a proposed distiller lesson")
)

// Service runs the distiller and records dismissals.
type Service struct {
	DB *sql.DB
	Q  *gen.Queries
	// ModelFor builds the model for a provider config and system prompt; nil
	// (or a nil result) means the provider cannot be used.
	ModelFor func(pc gen.ProviderConfig, systemPrompt string) propose.Model
	// Tick is how often the scheduler looks for due repos (default 1h).
	Tick time.Duration

	now     func() time.Time
	mu      sync.Mutex
	running map[string]bool
	baseCtx context.Context
}

// New creates a Service.
func New(db *sql.DB, q *gen.Queries, modelFor func(gen.ProviderConfig, string) propose.Model) *Service {
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

type prepared struct {
	run   gen.LessonDistillRun
	model propose.Model
	repo  gen.Repo
}

// begin validates the repo and settings, claims the repo and inserts a running
// row. The caller must call execute (which releases the claim).
func (s *Service) begin(ctx context.Context, repoID, trigger, actor string) (prepared, error) {
	repo, err := s.Q.GetRepo(ctx, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prepared{}, ErrNotFound
		}
		return prepared{}, err
	}
	// The provider config is the one chosen for rule proposals.
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
	var model propose.Model
	if s.ModelFor != nil {
		model = s.ModelFor(pc, SystemPrompt)
	}
	if model == nil {
		return prepared{}, ErrNoModel
	}
	s.mu.Lock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running[repoID] {
		s.mu.Unlock()
		return prepared{}, ErrRunning
	}
	s.running[repoID] = true
	s.mu.Unlock()
	pcID := pc.ID
	run, err := s.Q.CreateLessonDistillRun(ctx, gen.CreateLessonDistillRunParams{
		ID: uuid.NewString(), RepoID: repoID, Trigger: trigger, Actor: actor,
		Status: RunRunning, ProviderConfigID: &pcID, StartedAt: s.clock(),
	})
	if err != nil {
		s.release(repoID)
		return prepared{}, err
	}
	return prepared{run: run, model: model, repo: repo}, nil
}

func (s *Service) release(repoID string) {
	s.mu.Lock()
	delete(s.running, repoID)
	s.mu.Unlock()
}

// Start begins a manual run for a repo and executes it in the background. The
// returned row is still running.
func (s *Service) Start(ctx context.Context, repoID, actor string) (gen.LessonDistillRun, error) {
	p, err := s.begin(ctx, repoID, TriggerManual, actor)
	if err != nil {
		return gen.LessonDistillRun{}, err
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

// RunRepo runs a repo synchronously (scheduler, tests).
func (s *Service) RunRepo(ctx context.Context, repoID, trigger, actor string) (gen.LessonDistillRun, error) {
	p, err := s.begin(ctx, repoID, trigger, actor)
	if err != nil {
		return gen.LessonDistillRun{}, err
	}
	return s.execute(ctx, p), nil
}

type result struct {
	status    string
	evidence  int
	proposals int
	errMsg    string
	comp      propose.Completion
}

func failed(err error) result { return result{status: RunFailed, errMsg: err.Error()} }

func (s *Service) execute(ctx context.Context, p prepared) gen.LessonDistillRun {
	defer s.release(p.run.RepoID)
	log := slog.With("component", "lesson_distill", "repo_id", p.run.RepoID, "run_id", p.run.ID)
	res := s.work(ctx, p)
	fin := s.clock()
	// Finish on a fresh context so a cancelled run is still recorded.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	run, err := s.Q.FinishLessonDistillRun(fctx, gen.FinishLessonDistillRunParams{
		Status: res.status, EvidenceCount: int64(res.evidence), ProposalCount: int64(res.proposals), Error: res.errMsg,
		CostUsd: res.comp.CostUSD, InputTokens: res.comp.InputTokens, OutputTokens: res.comp.OutputTokens,
		FinishedAt: &fin, ID: p.run.ID,
	})
	if err != nil {
		log.Error("finish lesson run", "err", err)
		return p.run
	}
	if res.status == RunFailed {
		log.Warn("lesson run failed", "err", res.errMsg)
	} else {
		log.Info("lesson run finished", "status", res.status, "evidence", res.evidence, "proposals", res.proposals, "cost_usd", res.comp.CostUSD)
	}
	return run
}

// known returns every body a new proposal must not repeat: the repo's and
// global lessons of any status (active, proposed, stale, retired) and the
// dismissed proposals.
func (s *Service) known(ctx context.Context, repoID string) ([]string, error) {
	rows, err := s.Q.ListLessonsForDedupe(ctx, &repoID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, l := range rows {
		out = append(out, l.Body)
	}
	dis, err := s.Q.ListLessonDismissals(ctx, repoID)
	if err != nil {
		return nil, err
	}
	for _, d := range dis {
		out = append(out, d.Body)
	}
	return out, nil
}

func (s *Service) work(ctx context.Context, p prepared) result {
	since := s.clock().AddDate(0, 0, -WindowDays)
	ev, err := GatherEvidence(ctx, s.Q, p.run.RepoID, since)
	if err != nil {
		return failed(err)
	}
	if len(ev) == 0 {
		return result{status: RunSkipped, errMsg: fmt.Sprintf("no evidence in the last %d days", WindowDays)}
	}
	known, err := s.known(ctx, p.run.RepoID)
	if err != nil {
		return failed(err)
	}
	prompt := BuildPrompt(p.repo.Name, known, ev)
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
	kept := 0
	for _, pr := range Filter(ans.Lessons, ev, known) {
		cit, _ := json.Marshal(pr.Citations)
		src := pr.Citations[0].TaskID
		_, err := s.Q.CreateRepoLesson(ctx, gen.CreateRepoLessonParams{
			ID: uuid.NewString(), RepoID: &p.run.RepoID, Kind: pr.Fields.Kind, Body: pr.Fields.Body,
			PathGlobs: pr.Fields.PathGlobs, Stages: pr.Fields.Stages, Citations: string(cit),
			Source: SourceDistill, SourceTaskID: &src, Status: StatusProposed,
		})
		if err != nil {
			r := failed(fmt.Errorf("store lesson: %w", err))
			r.evidence, r.comp = len(ev), comp
			return r
		}
		kept++
	}
	return result{status: RunSucceeded, evidence: len(ev), proposals: kept, comp: comp}
}

// Citation is one task a proposed lesson was drawn from, with the quoted note.
type Citation struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Quote  string `json:"quote"`
}

// Proposal is a validated candidate ready to store.
type Proposal struct {
	Fields    lessons.Fields
	Citations []Citation
}

// Filter validates model candidates server-side: the same validation as a
// human-created lesson, near-duplicates of known bodies (and of earlier
// candidates in the same answer) dropped, candidates citing no real evidence
// dropped, and at most MaxProposals kept. Cited ids that are not in the
// evidence are ignored. Model text is stripped of fence markers.
func Filter(cands []Candidate, ev []Evidence, known []string) []Proposal {
	byTask := map[string]Evidence{}
	for _, e := range ev {
		if _, ok := byTask[e.TaskID]; !ok {
			byTask[e.TaskID] = e
		}
	}
	known = append([]string(nil), known...)
	var out []Proposal
	for _, c := range cands {
		if len(out) >= MaxProposals {
			break
		}
		body := oneLine(stripMarkers(c.Body))
		f, msg := lessons.Validate(strings.TrimSpace(c.Kind), body, c.PathGlobs, c.Stages)
		if msg != "" {
			continue
		}
		var cits []Citation
		seen := map[string]bool{}
		for _, id := range c.TaskIDs {
			id = strings.TrimSpace(id)
			e, ok := byTask[id]
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			cits = append(cits, Citation{
				TaskID: e.TaskID, Title: clip(oneLine(stripMarkers(e.Title)), maxTitleLen), Kind: e.Kind,
				Quote: clip(oneLine(stripMarkers(e.Text)), MaxQuote),
			})
		}
		if len(cits) == 0 {
			continue
		}
		dup := false
		for _, k := range known {
			if NearDuplicate(f.Body, k) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		known = append(known, f.Body)
		out = append(out, Proposal{Fields: f, Citations: cits})
	}
	return out
}

// Dismiss rejects a proposed distiller lesson: its body is remembered so the
// same lesson is not offered again, and the row is removed. One transaction.
func (s *Service) Dismiss(ctx context.Context, lessonID, actor string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.Q.WithTx(tx)
	l, err := q.GetRepoLesson(ctx, lessonID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if l.Status != StatusProposed || l.Source != SourceDistill || l.RepoID == nil {
		return ErrNotProposal
	}
	if err := q.CreateLessonDismissal(ctx, gen.CreateLessonDismissalParams{
		ID: uuid.NewString(), RepoID: *l.RepoID, Body: l.Body, DismissedBy: actor,
	}); err != nil {
		return err
	}
	if _, err := q.DeleteRepoLesson(ctx, lessonID); err != nil {
		return err
	}
	return tx.Commit()
}
