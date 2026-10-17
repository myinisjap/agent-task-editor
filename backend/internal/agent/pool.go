package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent/runtime"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// runtimePrep is the seam Pool.prepareRuntime calls to install a repo's
// pinned toolchains (mise install / uv venv). A package-level var (mirroring
// worktree.go's gitRunner) so tests can substitute a fake that doesn't
// require mise/uv on PATH, e.g. to verify the sweep/dispatch path never
// blocks on a slow real install.
var runtimePrep = runtime.Prep

// Publisher broadcasts events to connected WebSocket clients.
type Publisher interface {
	Publish(eventType string, payload any)
}

// Job is a single unit of work submitted to the pool.
type Job struct {
	RunID    string
	Provider Provider
	Input    RunInput
	// SetupWorktree, when non-nil, provisions the task's git worktree. The
	// pool worker calls it before toolchain prep and the provider, so the
	// network-bound `git fetch` never runs in the dispatcher's sweep. On
	// success the worker fills in Input.RepoPath, Input.Task.Branch and
	// Input.Runtime.WorktreeDir from the result. Nil means the worktree already
	// exists and Input.RepoPath is final.
	SetupWorktree func(ctx context.Context) (WorktreeResult, error)
}

// WorktreeResult is what Job.SetupWorktree reports about the provisioned
// worktree.
type WorktreeResult struct {
	Path   string
	Branch string
}

// Pool manages a bounded set of concurrent agent runs.
type Pool struct {
	maxWorkers int
	jobs       chan Job
	db         *sql.DB
	q          *gen.Queries
	engine     *workflow.Engine
	pub        Publisher
	wg         sync.WaitGroup
	// RateLimits tracks per-agent-config rate-limit blocks. Optional — no-op when nil.
	RateLimits *RateLimitRegistry
	// ProviderPauses is the provider circuit breaker, tripped when a run is
	// classified ClassAuth. Optional — no-op when nil.
	ProviderPauses *ProviderPauseRegistry
	// GitName/GitEmail are used for safety-net commits when the container has no git identity.
	GitName  string
	GitEmail string
	// Subtasks coordinates child→parent merge-back (optional — nil disables it).
	// Used here to skip pushing child branches and to flush/resolve merges once a
	// parent's run completes.
	Subtasks *SubtaskCoordinator
	// Verifier runs the repo verify command when a run reports success (see
	// verify.go). Optional: with it nil, a repo that has a verify command
	// configured fails closed (the task is escalated to a human).
	Verifier Verifier

	// running tracks the cancel func for each in-flight run so a human can stop
	// a runaway agent (see Cancel). Populated in run(), removed when it returns.
	mu      sync.Mutex
	running map[string]*runControl

	// draining is set by Shutdown: Submit refuses new jobs and workers stop
	// picking queued ones. stop wakes idle workers; hardStop is set once the
	// drain window has expired so a run registering late is cancelled too.
	draining atomic.Bool
	hardStop atomic.Bool
	stop     chan struct{}
	stopOnce sync.Once
	// ShutdownGrace bounds how long Shutdown waits, after cancelling the
	// remaining runs, for them to record their interrupted status. Defaults
	// to 30s; a field so tests can shorten it.
	ShutdownGrace time.Duration
}

// CancelReason distinguishes why a run was cancelled, so handleCancelled can
// pick an accurate note and decide whether the task should be left paused
// (a human deliberately stopped it — don't silently retry what they killed)
// or left resumable (an infrastructure constraint stopped it — the task
// itself did nothing wrong, so it should be eligible for re-dispatch like
// any other transient failure).
type CancelReason int

const (
	// CancelReasonUser is a human-requested stop via the API. Pauses the task.
	CancelReasonUser CancelReason = iota
	// CancelReasonResourceConstraint is memguard killing the run because
	// container memory crossed its critical threshold — not the task's
	// fault. Leaves the task unpaused so it's re-dispatched normally.
	CancelReasonResourceConstraint
	// CancelReasonShutdown is the server shutting down (drain window expired,
	// or the root context was cancelled). The run ends "interrupted", not
	// failed or cancelled: it consumes no retry budget, leaves the task
	// unpaused with its lock released, and re-dispatches after restart.
	CancelReasonShutdown
)

func (r CancelReason) note() string {
	switch r {
	case CancelReasonShutdown:
		return "Run interrupted by server shutdown. Not a failure of this task: it will be re-dispatched (resuming the prior session when supported) after restart."
	case CancelReasonResourceConstraint:
		return "Run stopped: container was low on memory (likely a runaway subprocess, e.g. a test run). Not a failure of this task — it will be retried."
	default:
		return "Run cancelled by user."
	}
}

func (r CancelReason) String() string {
	switch r {
	case CancelReasonShutdown:
		return "shutdown"
	case CancelReasonResourceConstraint:
		return "resource_constraint"
	default:
		return "user"
	}
}

// runControl carries the per-run cancellation handle plus a flag distinguishing
// a human-requested stop from an incidental context cancellation (e.g. pool
// shutdown), so the pool can mark the run "cancelled" rather than "failed".
type runControl struct {
	cancel    context.CancelFunc
	cancelled atomic.Bool
	reason    atomic.Int32
	startedAt time.Time
}

// NewPool creates a new pool. Call Start to begin accepting jobs.
func NewPool(maxWorkers int, db *sql.DB, engine *workflow.Engine, pub Publisher) *Pool {
	metrics.PoolMaxWorkers.Set(float64(maxWorkers))
	return &Pool{
		maxWorkers: maxWorkers,
		jobs:       make(chan Job, maxWorkers*4),
		db:         db,
		q:          gen.New(db),
		engine:     engine,
		pub:        pub,
		running:    make(map[string]*runControl),
		stop:       make(chan struct{}),

		ShutdownGrace: 30 * time.Second,
	}
}

// Draining reports whether Shutdown has begun, i.e. the pool no longer
// accepts new jobs.
func (p *Pool) Draining() bool {
	return p.draining.Load()
}

// Shutdown stops the pool gracefully and blocks until every worker has
// exited. It stops accepting jobs (Submit returns false), records jobs still
// queued but never started as interrupted, and lets in-flight runs keep going
// for up to drain. Runs still going after that are cancelled with
// CancelReasonShutdown, so they end "interrupted" with their dispatch lock
// released, and are given ShutdownGrace to record that. Callers must keep the
// pool's context alive (and the DB open) until Shutdown returns, and only then
// cancel it. Safe to call more than once.
func (p *Pool) Shutdown(drain time.Duration) {
	p.draining.Store(true)
	p.stopOnce.Do(func() { close(p.stop) })

	// Queued jobs hold a pending run row and the task lock but never started.
	for {
		select {
		case job := <-p.jobs:
			p.handleCancelled(job, Result{}, time.Now(), CancelReasonShutdown)
			continue
		default:
		}
		break
	}
	metrics.PoolQueueDepth.Set(0)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(drain)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}

	p.hardStop.Store(true)
	p.mu.Lock()
	ids := make([]string, 0, len(p.running))
	for id := range p.running {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	slog.Warn("pool: drain window expired, interrupting remaining runs", "component", "pool", "count", len(ids))
	for _, id := range ids {
		p.cancelWithReason(id, CancelReasonShutdown)
	}

	grace := time.NewTimer(p.ShutdownGrace)
	defer grace.Stop()
	select {
	case <-done:
	case <-grace.C:
		slog.Warn("pool: runs did not stop within the shutdown grace period; boot recovery will mark them interrupted", "component", "pool")
	}
}

// cancelledBy reports whether the run's context was cancelled and why: an
// explicit Cancel* call carries its own reason, while a context cancelled
// without one is the pool's parent context going away (process shutdown).
func cancelledBy(rc *runControl, runCtx context.Context) (CancelReason, bool) {
	if rc.cancelled.Load() {
		return CancelReason(rc.reason.Load()), true
	}
	if runCtx.Err() != nil {
		return CancelReasonShutdown, true
	}
	return 0, false
}

// Cancel signals the in-flight run identified by runID to stop, as a
// human-requested stop (see CancelReasonUser) — the task is left paused
// afterward. Returns false if no such run is currently active (already
// finished, never started, or running on a different server instance).
func (p *Pool) Cancel(runID string) bool {
	return p.cancelWithReason(runID, CancelReasonUser)
}

// CancelForResourceConstraint is like Cancel but marks the run as stopped by
// an infrastructure constraint (see CancelReasonResourceConstraint) rather
// than a human request — the task is left unpaused and eligible for normal
// re-dispatch. Used by the memguard watchdog.
func (p *Pool) CancelForResourceConstraint(runID string) bool {
	return p.cancelWithReason(runID, CancelReasonResourceConstraint)
}

func (p *Pool) cancelWithReason(runID string, reason CancelReason) bool {
	p.mu.Lock()
	rc := p.running[runID]
	p.mu.Unlock()
	if rc == nil {
		return false
	}
	rc.reason.Store(int32(reason))
	rc.cancelled.Store(true)
	rc.cancel()
	return true
}

// registerRun records the cancel handle for a run and returns its control block.
func (p *Pool) registerRun(runID string, cancel context.CancelFunc) *runControl {
	rc := &runControl{cancel: cancel, startedAt: time.Now()}
	p.mu.Lock()
	p.running[runID] = rc
	p.mu.Unlock()
	return rc
}

// OldestRunID returns the run ID that has been in flight the longest, or ""
// if nothing is running. Used by the memory watchdog to pick a victim when
// container memory is critical — the oldest run is the best guess for a
// stuck/runaway one, and killing it (rather than everything) keeps the blast
// radius as small as possible.
func (p *Pool) OldestRunID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var oldestID string
	var oldestAt time.Time
	for id, rc := range p.running {
		if oldestID == "" || rc.startedAt.Before(oldestAt) {
			oldestID, oldestAt = id, rc.startedAt
		}
	}
	return oldestID
}

// unregisterRun removes a run from the cancel registry once it has finished.
func (p *Pool) unregisterRun(runID string) {
	p.mu.Lock()
	delete(p.running, runID)
	p.mu.Unlock()
}

// Saturated reports whether every worker slot is currently busy running a
// job. Used to gate the "queued" UI signal (see TasksHandler.queuePositionMap)
// — a pickup-eligible task's position is only meaningful to surface as
// "waiting" when there is no free worker to run it immediately.
func (p *Pool) Saturated() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.running) >= p.maxWorkers
}

// MaxWorkers returns the pool's configured worker count (MAX_WORKERS). Used
// by the dispatcher/dashboard as the fallback effective concurrency limit for
// a repo with no repos.max_concurrent_runs override.
func (p *Pool) MaxWorkers() int {
	return p.maxWorkers
}

// Start launches worker goroutines. Blocks until ctx is cancelled.
func (p *Pool) Start(ctx context.Context) {
	for i := 0; i < p.maxWorkers; i++ {
		p.wg.Add(1)
		go p.worker(ctx)
	}
	p.wg.Wait()
}

// Submit enqueues a job. Returns false if the queue is full (job was not enqueued).
func (p *Pool) Submit(job Job) bool {
	if p.draining.Load() {
		return false
	}
	select {
	case p.jobs <- job:
		metrics.PoolQueueDepth.Set(float64(len(p.jobs)))
		return true
	default:
		metrics.PoolSubmitRejectedTotal.Inc()
		slog.Warn("pool: queue full, dropping job", "component", "pool", "run_id", job.RunID)
		return false
	}
}

// releaseLock clears the task's active-run lock, but only if it still points
// at runID. A finished/aborted run must never wipe a lock a concurrent
// (re-)dispatch has since taken -- that's how two agents end up sharing one
// worktree (see issue #244). If the lock has already moved on to another
// run, this logs and does nothing.
func (p *Pool) releaseLock(ctx context.Context, taskID, runID string) {
	p.lifecycle().releaseLock(ctx, taskID, runID)
}

// lifecycle returns the run-lifecycle helper that owns escalation and lock
// release (see escalate.go).
func (p *Pool) lifecycle() runLifecycle {
	return runLifecycle{q: p.q, pub: p.pub}
}

func (p *Pool) worker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case job := <-p.jobs:
			if p.draining.Load() {
				p.handleCancelled(job, Result{}, time.Now(), CancelReasonShutdown)
				continue
			}
			p.runGuarded(ctx, job)
		}
	}
}

// runGuarded wraps run so a panic in a provider can't silently kill the worker
// goroutine (which would permanently shrink the pool). It logs, marks the run
// failed, and clears the lock so the dispatcher can re-pick the task.
func (p *Pool) runGuarded(ctx context.Context, job Job) {
	log := slog.With("component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID)
	defer func() {
		if r := recover(); r != nil {
			log.Error("pool: agent run panicked", "panic", r, "stack", string(debug.Stack()))
			_, _ = p.q.SetAgentRunCompleted(context.Background(), gen.SetAgentRunCompletedParams{Status: "failed", ID: job.RunID})
			p.releaseLock(context.Background(), job.Input.Task.ID, job.RunID)
			metrics.RunTerminalTotal.WithLabelValues("failed").Inc()
			metrics.RunClassificationTotal.WithLabelValues(string(ClassGenuine)).Inc()
		}
	}()
	p.run(ctx, job)
}

func (p *Pool) run(ctx context.Context, job Job) {
	log := slog.With("component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID)

	log.Info("pool: agent run starting", "provider", job.Input.AgentConfig.Provider, "agent", job.Input.AgentConfig.Name)
	startedAt := time.Now()

	// Derive a cancellable context so a human can stop this run (see Cancel).
	// It also inherits the worker ctx, so pool shutdown still tears the run down.
	runCtx, cancel := context.WithCancel(ctx)
	rc := p.registerRun(job.RunID, cancel)
	if p.hardStop.Load() {
		// Registered after Shutdown's cancel sweep; make sure it is stopped too.
		p.cancelWithReason(job.RunID, CancelReasonShutdown)
	}
	metrics.PoolBusyWorkers.Inc()
	defer func() {
		metrics.PoolBusyWorkers.Dec()
		cancel()
		p.unregisterRun(job.RunID)
	}()
	ctx = runCtx

	if !p.startRun(ctx, job, log, startedAt) {
		return
	}

	var ok bool
	if job, ok = p.setupWorktree(ctx, job, rc, log, startedAt); !ok {
		return
	}

	// Prepare the repo's pinned toolchains (mise install / uv venv), if any,
	// here — in this run's own goroutine, under runCtx (the run's lifecycle
	// context: cancellable via Cancel, torn down on pool shutdown, but never
	// an HTTP request context) rather than synchronously in the dispatcher's
	// startRun. A cold install can take minutes; running it in the sweep loop
	// or under DispatchReply's request context would freeze all dispatch or
	// get killed by request-context cancellation. See runtime.Prep's own
	// 10-minute timeout for the failure bound.
	if !p.prepareRuntime(ctx, job, log, startedAt) {
		return
	}

	result, err := p.executeProvider(ctx, job)

	// A human-requested stop short-circuits all outcome handling: the run is
	// marked "cancelled" (not failed), consumes no retry budget, and is not
	// re-dispatched. Checked before error classification because a cancelled
	// provider typically returns a context/transient-looking error.
	if reason, ok := cancelledBy(rc, ctx); ok {
		if reason == CancelReasonShutdown {
			// Keep the provider session so the re-dispatch can resume it.
			p.persistRunSession(job, result, log)
		}
		p.handleCancelled(job, result, startedAt, reason)
		return
	}

	p.persistRunSession(job, result, log)

	// Surface the early-warning threshold regardless of how the run
	// ultimately ends (including a hard cost-budget kill below, so the board
	// still gets the warning even on a run that also gets the terminal
	// needs_human escalation) — the watchdog observed projected cost cross
	// CostWarnRatio*budget at some point during the run.
	if result.CostWarned && p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskCostWarning, wsevent.TaskCostWarning{
			TaskID:    job.Input.Task.ID,
			RunID:     job.RunID,
			SpentUSD:  job.Input.CostSpentUSD + result.CostUSD,
			BudgetUSD: job.Input.CostBudgetUSD,
		})
	}

	// Classify a provider error. A transient/rate-limit error is handled here and
	// short-circuits the rest of the run; a genuine error falls through as a plain
	// failed result.
	if err != nil {
		if p.handleProviderError(ctx, job, err, result, startedAt, log) {
			return
		}
		// A genuine error that never produced any evidence of actually starting
		// (no logs, no session, zero usage) is a "pre-stream" failure — e.g. the
		// provider errored building settings/args or spawning its subprocess,
		// before any output streamed. hasLoginError's auth-escalation net only
		// inspects the run's LOGS, so a pre-stream failure has nothing for it to
		// find and bypasses it; left as a plain "failed" result, it resets the
		// retry budget and clears the dispatch lock, so the 5s sweep re-picks
		// the task immediately — forever, since the same pre-stream error just
		// recurs (see the 100-run/3h loop this fixes). Routing it through
		// handleTransientFailure instead applies max_retries/retry_backoff_secs
		// and escalates to waiting_human once the budget is exhausted.
		if p.isPreStreamFailure(ctx, job, result) {
			p.handleTransientFailure(ctx, job, result, "provider failed before producing any output", startedAt)
			return
		}
		result = Result{Status: "failed"}
	}

	finalStatus := p.persistRunResult(ctx, job, &result, startedAt, log)

	p.safetyNetCommit(ctx, job, finalStatus, log)

	// A configured repo verify command is authoritative over the agent's
	// self-reported success: it runs after the safety-net commit (so it sees
	// the final tree) and before the success transition is resolved.
	if finalStatus == "completed" && result.Outcome == "success" {
		if p.verifyOutcome(ctx, job, &result, log) {
			reason, _ := cancelledBy(rc, ctx)
			p.handleCancelled(job, result, startedAt, reason)
			return
		}
	}

	// Resolve outcome → next label via workflow transitions.
	var resolvedLabel string
	if finalStatus == "completed" && result.Outcome != "" {
		resolvedLabel = p.resolveOutcome(ctx, job.Input.Task, result.Outcome)
	}

	if p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskAgentDone, wsevent.TaskAgentDone{
			TaskID: job.Input.Task.ID,
			RunID:  job.RunID,
			Status: finalStatus,
		})
	}

	p.applyTerminalTransition(ctx, job, result, finalStatus, resolvedLabel, log)

	// Clear rate-limit backoff on any non-rate-limited completion (success or normal failure).
	// Conditional on no newer 429 having landed since this run started, so a
	// slow sibling run sharing the same agent config can't wipe a fresh block
	// registered by a concurrently-finishing run (see issue #344).
	if p.RateLimits != nil {
		p.RateLimits.UnblockIfNotBlockedSince(job.Input.AgentConfig.usagePool(), startedAt)
	}

	// If this task is a parent with subtasks, settle merge-backs now that its run
	// is done: resolve conflict-resolution merges the run committed, and flush any
	// merges that were deferred while this run was in flight. No-op otherwise.
	isSubtask := job.Input.Task.ParentID != ""
	if p.Subtasks != nil && (finalStatus == "completed" || finalStatus == "failed") && !isSubtask && job.Input.Task.RepoPath != "" {
		p.Subtasks.AfterParentRun(ctx, job.Input.Task.ID, job.Input.Task.RepoPath, finalStatus == "completed")
	}

	log.Info("pool: agent run finished", "status", finalStatus)
}

// startRun marks the run started and publishes task.agent_started. It returns
// false (after recording a failed terminal run and clearing the task lock) if
// the started-marker write fails, signalling run() to abort.
func (p *Pool) startRun(ctx context.Context, job Job, log *slog.Logger, startedAt time.Time) bool {
	if _, err := p.q.SetAgentRunStarted(ctx, job.RunID); err != nil {
		log.Error("pool: set run started", "err", err)
		_, _ = p.q.SetAgentRunCompleted(context.Background(), gen.SetAgentRunCompletedParams{
			Status: "failed",
			ID:     job.RunID,
		})
		p.releaseLock(context.Background(), job.Input.Task.ID, job.RunID)
		metrics.RunTerminalTotal.WithLabelValues("failed").Inc()
		metrics.RunClassificationTotal.WithLabelValues(string(ClassGenuine)).Inc()
		metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())
		return false
	}
	if p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskAgentStarted, wsevent.TaskAgentStarted{
			TaskID:    job.Input.Task.ID,
			RunID:     job.RunID,
			AgentName: job.Input.AgentConfig.Name,
		})
	}
	return true
}

// setupWorktree provisions the task's worktree (job.SetupWorktree) in this
// run's own goroutine, so the network-bound `git fetch --prune` never blocks
// the dispatcher's sweep. It returns the job with the worktree-dependent input
// fields filled in, or false after ending the run: a human cancel is recorded
// as cancelled, and any other failure goes through handleTransientFailure
// (bounded backed-off retry, then waiting_human), which releases the dispatch
// lock only if this run still owns it. No-op when the worktree already existed.
func (p *Pool) setupWorktree(ctx context.Context, job Job, rc *runControl, log *slog.Logger, startedAt time.Time) (Job, bool) {
	if job.SetupWorktree == nil {
		return job, true
	}
	res, err := job.SetupWorktree(ctx)
	if err != nil {
		if reason, ok := cancelledBy(rc, ctx); ok {
			p.handleCancelled(job, Result{}, startedAt, reason)
			return job, false
		}
		log.Error("pool: worktree setup failed", "err", err)
		p.handleTransientFailure(ctx, job, Result{}, fmt.Sprintf("worktree setup failed: %v", err), startedAt)
		return job, false
	}
	job.Input.RepoPath = res.Path
	job.Input.Task.Branch = res.Branch
	if spec := job.Input.Runtime; spec != nil {
		cp := *spec
		cp.WorktreeDir = res.Path
		job.Input.Runtime = &cp
	}
	return job, true
}

// prepareRuntime installs the repo's pinned toolchains (job.Input.Runtime,
// resolved but NOT yet installed by the dispatcher — see
// Dispatcher.resolveRuntimeSpec) before the provider is invoked. No-op,
// returning true immediately, when the run has no runtime spec (the §4.1
// byte-identical case) — this never shells out to mise/uv for a repo with no
// runtime_languages configured.
//
// On prep failure, this mirrors handleMaxTurnsExhausted's escalation shape:
// the run row already exists and was already marked started (by
// p.startRun above) and already holds the task's active-run lock (set by the
// dispatcher's persistRunRow before the job was ever submitted), so — unlike
// the dispatcher's phantom-run escalation for a resolveRuntimeSpec parse
// failure — there is no lock/current-run bookkeeping to redo here: marking
// this real run waiting_human and leaving the lock exactly where it is
// already satisfies the fail-closed contract (PRD §4.6). Returns false if
// prep failed (run() must return without invoking the provider).
func (p *Pool) prepareRuntime(ctx context.Context, job Job, log *slog.Logger, startedAt time.Time) bool {
	spec := job.Input.Runtime
	if spec == nil || len(spec.Pins) == 0 {
		return true
	}

	if err := runtimePrep(ctx, spec.Pins, spec.WorktreeDir); err != nil {
		msg := fmt.Sprintf("agent runtime prep failed: %v", err)
		log.Error("pool: runtime prep failed", "err", err)
		_ = p.lifecycle().escalate(context.Background(), escalation{
			TaskID: job.Input.Task.ID, RunID: job.RunID, Reason: ReasonRuntimePrep, Message: msg, PublishDone: true,
		})
		metrics.RunTerminalTotal.WithLabelValues("waiting_human").Inc()
		metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())
		return false
	}
	return true
}

// executeProvider runs the provider while draining its log channel through a
// background persistence goroutine, returning the provider's result once both
// the provider and the log drain have finished.
func (p *Pool) executeProvider(ctx context.Context, job Job) (Result, error) {
	log := slog.With("component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID)

	logCh := make(chan LogEntry, 256)
	done := make(chan struct{})

	// Persist log entries in the background
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Error("pool: persist logs panicked", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		p.persistLogs(ctx, job.RunID, job.Input.Task.ID, logCh)
	}()

	result, err := job.Provider.Run(ctx, job.Input, logCh)
	close(logCh)
	<-done
	return result, err
}

// persistRunSession stores the provider session id (claude/qwen stream-json
// session_id) as soon as the provider returns — on any outcome, including
// failures, so a later run on this task can resume the conversation. Done
// before error classification because the transient/rate-limit paths return
// early.
func (p *Pool) persistRunSession(job Job, result Result, log *slog.Logger) {
	if result.SessionID != "" {
		if serr := p.q.SetAgentRunSession(context.Background(), gen.SetAgentRunSessionParams{
			SessionID: result.SessionID,
			ID:        job.RunID,
		}); serr != nil {
			log.Warn("pool: persist run session", "err", serr)
		}
	}
}

// handleProviderError classifies a non-nil provider error. A rate-limit,
// transient, or max-turns error is fully handled here (recording the failure
// and scheduling retry/escalation) and returns true so run() short-circuits.
// A genuine error is logged and returns false, letting run() fall through as
// a plain failed result. result carries any usage the provider accumulated
// before returning the error, so it can be preserved on the persisted run row.
func (p *Pool) handleProviderError(ctx context.Context, job Job, err error, result Result, startedAt time.Time, log *slog.Logger) bool {
	// Checked before the transientErr interface check below (belt-and-
	// suspenders): ErrMaxTurns deliberately does NOT implement Transient(),
	// but ordering this first guarantees a turn-limit run can never be
	// mistaken for a transient one even if that changes.
	var mt *ErrMaxTurns
	if errors.As(err, &mt) {
		log.Warn("pool: agent run hit max turns", "classification", string(ClassMaxTurns), "max_turns", mt.MaxTurns)
		p.handleMaxTurnsExhausted(ctx, job, result, mt, startedAt)
		return true
	}

	// Checked before the transientErr interface check below for the same
	// reason as ErrMaxTurns above: ErrCostBudgetExceeded deliberately does
	// NOT implement Transient(), but ordering this first guarantees a
	// budget-killed run can never be mistaken for a transient one.
	var ce *ErrCostBudgetExceeded
	if errors.As(err, &ce) {
		log.Warn("pool: agent run hit cost budget", "classification", string(ClassCostBudget), "spent_usd", ce.SpentUSD, "budget_usd", ce.BudgetUSD)
		p.handleCostBudgetExceeded(ctx, job, result, ce, startedAt)
		return true
	}

	var rl *ErrRateLimit
	if errors.As(err, &rl) {
		log.Warn("pool: agent run rate limited", "classification", string(ClassRateLimit), "reset_at", rl.ResetAt, "msg", rl.Message)
		metrics.RunClassificationTotal.WithLabelValues(string(ClassRateLimit)).Inc()
		// Register the block in the rate-limit registry. This blocks the
		// whole agent config (not just this task) from further dispatch
		// for a while — separate from, and complementary to, the
		// per-task retry-budget bookkeeping below.
		if p.RateLimits != nil {
			if !rl.ResetAt.IsZero() && rl.ResetAt.After(time.Now()) {
				p.RateLimits.Block(job.Input.AgentConfig.usagePool(), rl.ResetAt)
			} else {
				p.RateLimits.BlockWithBackoff(job.Input.AgentConfig.usagePool())
			}
		}
		// Notify the frontend so the task card can show a rate-limit hint.
		if p.pub != nil {
			var unblockStr string
			if p.RateLimits != nil {
				if until := p.RateLimits.BlockedUntil(job.Input.AgentConfig.usagePool()); !until.IsZero() {
					unblockStr = until.Format(time.RFC3339)
				}
			}
			p.pub.Publish(wsevent.TypeTaskRateLimited, wsevent.TaskRateLimited{
				TaskID:        job.Input.Task.ID,
				RunID:         job.RunID,
				AgentConfigID: job.Input.AgentConfig.ID,
				UnblockedAt:   unblockStr,
			})
		}
		p.handleRateLimited(ctx, job, result, "rate limited: "+rl.Message, startedAt)
		return true
	}

	var te transientErr
	if errors.As(err, &te) {
		log.Warn("pool: agent run transient error", "classification", string(ClassTransient), "err", err)
		metrics.RunClassificationTotal.WithLabelValues(string(ClassTransient)).Inc()
		p.handleTransientFailure(ctx, job, result, err.Error(), startedAt)
		return true
	}

	log.Error("pool: agent run error", "err", err)
	return false
}

// persistRunResult resolves the final status (escalating a login/auth failure
// to waiting_human), resets the transient-retry budget on a terminal
// success/genuine-failure, records the completed run row and its usage metrics,
// applies any resolved review comments, and persists agent notes. It returns
// the resolved final status. result may be mutated (Message set on auth
// escalation).
func (p *Pool) persistRunResult(ctx context.Context, job Job, result *Result, startedAt time.Time, log *slog.Logger) string {
	finalStatus := result.Status
	// Classify a plain failure for observability. Defaults to "genuine" (real
	// task failure, immediate re-dispatch); an auth signal in the run's logs
	// escalates to waiting_human so a human can re-authenticate rather than
	// silently retrying forever. Logged below as the `classification` field so
	// misclassifications are diagnosable from logs alone (see errclass.go).
	classification := ClassGenuine
	var authNotes *string
	if finalStatus == "failed" && p.hasLoginError(ctx, job.RunID) {
		finalStatus = "waiting_human"
		classification = ClassAuth
		msg := "Agent failed: not logged in. Please re-authenticate and re-run."
		result.Message = &msg
		result.EscalationReason = ReasonAuthRequired
		authNotes = &msg
		// One broken login would otherwise escalate every task the dispatcher
		// starts next: pause dispatch for this provider kind until its health
		// check passes again (or a human resumes it).
		if p.ProviderPauses.Pause(job.Input.AgentConfig.Provider, "Not logged in: re-authenticate the "+job.Input.AgentConfig.Provider+" provider.") {
			log.Warn("pool: auth failure, pausing dispatch for provider", "provider", job.Input.AgentConfig.Provider)
		}
	}
	if result.Status == "failed" {
		log.Warn("pool: agent run failed", "classification", string(classification), "final_status", finalStatus)
		metrics.RunClassificationTotal.WithLabelValues(string(classification)).Inc()
	}

	// Genuine failures release the dispatch lock, so without a cap the next 5s
	// sweep would re-run a persistently failing task forever. Count them and,
	// once maxConsecutiveFailures is reached, escalate to waiting_human (lock
	// kept) instead. Auth failures are already escalated above and don't count.
	genuineFailure := finalStatus == "failed"
	runNotes := result.Notes // run-row notes only; task agent_notes keeps the agent's own text
	if authNotes != nil {
		// Store the reason on the run so the UI can show it (previously a
		// login failure left the run with no notes or feedback at all).
		runNotes = authNotes
	}
	if genuineFailure {
		n, err := p.q.IncrementTaskFailureCount(ctx, job.Input.Task.ID)
		switch {
		case err != nil:
			slog.Warn("pool: increment failure count", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		case n >= maxConsecutiveFailures:
			finalStatus = "waiting_human"
			msg := fmt.Sprintf("Agent failed %d times in a row without completing. Review the run logs, then re-dispatch or intervene manually.", n)
			result.Message = &msg
			runNotes = &msg
			result.EscalationReason = ReasonRepeatedFailure
			log.Warn("pool: consecutive failure limit reached, escalating to waiting_human", "count", n, "limit", maxConsecutiveFailures)
		}
	}

	// A genuine (non-transient) failure or a successful completion resets the
	// task's transient-retry budget so a later unrelated transient blip
	// starts counting fresh.
	//
	// The transient reset on genuine failure is kept deliberately: a run that
	// executed and failed on its own merits shows the infra is reachable, so an
	// earlier blip shouldn't count against a later one. It can't defeat
	// escalation because the genuine-failure counter above is separate and is
	// NOT reset by transient failures, so alternating transient/genuine
	// failures still hit maxConsecutiveFailures.
	if genuineFailure || finalStatus == "completed" {
		if _, err := p.q.ResetTaskTransientRetry(ctx, job.Input.Task.ID); err != nil {
			slog.Warn("pool: reset transient retry count", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
	}
	// The genuine-failure streak is deliberately NOT reset here for a completed
	// run: a completed run whose requested transition is then rejected counts as
	// a failed attempt (see handleRejectedTransition). The streak is cleared
	// instead by the successful label transition itself (engine.Transition
	// zeroes consecutive_failure_count), which is what "completed and advanced"
	// means.

	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	var escReason *string
	if finalStatus == "waiting_human" {
		if result.EscalationReason == "" {
			// A provider-reported waiting_human is the request_human tool.
			result.EscalationReason = ReasonAgentQuestion
		}
		r := string(result.EscalationReason)
		escReason = &r
	}
	if _, err := p.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status:           finalStatus,
		StoredInfo:       result.StoredInfo,
		Notes:            runNotes,
		InputTokens:      result.InputTokens,
		OutputTokens:     result.OutputTokens,
		CostUsd:          result.CostUSD,
		CostUnknown:      costUnknown,
		TurnsUsed:        result.TurnsUsed,
		EscalationReason: escReason,
		ID:               job.RunID,
	}); err != nil {
		log.Error("pool: set run completed", "err", err)
	}
	metrics.RunTerminalTotal.WithLabelValues(finalStatus).Inc()
	metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())
	if result.CostUSD > 0 {
		metrics.RunCostUSDTotal.WithLabelValues(job.Input.AgentConfig.Provider, job.Input.AgentConfig.Name).Add(result.CostUSD)
	}
	if result.InputTokens > 0 {
		metrics.RunInputTokensTotal.WithLabelValues(job.Input.AgentConfig.Provider, job.Input.AgentConfig.Name).Add(float64(result.InputTokens))
	}
	if result.OutputTokens > 0 {
		metrics.RunOutputTokensTotal.WithLabelValues(job.Input.AgentConfig.Provider, job.Input.AgentConfig.Name).Add(float64(result.OutputTokens))
	}

	// Mark review comments the agent resolved via the MCP resolve_comment tool.
	// Only applied on a completed run — a failed run's claimed fixes never made
	// it onto the branch (the safety-net commit only runs on completion).
	if finalStatus == "completed" && len(result.ResolvedComments) > 0 {
		applied := 0
		for _, rc := range result.ResolvedComments {
			note := rc.Note
			if _, err := p.q.ResolveTaskReviewComment(ctx, gen.ResolveTaskReviewCommentParams{
				ResolutionNote:  &note,
				ResolvedByRunID: &job.RunID,
				ID:              rc.ID,
				TaskID:          job.Input.Task.ID,
			}); err != nil {
				// sql.ErrNoRows here means an unknown/already-resolved comment ID.
				log.Warn("pool: resolve review comment", "comment_id", rc.ID, "err", err)
				continue
			}
			applied++
		}
		if applied > 0 && p.pub != nil {
			p.pub.Publish(wsevent.TypeTaskReviewCommentsChange, wsevent.TaskReviewCommentsChanged{
				TaskID:   job.Input.Task.ID,
				RunID:    job.RunID,
				Resolved: applied,
			})
		}
	}

	if result.Notes != nil && *result.Notes != "" {
		if _, err := p.q.UpdateTaskNotes(ctx, gen.UpdateTaskNotesParams{
			AgentNotes: *result.Notes,
			ID:         job.Input.Task.ID,
		}); err != nil {
			log.Error("pool: persist agent notes", "err", err)
		}
	}

	return finalStatus
}

// safetyNetCommit captures any changes the agent left uncommitted in its
// worktree so the task's branch always reflects the run, then pushes ordinary
// (non-subtask) tasks' branches. No-op unless the run completed with a repo
// path; no-op on the commit if the agent already committed.
func (p *Pool) safetyNetCommit(ctx context.Context, job Job, finalStatus string, log *slog.Logger) {
	isSubtask := job.Input.Task.ParentID != ""
	if finalStatus == "completed" && job.Input.RepoPath != "" {
		// Serialize ref-mutating git ops against other tasks/merges in this repo.
		lock := RepoGitLock(job.Input.Task.RepoPath)
		lock.Lock()
		title := job.Input.Task.Title
		if title == "" {
			title = "(untitled task)"
		}
		msg := fmt.Sprintf("%s (safety-net commit)\n\nTask: %s\nAgent-Run: %s", title, job.Input.Task.ID, job.RunID)
		if err := commitIfDirty(ctx, job.Input.RepoPath, msg, p.GitName, p.GitEmail); err != nil {
			log.Warn("pool: safety-net commit failed", "err", err)
		}
		// Children never push to origin — their branch merges back into the
		// parent's branch on terminal (see SubtaskCoordinator). Only push
		// ordinary tasks.
		if !isSubtask && job.Input.RepoRemoteURL != "" && job.Input.Task.Branch != "" {
			if err := PushBranch(ctx, job.Input.RepoPath, job.Input.Task.Branch); err != nil {
				log.Warn("pool: push branch failed", "branch", job.Input.Task.Branch, "err", err)
			}
		}
		lock.Unlock()
	}
}

// applyTerminalTransition performs the terminal label/lock bookkeeping for the
// finished run based on its final status.
//
// The active-run lock must be cleared so the dispatcher can re-pick the task —
// but NOT before a transition that clears it itself. `engine.Transition` clears
// active_agent_run_id atomically as part of its compare-and-swap, so a blanket
// pre-clear here would open a window in which the dispatcher re-picks the task
// between the clear and the transition landing (a real double-dispatch race).
// Instead, each branch below clears the lock exactly when it owns that
// responsibility; a successful transition (and the waiting_human escalation,
// which intentionally stays locked) leaves it to the branch. Every clear here
// goes through releaseLock, which is scoped to this run's id: if a concurrent
// human transition (or a newer dispatch) has already moved the lock to
// another run, this run's cleanup must not clobber it (see issue #244).
func (p *Pool) applyTerminalTransition(ctx context.Context, job Job, result Result, finalStatus, resolvedLabel string, log *slog.Logger) {
	switch finalStatus {
	case "completed":
		if resolvedLabel != "" {
			// The outcome was resolved against the label the run was dispatched
			// on. If a human has since moved the task (possible once the run is
			// marked completed), applying it would act on the wrong column, and
			// the failure-loop bookkeeping below would escalate a task the human
			// already took over. Leave the task where the human put it; the move
			// already cleared the lock, and releaseLock is owner-scoped.
			if p.humanMovedTask(ctx, job, log) {
				p.releaseLock(ctx, job.Input.Task.ID, job.RunID)
				return
			}
			note := ""
			if result.Message != nil {
				note = *result.Message
			}
			// A "failure" outcome sends the task back along a rework path (e.g.
			// agent-review → work). Two things have to happen here that a plain
			// forward transition doesn't need:
			//   1. Route the findings to the next run as explicit *feedback* (a fix
			//      request), not just as the "prior plan". Otherwise the next Worker
			//      reads them under "NOTES FROM PRIOR AGENT" as an implementation
			//      plan, sees the code already written, and advances without ever
			//      addressing them — the core of the observed infinite loop.
			//   2. Break the loop if this same rework edge has already fired
			//      repeatedly without the task clearing review. The failure path is
			//      otherwise unbounded (every transition clears the dispatch lock),
			//      so a reviewer stuck on the same finding re-triggers a Worker
			//      forever until a human pauses it.
			if result.Outcome == "failure" {
				feedback := result.Message
				if result.verify != nil && result.verify.feedback != "" {
					feedback = &result.verify.feedback
				}
				if feedback != nil && *feedback != "" {
					if err := p.q.SetAgentRunFeedback(ctx, gen.SetAgentRunFeedbackParams{
						Feedback: feedback,
						ID:       job.RunID,
					}); err != nil {
						log.Warn("pool: route failure feedback", "err", err)
					}
				}
				if p.failureLoopExceeded(ctx, job.Input.Task.ID, job.Input.Task.Label, resolvedLabel, log) {
					// Escalation keeps the task locked on this run (waiting_human),
					// so the lock is deliberately left set — do not clear it.
					p.escalateFailureLoop(ctx, job, result, resolvedLabel, log)
					return
				}
			}
			// Pass the dispatch-time label so the engine's compare-and-swap
			// refuses (ErrStale) if a human moved the task since the check above.
			if err := p.engine.TransitionExpecting(ctx, job.Input.Task.ID, resolvedLabel, workflow.TriggerAgent, job.RunID, note, job.Input.Task.Label); err != nil {
				// The transition didn't land, so it did not clear the lock.
				log.Warn("pool: agent-requested transition rejected", "to", resolvedLabel, "err", err)
				p.handleRejectedTransition(ctx, job, result, resolvedLabel, err, log)
			}
		} else if result.verify != nil && result.verify.errMsg != "" {
			p.escalateVerifyError(ctx, job, result, log)
		} else if result.verify != nil {
			// Verification failed and the label has no agent failure path.
			p.holdOnVerifyFailure(ctx, job, result, log)
		} else {
			// No resolved label: the agent finished without reporting an outcome
			// (no signal_complete / OUTCOME: line) or reported one that maps to no
			// unambiguous transition. Re-running would just repeat the same thing
			// every sweep, so escalate: mark the run waiting_human and KEEP the
			// lock until a human acts.
			p.escalateNoOutcome(ctx, job, result, log)
		}

	case "failed":
		// Genuine failure below the consecutive-failure limit — the task stays on
		// its current label; unlock it so the next sweep re-picks it. Reaching
		// the limit turns finalStatus into waiting_human in persistRunResult, so
		// it never gets here. (Transient failures never reach here either; they
		// are handled by handleTransientFailure, which owns their lock/backoff.)
		p.releaseLock(ctx, job.Input.Task.ID, job.RunID)

	case "waiting_human":
		msg := ""
		if result.Message != nil {
			msg = *result.Message
		}
		p.lifecycle().announceNeedsHuman(job.Input.Task.ID, job.RunID, result.EscalationReason, msg)
	}
}

// humanMovedTask reports whether the task is no longer on the label this run
// was dispatched on, logging when so. A lookup error is treated as "not moved"
// so the transition (which re-checks atomically) decides.
func (p *Pool) humanMovedTask(ctx context.Context, job Job, log *slog.Logger) bool {
	if job.Input.Task.Label == "" {
		return false
	}
	t, err := p.q.GetTask(ctx, job.Input.Task.ID)
	if err != nil {
		return false
	}
	if t.Label != job.Input.Task.Label {
		log.Warn("pool: task moved since dispatch; skipping agent outcome",
			"dispatched_label", job.Input.Task.Label, "current_label", t.Label)
		return true
	}
	return false
}

// persistLogs drains logCh and writes to SQLite in batches wrapped in a transaction.
func (p *Pool) persistLogs(ctx context.Context, runID, taskID string, logCh <-chan LogEntry) {
	log := slog.With("component", "pool", "run_id", runID, "task_id", taskID)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var batch []gen.CreateAgentLogParams

	flush := func(flushCtx context.Context) {
		if len(batch) == 0 {
			return
		}
		tx, err := p.db.BeginTx(flushCtx, nil)
		if err != nil {
			log.Error("pool: persist log begin tx", "err", err)
			batch = batch[:0]
			return
		}
		tq := gen.New(tx)
		for _, entry := range batch {
			if err := tq.CreateAgentLog(flushCtx, entry); err != nil {
				log.Error("pool: persist log entry", "err", err)
			}
		}
		if err := tx.Commit(); err != nil {
			log.Error("pool: persist log commit", "err", err)
			_ = tx.Rollback()
		}
		batch = batch[:0]
	}

	for {
		select {
		case entry, ok := <-logCh:
			if !ok {
				// Use Background so a cancelled worker ctx doesn't drop the final batch.
				flush(context.Background())
				return
			}
			logID := uuid.NewString()
			batch = append(batch, gen.CreateAgentLogParams{
				ID:         logID,
				AgentRunID: runID,
				Timestamp:  entry.At,
				Type:       string(entry.Type),
				Content:    entry.Content,
			})
			if len(batch) >= 50 {
				flush(ctx)
			}
			// Also publish to WebSocket for live streaming. The published entry
			// carries the same id as the persisted agent_logs row (logID) so
			// clients can dedupe live entries against agent.log_replay and
			// GET /tasks/{id}/runs/{run_id}/logs pages by id alone.
			if p.pub != nil {
				p.pub.Publish(wsevent.TypeAgentLog, wsevent.AgentLog{
					RunID:  runID,
					TaskID: taskID,
					Entry: wsevent.AgentLogEntry{
						ID:      logID,
						Type:    string(entry.Type),
						Content: entry.Content,
						At:      entry.At,
					},
				})
			}

		case <-ticker.C:
			flush(ctx)
		}
	}
}
