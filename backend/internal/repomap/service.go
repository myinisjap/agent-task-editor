package repomap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Run triggers and statuses (repo_map_runs).
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	RunRunning      = "running"
	RunSucceeded    = "succeeded"
	RunFailed       = "failed"
	RunSkipped      = "skipped"
)

const defaultTick = time.Hour

// Errors returned to API callers.
var (
	ErrNotFound    = errors.New("not found")
	ErrDisabled    = errors.New("repo map is not enabled for this repo")
	ErrNoProvider  = errors.New("no provider config is set for this repo's map")
	ErrNoModel     = errors.New("the repo map provider config's provider is not available")
	ErrRunning     = errors.New("a repo map run is already in progress for this repo")
	ErrCostCeiling = errors.New("the global cost ceiling is reached; no new runs are started")
	ErrNoRepoPath  = errors.New("repo has no usable local clone")
)

// Service generates and serves repo maps.
type Service struct {
	DB *sql.DB
	Q  *gen.Queries
	// GeneratorFor builds the generator for a provider config; nil (or a nil
	// result) means the provider cannot be used.
	GeneratorFor func(pc gen.ProviderConfig) Generator
	// CostTripped reports whether a global cost ceiling is currently hit
	// (agent.Dispatcher.GlobalCostStatus().Tripped). Nil means never.
	CostTripped func() bool
	// Tick is how often the scheduler looks for due repos (default 1h).
	Tick time.Duration

	now     func() time.Time
	mu      sync.Mutex
	running map[string]bool
	baseCtx context.Context
}

// New creates a Service.
func New(db *sql.DB, q *gen.Queries, generatorFor func(gen.ProviderConfig) Generator) *Service {
	return &Service{DB: db, Q: q, GeneratorFor: generatorFor}
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
	run  gen.RepoMapRun
	repo gen.Repo
	gen  Generator
}

func (s *Service) release(repoID string) {
	s.mu.Lock()
	delete(s.running, repoID)
	s.mu.Unlock()
}

func (s *Service) begin(ctx context.Context, repoID, trigger, actor string) (prepared, error) {
	repo, err := s.Q.GetRepo(ctx, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prepared{}, ErrNotFound
		}
		return prepared{}, err
	}
	cfg, err := s.Q.GetRepoMapConfig(ctx, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prepared{}, ErrDisabled
		}
		return prepared{}, err
	}
	if cfg.Enabled == 0 {
		return prepared{}, ErrDisabled
	}
	if cfg.ProviderConfigID == nil || *cfg.ProviderConfigID == "" {
		return prepared{}, ErrNoProvider
	}
	pc, err := s.Q.GetProviderConfig(ctx, *cfg.ProviderConfigID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return prepared{}, ErrNoProvider
		}
		return prepared{}, err
	}
	var g Generator
	if s.GeneratorFor != nil {
		g = s.GeneratorFor(pc)
	}
	if g == nil {
		return prepared{}, ErrNoModel
	}
	if s.CostTripped != nil && s.CostTripped() {
		return prepared{}, ErrCostCeiling
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
	run, err := s.Q.CreateRepoMapRun(ctx, gen.CreateRepoMapRunParams{
		ID: uuid.NewString(), RepoID: repoID, Trigger: trigger, Actor: actor,
		Status: RunRunning, ProviderConfigID: &pcID, StartedAt: s.clock(),
	})
	if err != nil {
		s.release(repoID)
		return prepared{}, err
	}
	return prepared{run: run, repo: repo, gen: g}, nil
}

// Start begins a manual run and executes it in the background; the returned
// row is still running.
func (s *Service) Start(ctx context.Context, repoID, actor string) (gen.RepoMapRun, error) {
	p, err := s.begin(ctx, repoID, TriggerManual, actor)
	if err != nil {
		return gen.RepoMapRun{}, err
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

// RunRepo runs synchronously (scheduler, tests).
func (s *Service) RunRepo(ctx context.Context, repoID, trigger, actor string) (gen.RepoMapRun, error) {
	p, err := s.begin(ctx, repoID, trigger, actor)
	if err != nil {
		return gen.RepoMapRun{}, err
	}
	return s.execute(ctx, p), nil
}

type outcome struct {
	status string
	sha    string
	pages  []Page
	err    string
	comp   Completion
}

func (s *Service) execute(ctx context.Context, p prepared) gen.RepoMapRun {
	defer s.release(p.repo.ID)
	log := slog.With("component", "repomap", "repo_id", p.repo.ID, "run_id", p.run.ID)
	out := s.work(ctx, p)
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	// Pages first so a finished run never lacks them.
	if out.status == RunSucceeded {
		for i, pg := range out.pages {
			if _, err := s.Q.CreateRepoMapPage(fctx, gen.CreateRepoMapPageParams{
				ID: uuid.NewString(), RepoID: p.repo.ID, RunID: p.run.ID, Slug: pg.Slug, Title: pg.Title,
				Body: pg.Body, CommitSha: out.sha, Position: int64(i),
			}); err != nil {
				out.status, out.err = RunFailed, "store page: "+err.Error()
				break
			}
		}
	}
	fin := s.clock()
	run, err := s.Q.FinishRepoMapRun(fctx, gen.FinishRepoMapRunParams{
		Status: out.status, CommitSha: out.sha, PageCount: int64(len(out.pages)), CharCount: int64(TotalChars(out.pages)),
		Error: out.err, CostUsd: out.comp.CostUSD, InputTokens: out.comp.InputTokens,
		OutputTokens: out.comp.OutputTokens, FinishedAt: &fin, ID: p.run.ID,
	})
	if err != nil {
		log.Error("finish repo map run", "err", err)
		return p.run
	}
	if err := s.Q.PruneRepoMapPages(fctx, gen.PruneRepoMapPagesParams{RepoID: p.repo.ID, Limit: KeepVersions}); err != nil {
		log.Warn("prune repo map pages", "err", err)
	}
	log.Info("repo map run finished", "status", out.status, "pages", len(out.pages), "cost_usd", out.comp.CostUSD, "err", out.err)
	return run
}

func fail(err error) outcome { return outcome{status: RunFailed, err: err.Error()} }

func (s *Service) work(ctx context.Context, p prepared) outcome {
	if !dirExists(p.repo.Path) {
		return fail(ErrNoRepoPath)
	}
	// Best-effort refresh of the remote refs so the map reflects the tip.
	fctx, cancel := context.WithTimeout(ctx, time.Minute)
	fetch(fctx, p.repo.Path)
	cancel()
	br, err := DefaultBranch(ctx, p.repo.Path)
	if err != nil {
		return fail(err)
	}
	co, err := NewCheckout(ctx, p.repo.Path, br.SHA)
	if err != nil {
		return fail(err)
	}
	defer co.Close()
	if !co.IsReadOnly() && !runsAsRoot() {
		return outcome{status: RunFailed, sha: br.SHA, err: "scratch checkout is writable; refusing to run"}
	}
	docs := ExistingDocs(co.Dir)
	gctx, cancel := context.WithTimeout(ctx, GenerateTimeout)
	comp, err := p.gen.Generate(gctx, co.Dir, BuildPrompt(p.repo.Name, br.SHA, docs))
	cancel()
	if err != nil {
		o := fail(fmt.Errorf("generate: %w", err))
		o.sha, o.comp = br.SHA, comp
		return o
	}
	pages := EnforceCaps(AddDocPointers(Parse(comp.Text), docs))
	if !Required(pages) {
		o := fail(errors.New("model output is missing the overview or layout page"))
		o.sha, o.comp = br.SHA, comp
		return o
	}
	return outcome{status: RunSucceeded, sha: br.SHA, pages: pages, comp: comp}
}

// Map is the stored map plus its staleness.
type Map struct {
	Pages         []gen.RepoMapPage
	Run           *gen.RepoMapRun // the successful run the pages came from
	CommitsBehind int             // -1 unknown
	LayoutChanged bool
	HeadSHA       string
}

// Current loads the latest successful map and computes how far the default
// branch has moved since its SHA. Pages is empty when no map exists.
func (s *Service) Current(ctx context.Context, repoID string) (Map, error) {
	var m Map
	run, err := s.Q.GetLatestSuccessfulRepoMapRun(ctx, repoID)
	if errors.Is(err, sql.ErrNoRows) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	m.Run = &run
	if m.Pages, err = s.Q.ListRepoMapPagesForRun(ctx, run.ID); err != nil {
		return m, err
	}
	m.CommitsBehind = -1
	repo, err := s.Q.GetRepo(ctx, repoID)
	if err != nil || !dirExists(repo.Path) {
		return m, nil
	}
	if br, err := DefaultBranch(ctx, repo.Path); err == nil {
		m.HeadSHA = br.SHA
		m.CommitsBehind = CommitsBehind(ctx, repo.Path, run.CommitSha, br.Ref)
		if m.CommitsBehind != 0 {
			m.LayoutChanged = LayoutChanged(ctx, repo.Path, run.CommitSha, br.Ref)
		}
	}
	return m, nil
}

// PromptSection returns the REPO MAP prompt section for a run of an agent
// config on label in repoID, or "" when the repo has no enabled map, the
// label is not flagged, the agent opted out, or no map exists yet.
func (s *Service) PromptSection(ctx context.Context, repoID, label, agentConfigID string) string {
	cfg, err := s.Q.GetRepoMapConfig(ctx, repoID)
	if err != nil || cfg.Enabled == 0 {
		return ""
	}
	g, err := s.Q.GetRepoMapGlobal(ctx)
	if err != nil {
		return ""
	}
	if !ShouldInject(g.InjectLabels, label) || AgentExcluded(g.ExcludedAgentIds, agentConfigID) {
		return ""
	}
	run, err := s.Q.GetLatestSuccessfulRepoMapRun(ctx, repoID)
	if err != nil {
		return ""
	}
	rows, err := s.Q.ListRepoMapPagesForRun(ctx, run.ID)
	if err != nil {
		return ""
	}
	pages := make([]Page, 0, len(rows))
	for _, r := range rows {
		pages = append(pages, Page{Slug: r.Slug, Title: r.Title, Body: r.Body})
	}
	behind := -1
	if repo, err := s.Q.GetRepo(ctx, repoID); err == nil && dirExists(repo.Path) {
		if br, err := DefaultBranch(ctx, repo.Path); err == nil {
			behind = CommitsBehind(ctx, repo.Path, run.CommitSha, br.Ref)
		}
	}
	return InjectionSection(pages, run.CommitSha, behind)
}

// Run marks runs left running by a restart as failed, then refreshes due
// repos every Tick until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()
	now := s.clock()
	if err := s.Q.FailRunningRepoMapRuns(ctx, &now); err != nil {
		slog.Warn("repomap: recover running runs", "err", err)
	}
	tick := s.Tick
	if tick <= 0 {
		tick = defaultTick
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.RunDue(ctx)
		}
	}
}

// RunDue refreshes every enabled repo whose last run is older than its
// interval and whose map is stale by the NeedsRefresh rule. Errors are
// logged, never fatal.
func (s *Service) RunDue(ctx context.Context) {
	cfgs, err := s.Q.ListEnabledRepoMapConfigs(ctx)
	if err != nil {
		slog.Warn("repomap: list configs", "err", err)
		return
	}
	for _, cfg := range cfgs {
		if ctx.Err() != nil {
			return
		}
		if cfg.RefreshIntervalHours <= 0 || cfg.ProviderConfigID == nil {
			continue
		}
		interval := time.Duration(cfg.RefreshIntervalHours) * time.Hour
		if last, err := s.Q.GetLatestRepoMapRun(ctx, cfg.RepoID); err == nil && s.clock().Sub(last.StartedAt) < interval {
			continue
		}
		due, err := s.Due(ctx, cfg)
		if err != nil || !due {
			continue
		}
		if _, err := s.RunRepo(ctx, cfg.RepoID, TriggerSchedule, "schedule"); err != nil {
			if errors.Is(err, ErrCostCeiling) {
				slog.Info("repomap: scheduled refresh skipped, cost ceiling reached")
				return
			}
			if !errors.Is(err, ErrRunning) {
				slog.Warn("repomap: scheduled run", "repo_id", cfg.RepoID, "err", err)
			}
		}
	}
}

// Due applies the staleness rule to a repo's config.
func (s *Service) Due(ctx context.Context, cfg gen.RepoMapConfig) (bool, error) {
	m, err := s.Current(ctx, cfg.RepoID)
	if err != nil {
		return false, err
	}
	if m.Run == nil {
		return true, nil
	}
	return NeedsRefresh(true, m.CommitsBehind, int(cfg.MinCommitsBehind), m.LayoutChanged), nil
}
