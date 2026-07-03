package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
}

// NewPool creates a new pool. Call Start to begin accepting jobs.
func NewPool(maxWorkers int, db *sql.DB, engine *workflow.Engine, pub Publisher) *Pool {
	return &Pool{
		maxWorkers: maxWorkers,
		jobs:       make(chan Job, maxWorkers*4),
		db:         db,
		q:          gen.New(db),
		engine:     engine,
		pub:        pub,
	}
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
		return true
	default:
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
	defer func() {
		if r := recover(); r != nil {
			slog.Error("pool: agent run panicked", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "panic", r, "stack", string(debug.Stack()))
			_, _ = p.q.SetAgentRunCompleted(context.Background(), gen.SetAgentRunCompletedParams{Status: "failed", ID: job.RunID})
			_ = p.q.ClearActiveAgentRun(context.Background(), job.Input.Task.ID)
		}
	}()
	p.run(ctx, job)
}

func (p *Pool) run(ctx context.Context, job Job) {
	slog.Info("pool: agent run starting", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "provider", job.Input.AgentConfig.Provider, "agent", job.Input.AgentConfig.Name)

	if _, err := p.q.SetAgentRunStarted(ctx, job.RunID); err != nil {
		slog.Error("pool: set run started", "component", "pool", "run_id", job.RunID, "err", err)
		_, _ = p.q.SetAgentRunCompleted(context.Background(), gen.SetAgentRunCompletedParams{
			Status: "failed",
			ID:     job.RunID,
		})
		_ = p.q.ClearActiveAgentRun(context.Background(), job.Input.Task.ID)
		return
	}
	if p.pub != nil {
		p.pub.Publish("task.agent_started", map[string]any{
			"task_id":    job.Input.Task.ID,
			"run_id":     job.RunID,
			"agent_name": job.Input.AgentConfig.Name,
		})
	}

	logCh := make(chan LogEntry, 256)
	done := make(chan struct{})

	// Persist log entries in the background
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				slog.Error("pool: persist logs panicked", "component", "pool", "run_id", job.RunID, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		p.persistLogs(ctx, job.RunID, job.Input.Task.ID, logCh)
	}()

	result, err := job.Provider.Run(ctx, job.Input, logCh)
	close(logCh)
	<-done

	if err != nil {
		var rl *ErrRateLimit
		if errors.As(err, &rl) {
			slog.Warn("pool: agent run rate limited", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "reset_at", rl.ResetAt, "msg", rl.Message)
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
			p.handleTransientFailure(ctx, job, "rate limited: "+rl.Message)
			return
		}

		var te transientErr
		if errors.As(err, &te) {
			slog.Warn("pool: agent run transient error", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
			p.handleTransientFailure(ctx, job, err.Error())
			return
		}

		slog.Error("pool: agent run error", "component", "pool", "run_id", job.RunID, "err", err)
		result = Result{Status: "failed"}
	}

	finalStatus := result.Status
	// If the run failed due to an auth error, escalate to waiting_human so a human
	// can intervene (e.g. re-login) rather than silently retrying forever.
	if finalStatus == "failed" && p.hasLoginError(ctx, job.RunID) {
		finalStatus = "waiting_human"
		msg := "Agent failed: not logged in. Please re-authenticate and re-run."
		result.Message = &msg
	}

	// A genuine (non-transient) failure or a successful completion resets the
	// task's transient-retry budget so a later unrelated transient blip
	// starts counting fresh.
	if finalStatus == "failed" || finalStatus == "completed" {
		if _, err := p.q.ResetTaskTransientRetry(ctx, job.Input.Task.ID); err != nil {
			slog.Warn("pool: reset transient retry count", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
	}

	if _, err := p.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status:       finalStatus,
		StoredInfo:   result.StoredInfo,
		Notes:        result.Notes,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		CostUsd:      result.CostUSD,
		ID:           job.RunID,
	}); err != nil {
		slog.Error("pool: set run completed", "component", "pool", "run_id", job.RunID, "err", err)
	}

	if result.Notes != nil && *result.Notes != "" {
		if _, err := p.q.UpdateTaskNotes(ctx, gen.UpdateTaskNotesParams{
			AgentNotes: *result.Notes,
			ID:         job.Input.Task.ID,
		}); err != nil {
			slog.Error("pool: persist agent notes", "component", "pool", "run_id", job.RunID, "err", err)
		}
	}

	// Safety-net: capture any changes the agent left uncommitted in its worktree
	// so the task's branch always reflects the run. No-op if the agent committed.
	if finalStatus == "completed" && job.Input.RepoPath != "" {
		msg := fmt.Sprintf("task %s: agent run %s", job.Input.Task.ID, job.RunID)
		if err := commitIfDirty(ctx, job.Input.RepoPath, msg, p.GitName, p.GitEmail); err != nil {
			slog.Warn("pool: safety-net commit failed", "component", "pool", "run_id", job.RunID, "err", err)
		}
		if job.Input.RepoRemoteURL != "" && job.Input.Task.Branch != "" {
			if err := PushBranch(ctx, job.Input.RepoPath, job.Input.Task.Branch); err != nil {
				slog.Warn("pool: push branch failed", "component", "pool", "run_id", job.RunID, "branch", job.Input.Task.Branch, "err", err)
			}
		}
	}

	// Resolve outcome → next label via workflow transitions.
	var resolvedLabel string
	if finalStatus == "completed" && result.Outcome != "" {
		resolvedLabel = p.resolveOutcome(ctx, job.Input.Task, result.Outcome)
	}

	// Clear the active-run slot so the dispatcher can re-pick the task.
	// waiting_human intentionally stays locked until a human acts.
	if finalStatus == "failed" || finalStatus == "completed" {
		_ = p.q.ClearActiveAgentRun(ctx, job.Input.Task.ID)
	}

	if p.pub != nil {
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  finalStatus,
		})
	}

	switch finalStatus {
	case "completed":
		if resolvedLabel != "" {
			note := ""
			if result.Message != nil {
				note = *result.Message
			}
			if err := p.engine.Transition(ctx, job.Input.Task.ID, resolvedLabel, workflow.TriggerAgent, job.RunID, note); err != nil {
				slog.Warn("pool: agent-requested transition rejected", "component", "pool", "run_id", job.RunID, "to", resolvedLabel, "err", err)
			}
		} else {
			// No resolved label — block re-dispatch until a human acts.
			if p.pub != nil {
				p.pub.Publish("task.needs_human", map[string]any{
					"task_id": job.Input.Task.ID,
					"run_id":  job.RunID,
					"message": "Agent completed but outcome could not be resolved to a transition. Please review and move the task manually.",
				})
			}
		}

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

	// Clear rate-limit backoff on any non-rate-limited completion (success or normal failure).
	if p.RateLimits != nil {
		p.RateLimits.Unblock(job.Input.AgentConfig.ID)
	}

	slog.Info("pool: agent run finished", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "status", finalStatus)
}

// handleTransientFailure marks the run failed and either schedules a bounded,
// backed-off auto-retry of the task or — once the configured retry budget is
// exhausted — escalates to waiting_human so a human isn't left guessing
// whether the task is quietly retrying or stuck. Always clears the active
// run slot so the dispatcher can re-pick the task once eligible (either on
// the next sweep, if no retry budget was consumed, or once next_retry_at
// elapses).
func (p *Pool) handleTransientFailure(ctx context.Context, job Job, reason string) {
	bg := context.Background()

	maxRetries := job.Input.AgentConfig.MaxRetries
	task, terr := p.q.GetTask(bg, job.Input.Task.ID)
	var count int64
	if terr == nil {
		count = task.TransientRetryCount
	} else {
		slog.Warn("pool: handleTransientFailure: get task", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", terr)
	}

	finalStatus := "failed"
	var message *string

	if maxRetries > 0 && count < maxRetries {
		// Still within budget — bump the counter and schedule a backed-off retry.
		newCount := count + 1
		backoffBase := time.Duration(job.Input.AgentConfig.RetryBackoffSecs) * time.Second
		delay := BackoffDurationWithBase(int(newCount-1), backoffBase)
		nextRetryAt := time.Now().Add(delay)
		if _, err := p.q.SetTaskTransientRetry(bg, gen.SetTaskTransientRetryParams{
			TransientRetryCount: newCount,
			NextRetryAt:         &nextRetryAt,
			ID:                  job.Input.Task.ID,
		}); err != nil {
			slog.Warn("pool: set transient retry", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
		slog.Info("pool: transient failure, scheduling auto-retry", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "attempt", newCount, "max_retries", maxRetries, "next_retry_at", nextRetryAt, "reason", reason)
	} else if maxRetries > 0 {
		// Budget exhausted — escalate to waiting_human rather than retrying forever.
		// Reset the counter so a human-triggered re-dispatch starts a fresh budget.
		finalStatus = "waiting_human"
		msg := fmt.Sprintf("Agent failed after %d transient retries (%s). Manual re-dispatch or intervention required.", count, reason)
		message = &msg
		if _, err := p.q.ResetTaskTransientRetry(bg, job.Input.Task.ID); err != nil {
			slog.Warn("pool: reset transient retry (escalation)", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
		slog.Warn("pool: transient retry budget exhausted, escalating to waiting_human", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "count", count, "max_retries", maxRetries)
	} else {
		// max_retries == 0 — auto-retry disabled for this config; behaves like
		// today's plain failed-and-immediately-re-dispatchable behavior.
		if _, err := p.q.ResetTaskTransientRetry(bg, job.Input.Task.ID); err != nil {
			slog.Warn("pool: reset transient retry (disabled)", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "err", err)
		}
		slog.Info("pool: transient failure, auto-retry disabled (max_retries=0)", "component", "pool", "run_id", job.RunID, "task_id", job.Input.Task.ID, "reason", reason)
	}

	runParams := gen.SetAgentRunCompletedParams{Status: finalStatus, ID: job.RunID}
	if message != nil {
		runParams.Notes = message
	}
	if _, err := p.q.SetAgentRunCompleted(bg, runParams); err != nil {
		slog.Warn("pool: set run completed (transient)", "component", "pool", "run_id", job.RunID, "err", err)
	}

	if finalStatus == "waiting_human" {
		// waiting_human intentionally stays locked (active run not cleared)
		// until a human acts, consistent with the login-error escalation path.
		if p.pub != nil {
			msg := ""
			if message != nil {
				msg = *message
			}
			p.pub.Publish("task.needs_human", map[string]any{
				"task_id": job.Input.Task.ID,
				"run_id":  job.RunID,
				"message": msg,
			})
		}
	} else {
		_ = p.q.ClearActiveAgentRun(bg, job.Input.Task.ID)
	}

	if p.pub != nil {
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  finalStatus,
		})
	}
}

// hasLoginError scans the last 20 log entries for an auth/login error signal.
func (p *Pool) hasLoginError(ctx context.Context, runID string) bool {
	logs, err := p.q.ListAgentLogs(ctx, runID)
	if err != nil {
		return false
	}
	// Check the last 20 entries only — login errors appear near the end.
	start := len(logs) - 20
	if start < 0 {
		start = 0
	}
	for _, l := range logs[start:] {
		if strings.Contains(l.Content, "Not logged in") || strings.Contains(l.Content, "Please run /login") {
			return true
		}
	}
	return false
}

// resolveOutcome finds the to_label for a given outcome ("success"|"failure") from the task's current label.
// Returns empty string if no unambiguous match is found.
func (p *Pool) resolveOutcome(ctx context.Context, task Task, outcome string) string {
	all, err := p.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		slog.Error("pool: resolve outcome: list transitions", "component", "pool", "task_id", task.ID, "err", err)
		return ""
	}
	var match string
	for _, t := range all {
		if t.FromLabel != task.Label {
			continue
		}
		// Only consider transitions that an agent can trigger.
		if t.TriggerType == "human" {
			continue
		}
		// nil Path means the transition fires on any outcome.
		if t.Path != nil && *t.Path != outcome && *t.Path != "either" {
			continue
		}
		if match != "" {
			slog.Warn("pool: resolve outcome: ambiguous transitions", "component", "pool", "task_id", task.ID, "outcome", outcome)
			return ""
		}
		match = t.ToLabel
	}
	return match
}

// persistLogs drains logCh and writes to SQLite in batches wrapped in a transaction.
func (p *Pool) persistLogs(ctx context.Context, runID, taskID string, logCh <-chan LogEntry) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var batch []gen.CreateAgentLogParams

	flush := func(flushCtx context.Context) {
		if len(batch) == 0 {
			return
		}
		tx, err := p.db.BeginTx(flushCtx, nil)
		if err != nil {
			slog.Error("pool: persist log begin tx", "component", "pool", "err", err)
			batch = batch[:0]
			return
		}
		tq := gen.New(tx)
		for _, entry := range batch {
			if err := tq.CreateAgentLog(flushCtx, entry); err != nil {
				slog.Error("pool: persist log entry", "component", "pool", "err", err)
			}
		}
		if err := tx.Commit(); err != nil {
			slog.Error("pool: persist log commit", "component", "pool", "err", err)
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
