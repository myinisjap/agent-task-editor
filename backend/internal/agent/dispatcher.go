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
		slog.Error("dispatcher sweep failed", "component", "dispatcher", "err", err)
		return
	}
	slog.Debug("dispatcher sweep", "component", "dispatcher", "pending_tasks", len(tasks))
	if len(tasks) == 0 {
		return
	}

	// Fetch active configs once per sweep, not once per task.
	configs, err := d.q.ListAgentConfigs(ctx)
	if err != nil {
		slog.Error("dispatcher: list active agent configs", "component", "dispatcher", "err", err)
		return
	}
	slog.Debug("dispatcher sweep: active configs", "component", "dispatcher", "config_count", len(configs))

	for _, t := range tasks {
		d.dispatch(ctx, t, configs)
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, t gen.Task, configs []gen.AgentConfig) {
	matched := matchConfig(configs, t.Label)
	if matched == nil {
		slog.Debug("dispatcher: no active config for label", "component", "dispatcher", "task_id", t.ID, "label", t.Label)
		return
	}

	repo, err := d.q.GetRepo(ctx, t.RepoID)
	if err != nil {
		slog.Error("dispatcher: get repo", "component", "dispatcher", "task_id", t.ID, "err", err)
		return
	}

	runID := uuid.NewString()
	var feedback *string
	if t.CurrentAgentRunID != nil {
		prior, _ := d.q.GetAgentRun(ctx, *t.CurrentAgentRunID)
		feedback = prior.Feedback
	}

	var agentNotes *string
	if t.AgentNotes != "" {
		agentNotes = &t.AgentNotes
	}

	agentCfg := toAgentConfig(*matched)

	_, err = d.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:             runID,
		TaskID:         t.ID,
		AgentConfigID:  &matched.ID,
		Feedback:       feedback,
	})
	if err != nil {
		slog.Error("dispatcher: create agent run", "component", "dispatcher", "task_id", t.ID, "err", err)
		return
	}

	// Mark the task's active run so the next sweep skips it.
	if err := d.q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID,
		ActiveAgentRunID:  &runID,
		ID:                t.ID,
	}); err != nil {
		slog.Error("dispatcher: set task active run", "component", "dispatcher", "task_id", t.ID, "err", err)
		return
	}

	transitions := d.buildTransitionHints(ctx, t.WorkflowID, t.Label)
	provider := d.ProviderFactory(agentCfg)

	enqueued := d.pool.Submit(Job{
		RunID:    runID,
		Provider: provider,
		Input: RunInput{
			RunID:       runID,
			Task:        Task{ID: t.ID, Title: t.Title, Description: t.Description, Type: t.Type, Label: t.Label, WorkflowID: t.WorkflowID, AgentNotes: t.AgentNotes},
			AgentConfig: agentCfg,
			RepoPath:    repo.Path,
			Transitions: transitions,
			Feedback:    feedback,
			PriorPlan:   agentNotes,
		},
	})
	if !enqueued {
		slog.Warn("dispatcher: pool full, dropping job", "component", "dispatcher", "task_id", t.ID, "run_id", runID)
		_, _ = d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
			Status: "failed",
			ID:     runID,
		})
		_ = d.q.ClearActiveAgentRun(ctx, t.ID)
		return
	}

	slog.Info("dispatcher: agent dispatched", "component", "dispatcher", "task_id", t.ID, "label", t.Label, "run_id", runID, "agent", matched.Name, "provider", matched.Provider, "agent_id", matched.ID, "agent_enabled", matched.Enabled)
}

func (d *Dispatcher) buildTransitionHints(ctx context.Context, workflowID, fromLabel string) []TransitionHint {
	all, err := d.q.ListWorkflowTransitions(ctx, workflowID)
	if err != nil {
		slog.Warn("dispatcher: build transition hints", "component", "dispatcher", "workflow_id", workflowID, "err", err)
		return nil
	}
	var hints []TransitionHint
	for _, t := range all {
		if t.FromLabel != fromLabel || t.Path == nil {
			continue
		}
		hints = append(hints, TransitionHint{ToLabel: t.ToLabel, Path: *t.Path})
	}
	return hints
}

// matchConfig returns the first enabled config whose labels include the task's
// label. configs is ordered newest-first (created_at DESC), so the most recently
// created config wins on a tie. A parse failure is logged and the config skipped;
// a second match is logged as an ambiguity warning but does not change the winner.
func matchConfig(configs []gen.AgentConfig, label string) *gen.AgentConfig {
	var matched *gen.AgentConfig
	for i := range configs {
		cfg := &configs[i]
		if cfg.Enabled != 1 {
			continue // ponytail: defense-in-depth; ListAgentConfigs already filters enabled=1
		}
		var labels []string
		if err := json.Unmarshal([]byte(cfg.Labels), &labels); err != nil {
			slog.Error("dispatcher: skipping config with unparseable labels", "component", "dispatcher", "config_id", cfg.ID, "config_name", cfg.Name, "err", err)
			continue
		}
		for _, l := range labels {
			if l != label {
				continue
			}
			if matched == nil {
				matched = cfg
			} else {
				slog.Warn("dispatcher: multiple configs match label, using newest", "component", "dispatcher", "label", label, "using", matched.Name, "also_matched", cfg.Name)
			}
			break
		}
	}
	return matched
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
