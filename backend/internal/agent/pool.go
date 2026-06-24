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

// Submit enqueues a job. Non-blocking; drops if the queue is full.
func (p *Pool) Submit(job Job) {
	select {
	case p.jobs <- job:
	default:
		slog.Warn("agent pool queue full, dropping job", "run_id", job.RunID)
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
		p.persistLogs(ctx, job.RunID, logCh)
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

	if p.pub != nil {
		p.pub.Publish("task.agent_done", map[string]any{
			"task_id": job.Input.Task.ID,
			"run_id":  job.RunID,
			"status":  finalStatus,
		})
	}

	switch result.Status {
	case "completed":
		if result.NextLabel != nil && *result.NextLabel != "" {
			note := ""
			if result.Message != nil {
				note = *result.Message
			}
			if err := p.engine.Transition(ctx, job.Input.Task.ID, *result.NextLabel, workflow.TriggerAgent, job.RunID, note); err != nil {
				slog.Warn("agent-requested transition rejected", "run_id", job.RunID, "to", *result.NextLabel, "err", err)
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

// persistLogs drains logCh and writes to SQLite in batches.
func (p *Pool) persistLogs(ctx context.Context, runID string, logCh <-chan LogEntry) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var batch []gen.CreateAgentLogParams

	flush := func() {
		for _, entry := range batch {
			if err := p.q.CreateAgentLog(ctx, entry); err != nil {
				slog.Error("persist log", "err", err)
			}
		}
		batch = batch[:0]
	}

	for {
		select {
		case entry, ok := <-logCh:
			if !ok {
				flush()
				return
			}
			batch = append(batch, gen.CreateAgentLogParams{
				ID:          uuid.NewString(),
				AgentRunID:  runID,
				Timestamp:   entry.At,
				Type:        string(entry.Type),
				Content:     entry.Content,
			})
			if len(batch) >= 50 {
				flush()
			}
			// Also publish to WebSocket for live streaming
			if p.pub != nil {
				p.pub.Publish("agent.log", map[string]any{
					"run_id": runID,
					"entry": map[string]any{
						"type":    entry.Type,
						"content": entry.Content,
						"at":      entry.At,
					},
				})
			}

		case <-ticker.C:
			flush()
		}
	}
}

// newSystemLog is a convenience to emit a system log entry.
func newSystemLog(msg string) LogEntry {
	return LogEntry{Type: LogSystem, Content: msg, At: time.Now()}
}

var _ = fmt.Sprintf // suppress unused import
var _ = newSystemLog
