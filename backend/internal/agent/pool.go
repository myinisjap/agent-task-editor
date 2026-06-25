package agent

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
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
		slog.Warn("agent pool queue full, dropping job", "run_id", job.RunID)
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
			p.run(ctx, job)
		}
	}
}

func (p *Pool) run(ctx context.Context, job Job) {
	slog.Info("agent run starting", "run_id", job.RunID, "task_id", job.Input.Task.ID)

	if _, err := p.q.SetAgentRunStarted(ctx, job.RunID); err != nil {
		slog.Error("set run started", "err", err)
		// Mark as failed and clear the active slot so the task re-queues.
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
		p.persistLogs(ctx, job.RunID, job.Input.Task.ID, logCh)
	}()

	result, err := job.Provider.Run(ctx, job.Input, logCh)
	close(logCh)
	<-done

	if err != nil {
		slog.Error("agent run error", "run_id", job.RunID, "err", err)
		result = Result{Status: "failed"}
	}

	finalStatus := result.Status
	if _, err := p.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: finalStatus,
		ID:     job.RunID,
	}); err != nil {
		slog.Error("set run completed", "err", err)
	}

	if result.Notes != nil && *result.Notes != "" {
		if _, err := p.q.UpdateTaskNotes(ctx, gen.UpdateTaskNotesParams{
			AgentNotes: *result.Notes,
			ID:         job.Input.Task.ID,
		}); err != nil {
			slog.Error("persist agent notes", "err", err)
		}
	}

	// Resolve outcome → next label via workflow transitions.
	var resolvedLabel string
	if finalStatus == "completed" && result.Outcome != "" {
		resolvedLabel = p.resolveOutcome(ctx, job.Input.Task, result.Outcome)
	}

	// Clear the active-run slot so the dispatcher can pick up the task again.
	// For waiting_human (or completed with no resolved label) we leave it set —
	// the engine.Transition called by the human action will clear it via SQL.
	clearActive := finalStatus == "failed" || (finalStatus == "completed" && resolvedLabel != "")
	if clearActive {
		_ = p.q.ClearActiveAgentRun(ctx, job.Input.Task.ID)
	}

	if p.pub != nil {
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  finalStatus,
		})
	}

	switch result.Status {
	case "completed":
		if resolvedLabel != "" {
			note := ""
			if result.Message != nil {
				note = *result.Message
			}
			if err := p.engine.Transition(ctx, job.Input.Task.ID, resolvedLabel, workflow.TriggerAgent, job.RunID, note); err != nil {
				slog.Warn("agent-requested transition rejected", "run_id", job.RunID, "to", resolvedLabel, "err", err)
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

	slog.Info("agent run finished", "run_id", job.RunID, "status", finalStatus)
}

// resolveOutcome finds the to_label for a given outcome ("success"|"failure") from the task's current label.
// Returns empty string if no unambiguous match is found.
func (p *Pool) resolveOutcome(ctx context.Context, task Task, outcome string) string {
	all, err := p.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		slog.Error("resolve outcome: list transitions", "err", err)
		return ""
	}
	var match string
	for _, t := range all {
		if t.FromLabel != task.Label {
			continue
		}
		if t.Path == nil {
			continue
		}
		if *t.Path != outcome && *t.Path != "either" {
			continue
		}
		if match != "" {
			slog.Warn("resolve outcome: ambiguous transitions", "task", task.ID, "outcome", outcome)
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
			slog.Error("persist log begin tx", "err", err)
			batch = batch[:0]
			return
		}
		tq := gen.New(tx)
		for _, entry := range batch {
			if err := tq.CreateAgentLog(flushCtx, entry); err != nil {
				slog.Error("persist log", "err", err)
			}
		}
		if err := tx.Commit(); err != nil {
			slog.Error("persist log commit", "err", err)
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

// newSystemLog is a convenience to emit a system log entry.
func newSystemLog(msg string) LogEntry {
	return LogEntry{Type: LogSystem, Content: msg, At: time.Now()}
}

var _ = fmt.Sprintf // suppress unused import
var _ = newSystemLog
