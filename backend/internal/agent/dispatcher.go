package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// Dispatcher sweeps the database on an interval, picks up tasks that are
// in agent-triggerable labels, and submits them to the Pool.
type Dispatcher struct {
	pool     *Pool
	q        *gen.Queries
	engine   *workflow.Engine
	interval time.Duration
	// ProviderFactory builds a Provider for a given AgentConfig.
	ProviderFactory func(cfg AgentConfig) Provider
}

// NewDispatcher creates a Dispatcher with a 5-second sweep interval.
func NewDispatcher(db *sql.DB, pool *Pool, engine *workflow.Engine, factory func(AgentConfig) Provider) *Dispatcher {
	return &Dispatcher{
		pool:            pool,
		q:               gen.New(db),
		engine:          engine,
		interval:        5 * time.Second,
		ProviderFactory: factory,
	}
}

// Run sweeps on interval until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.sweep(ctx)
		}
	}
}

func (d *Dispatcher) sweep(ctx context.Context) {
	tasks, err := d.q.ListAgentPickupTasks(ctx)
	if err != nil {
		slog.Error("dispatcher sweep", "err", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	// Fetch configs once per sweep, not once per task.
	configs, err := d.q.ListAgentConfigs(ctx)
	if err != nil {
		slog.Error("list agent configs", "err", err)
		return
	}

	for _, t := range tasks {
		d.dispatch(ctx, t, configs)
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, t gen.Task, configs []gen.AgentConfig) {
	var matched *gen.AgentConfig
	for i, cfg := range configs {
		var labels []string
		if err := json.Unmarshal([]byte(cfg.Labels), &labels); err != nil {
			continue
		}
		for _, l := range labels {
			if l == t.Label {
				matched = &configs[i]
				break
			}
		}
		if matched != nil {
			break
		}
	}

	if matched == nil {
		// No agent configured for this label — skip silently
		return
	}

	repo, err := d.q.GetRepo(ctx, t.RepoID)
	if err != nil {
		slog.Error("get repo", "task_id", t.ID, "err", err)
		return
	}

	runID := uuid.NewString()
	var feedback *string
	if t.CurrentAgentRunID != nil {
		prior, _ := d.q.GetAgentRun(ctx, *t.CurrentAgentRunID)
		feedback = prior.Feedback
	}

	agentCfg := toAgentConfig(*matched)

	_, err = d.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:             runID,
		TaskID:         t.ID,
		AgentConfigID:  matched.ID,
		Feedback:       feedback,
	})
	if err != nil {
		slog.Error("create agent run", "task_id", t.ID, "err", err)
		return
	}

	// Mark the task's active run so the next sweep skips it.
	if err := d.q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: runID,
		ActiveAgentRunID:  runID,
		ID:                t.ID,
	}); err != nil {
		slog.Error("set task active run", "task_id", t.ID, "err", err)
		return
	}

	provider := d.ProviderFactory(agentCfg)

	enqueued := d.pool.Submit(Job{
		RunID:    runID,
		Provider: provider,
		Input: RunInput{
			RunID:       runID,
			Task:        Task{ID: t.ID, Title: t.Title, Description: t.Description, Type: t.Type, Label: t.Label},
			AgentConfig: agentCfg,
			RepoPath:    repo.Path,
			Feedback:    feedback,
		},
	})
	if !enqueued {
		// Pool was full; mark the run failed and clear the active slot.
		_, _ = d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
			Status: "failed",
			ID:     runID,
		})
		_ = d.q.ClearActiveAgentRun(ctx, t.ID)
		return
	}

	slog.Info("dispatched agent", "task_id", t.ID, "label", t.Label, "run_id", runID, "agent", matched.Name)
}

func toAgentConfig(cfg gen.AgentConfig) AgentConfig {
	var env map[string]string
	_ = json.Unmarshal([]byte(cfg.Env), &env)
	if env == nil {
		env = map[string]string{}
	}
	return AgentConfig{
		ID:           cfg.ID,
		Name:         cfg.Name,
		Provider:     cfg.Provider,
		Model:        cfg.Model,
		SystemPrompt: cfg.SystemPrompt,
		MaxTokens:    cfg.MaxTokens,
		TimeoutSecs:  cfg.TimeoutSecs,
		Env:          env,
	}
}

// used to silence unused import for sql package
var _ = sql.ErrNoRows
