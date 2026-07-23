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

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// Publisher broadcasts events to connected WebSocket clients.
type Publisher interface {
	Publish(eventType string, payload map[string]any)
}

// Job is a single unit of work submitted to the pool.
type Job struct {
	RunID    string
	Provider Provider
	Input    RunInput
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
	// GitName/GitEmail are used for safety-net commits when the container has no git identity.
	GitName  string
	GitEmail string
	// Subtasks coordinates child→parent merge-back (optional — nil disables it).
	// Used here to skip pushing child branches and to flush/resolve merges once a
	// parent's run completes.
	Subtasks *SubtaskCoordinator

	// running tracks the cancel func for each in-flight run so a human can stop
	// a runaway agent (see Cancel). Populated in run(), removed when it returns.
	mu      sync.Mutex
	running map[string]*runControl
}

// runControl carries the per-run cancellation handle plus a flag distinguishing
// a human-requested stop from an incidental context cancellation (e.g. pool
// shutdown), so the pool can mark the run "cancelled" rather than "failed".
type runControl struct {
	cancel    context.CancelFunc
	cancelled atomic.Bool
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
	}
}

// Cancel signals the in-flight run identified by runID to stop. It cancels the
// run's context — CLI providers propagate this to their subprocess (they run
// under exec.CommandContext) and HTTP providers abort their request — and marks
// the run so the pool records it as "cancelled" when the provider returns.
// Returns false if no such run is currently active (already finished, never
// started, or running on a different server instance).
func (p *Pool) Cancel(runID string) bool {
	p.mu.Lock()
	rc := p.running[runID]
	p.mu.Unlock()
	if rc == nil {
		return false
	}
	rc.cancelled.Store(true)
	rc.cancel()
	return true
}

// registerRun records the cancel handle for a run and returns its control block.
func (p *Pool) registerRun(runID string, cancel context.CancelFunc) *runControl {
	rc := &runControl{cancel: cancel}
	p.mu.Lock()
	p.running[runID] = rc
	p.mu.Unlock()
	return rc
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

func (p *Pool) worker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.jobs:
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
			_ = p.q.ClearActiveAgentRun(context.Background(), job.Input.Task.ID)
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

	result, err := p.executeProvider(ctx, job)

	// A human-requested stop short-circuits all outcome handling: the run is
	// marked "cancelled" (not failed), consumes no retry budget, and is not
	// re-dispatched. Checked before error classification because a cancelled
	// provider typically returns a context/transient-looking error.
	if rc.cancelled.Load() {
		p.handleCancelled(job, startedAt)
		return
	}

	p.persistRunSession(job, result, log)

	// Classify a provider error. A transient/rate-limit error is handled here and
	// short-circuits the rest of the run; a genuine error falls through as a plain
	// failed result.
	if err != nil {
		if p.handleProviderError(ctx, job, err, startedAt, log) {
			return
		}
		result = Result{Status: "failed"}
	}

	finalStatus := p.persistRunResult(ctx, job, &result, startedAt, log)

	p.safetyNetCommit(ctx, job, finalStatus, log)

	// Resolve outcome → next label via workflow transitions.
	var resolvedLabel string
	if finalStatus == "completed" && result.Outcome != "" {
		resolvedLabel = p.resolveOutcome(ctx, job.Input.Task, result.Outcome)
	}

	if p.pub != nil {
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  finalStatus,
		})
	}

	p.applyTerminalTransition(ctx, job, result, finalStatus, resolvedLabel, log)

	// Clear rate-limit backoff on any non-rate-limited completion (success or normal failure).
	if p.RateLimits != nil {
		p.RateLimits.Unblock(job.Input.AgentConfig.ID)
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
		_ = p.q.ClearActiveAgentRun(context.Background(), job.Input.Task.ID)
		metrics.RunTerminalTotal.WithLabelValues("failed").Inc()
		metrics.RunClassificationTotal.WithLabelValues(string(ClassGenuine)).Inc()
		metrics.RunDurationSeconds.WithLabelValues(job.Input.AgentConfig.Provider).Observe(time.Since(startedAt).Seconds())
		return false
	}
	if p.pub != nil {
		p.pub.Publish("task.agent_started", map[string]any{
			"task_id":    job.Input.Task.ID,
			"run_id":     job.RunID,
			"agent_name": job.Input.AgentConfig.Name,
		})
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

// handleProviderError classifies a non-nil provider error. A rate-limit or
// transient error is fully handled here (recording the failure and scheduling
// retry/escalation) and returns true so run() short-circuits. A genuine error
// is logged and returns false, letting run() fall through as a plain failed
// result.
func (p *Pool) handleProviderError(ctx context.Context, job Job, err error, startedAt time.Time, log *slog.Logger) bool {
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
				p.RateLimits.Block(job.Input.AgentConfig.ID, rl.ResetAt)
			} else {
				p.RateLimits.BlockWithBackoff(job.Input.AgentConfig.ID)
			}
		}
		// Notify the frontend so the task card can show a rate-limit hint.
		if p.pub != nil {
			var unblockStr string
			if p.RateLimits != nil {
				if until := p.RateLimits.BlockedUntil(job.Input.AgentConfig.ID); !until.IsZero() {
					unblockStr = until.Format(time.RFC3339)
				}
			}
			p.pub.Publish("task.rate_limited", map[string]any{
				"task_id":         job.Input.Task.ID,
				"run_id":          job.RunID,
				"agent_config_id": job.Input.AgentConfig.ID,
				"unblocked_at":    unblockStr,
			})
		}
		p.handleTransientFailure(ctx, job, "rate limited: "+rl.Message, startedAt)
		return true
	}

	var te transientErr
	if errors.As(err, &te) {
		log.Warn("pool: agent run transient error", "classification", string(ClassTransient), "err", err)
		metrics.RunClassificationTotal.WithLabelValues(string(ClassTransient)).Inc()
		p.handleTransientFailure(ctx, job, err.Error(), startedAt)
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
	if finalStatus == "failed" && p.hasLoginError(ctx, job.RunID) {
		finalStatus = "waiting_human"
		classification = ClassAuth
		msg := "Agent failed: not logged in. Please re-authenticate and re-run."
		result.Message = &msg
	}
	if result.Status == "failed" {
		log.Warn("pool: agent run failed", "classification", string(classification), "final_status", finalStatus)
		metrics.RunClassificationTotal.WithLabelValues(string(classification)).Inc()
	}

	// A genuine (non-transient) failure or a successful completion resets the
	// task's transient-retry budget so a later unrelated transient blip
	// starts counting fresh.
	if finalStatus == "failed" || finalStatus == "completed" {
		if _, err := p.q.ResetTaskTransientRetry(ctx, job.Input.Task.ID); err != nil {
			slog.Warn("pool: reset transient retry count", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
	}

	costUnknown := int64(0)
	if result.CostUnknown {
		costUnknown = 1
	}
	if _, err := p.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status:       finalStatus,
		StoredInfo:   result.StoredInfo,
		Notes:        result.Notes,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		CostUnknown:  costUnknown,
		ID:           job.RunID,
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
			p.pub.Publish("task.review_comments_changed", map[string]any{
				"task_id":  job.Input.Task.ID,
				"run_id":   job.RunID,
				"resolved": applied,
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
// which intentionally stays locked) leaves it to the branch.
func (p *Pool) applyTerminalTransition(ctx context.Context, job Job, result Result, finalStatus, resolvedLabel string, log *slog.Logger) {
	switch finalStatus {
	case "completed":
		if resolvedLabel != "" {
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
				if result.Message != nil && *result.Message != "" {
					if err := p.q.SetAgentRunFeedback(ctx, gen.SetAgentRunFeedbackParams{
						Feedback: result.Message,
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
			if err := p.engine.Transition(ctx, job.Input.Task.ID, resolvedLabel, workflow.TriggerAgent, job.RunID, note); err != nil {
				// The transition didn't land (e.g. rejected or a concurrent move),
				// so it did not clear the lock — clear it here so the finished run
				// doesn't leave the task stuck locked.
				log.Warn("pool: agent-requested transition rejected", "to", resolvedLabel, "err", err)
				_ = p.q.ClearActiveAgentRun(ctx, job.Input.Task.ID)
			}
		} else {
			// No resolved label — unlock and block re-dispatch until a human acts.
			_ = p.q.ClearActiveAgentRun(ctx, job.Input.Task.ID)
			if p.pub != nil {
				p.pub.Publish("task.needs_human", map[string]any{
					"task_id": job.Input.Task.ID,
					"run_id":  job.RunID,
					"message": "Agent completed but outcome could not be resolved to a transition. Please review and move the task manually.",
				})
			}
		}

	case "failed":
		// Genuine failure — the task stays on its current label; unlock it so the
		// next sweep re-picks it (transient failures never reach here; they are
		// handled by handleTransientFailure, which owns their lock/backoff).
		_ = p.q.ClearActiveAgentRun(ctx, job.Input.Task.ID)

	case "waiting_human":
		msg := ""
		if result.Message != nil {
			msg = *result.Message
		}
		if p.pub != nil {
			p.pub.Publish("task.needs_human", map[string]any{
				"task_id": job.Input.Task.ID,
				"run_id":  job.RunID,
				"message": msg,
			})
		}
	}
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
			batch = append(batch, gen.CreateAgentLogParams{
				ID:         uuid.NewString(),
				AgentRunID: runID,
				Timestamp:  entry.At,
				Type:       string(entry.Type),
				Content:    entry.Content,
			})
			if len(batch) >= 50 {
				flush(ctx)
			}
			// Also publish to WebSocket for live streaming
			if p.pub != nil {
				p.pub.Publish("agent.log", map[string]any{
					"run_id":  runID,
					"task_id": taskID,
					"entry": map[string]any{
						"type":    entry.Type,
						"content": entry.Content,
						"at":      entry.At,
					},
				})
			}

		case <-ticker.C:
			flush(ctx)
		}
	}
}
