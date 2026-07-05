package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// Dispatcher sweeps the database on an interval, picks up tasks that are
// in agent-triggerable labels, and submits them to the Pool.
type Dispatcher struct {
	pool      *Pool
	db        *sql.DB
	q         *gen.Queries
	engine    *workflow.Engine
	interval  time.Duration
	uploadDir string
	// ProviderFactory builds a Provider for a given AgentConfig.
	ProviderFactory func(cfg AgentConfig) Provider
	// RateLimits is the shared rate-limit registry (optional — no-op when nil).
	RateLimits *RateLimitRegistry
}

// NewDispatcher creates a Dispatcher with a 5-second sweep interval.
func NewDispatcher(db *sql.DB, pool *Pool, engine *workflow.Engine, factory func(AgentConfig) Provider) *Dispatcher {
	return &Dispatcher{
		pool:            pool,
		db:              db,
		q:               gen.New(db),
		engine:          engine,
		interval:        5 * time.Second,
		ProviderFactory: factory,
	}
}

// SetUploadDir configures the directory where task attachment images are stored.
func (d *Dispatcher) SetUploadDir(dir string) {
	d.uploadDir = dir
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
	log := slog.With("component", "dispatcher", "task_id", t.ID)

	if t.Paused != 0 { // defense-in-depth; ListAgentPickupTasks already filters paused tasks
		log.Debug("dispatcher: skipping paused task")
		return
	}

	matched := matchConfig(configs, t.Label)
	if matched == nil {
		log.Debug("dispatcher: no active config for label", "label", t.Label)
		return
	}

	// Skip dispatch if the agent config is currently rate-limited.
	if d.RateLimits != nil {
		if blocked, until := d.RateLimits.IsBlocked(matched.ID); blocked {
			log.Info("dispatcher: skipping rate-limited agent config", "agent_config_id", matched.ID, "unblocked_at", until)
			return
		}
	}

	repo, err := d.q.GetRepo(ctx, t.RepoID)
	if err != nil {
		log.Error("dispatcher: get repo", "err", err)
		return
	}

	// Each task works in its own git worktree on its own branch so concurrent
	// agents on the same repo don't conflict. Reuse the task's worktree across
	// re-runs; provision it on first dispatch.
	workDir := t.WorktreePath
	if workDir == "" {
		wtPath, branch, baseRef, perr := provisionWorktree(ctx, repo.Path, t.ID, t.Title)
		if perr != nil {
			log.Error("dispatcher: provision worktree", "err", perr)
			return
		}
		if err := d.q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{
			Branch:       branch,
			WorktreePath: wtPath,
			BaseRef:      baseRef,
			ID:           t.ID,
		}); err != nil {
			log.Error("dispatcher: persist worktree", "err", err)
			return
		}
		workDir = wtPath
	}

	runID := uuid.NewString()
	log = log.With("run_id", runID)
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

	// Create the run row and mark the task's active run in a single transaction.
	// These two writes must be atomic: if the run were created but the task never
	// pointed at it (a crash or error between the statements), an orphaned
	// 'pending' run would linger with nothing gating re-dispatch. Committing them
	// together means either both land or neither does.
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		log.Error("dispatcher: begin tx", "err", err)
		return
	}
	tq := d.q.WithTx(tx)
	if _, err := tq.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:            runID,
		TaskID:        t.ID,
		AgentConfigID: &matched.ID,
		Feedback:      feedback,
	}); err != nil {
		_ = tx.Rollback()
		log.Error("dispatcher: create agent run", "err", err)
		return
	}
	// Mark the task's active run so the next sweep skips it.
	if err := tq.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID,
		ActiveAgentRunID:  &runID,
		ID:                t.ID,
	}); err != nil {
		_ = tx.Rollback()
		log.Error("dispatcher: set task active run", "err", err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Error("dispatcher: commit run creation", "err", err)
		return
	}

	// Parse task attachments from JSON.
	var attachmentRels []string
	if t.Attachments != "" && t.Attachments != "[]" {
		_ = json.Unmarshal([]byte(t.Attachments), &attachmentRels)
	}

	// Build absolute paths for attachments.
	var attachmentAbsPaths []string
	for _, rel := range attachmentRels {
		if d.uploadDir != "" {
			attachmentAbsPaths = append(attachmentAbsPaths, filepath.Join(d.uploadDir, rel))
		}
	}

	// Copy attachment images into the worktree so the agent can read them via file tools.
	if len(attachmentAbsPaths) > 0 && workDir != "" {
		if err := copyAttachmentsToWorktree(workDir, attachmentAbsPaths); err != nil {
			log.Warn("dispatcher: copy attachments to worktree", "err", err)
		}
	}

	// Open inline diff review comments are injected into the prompt on every
	// run until an agent (or a human) resolves them.
	var reviewComments []ReviewComment
	if rows, err := d.q.ListOpenTaskReviewComments(ctx, t.ID); err != nil {
		log.Warn("dispatcher: list open review comments", "err", err)
	} else {
		for _, c := range rows {
			reviewComments = append(reviewComments, ReviewComment{
				ID:         c.ID,
				FilePath:   c.FilePath,
				Side:       c.Side,
				StartLine:  c.StartLine,
				EndLine:    c.EndLine,
				QuotedText: c.QuotedText,
				Body:       c.Body,
			})
		}
	}

	transitions := d.buildTransitionHints(ctx, t.ID, t.WorkflowID, t.Label)
	provider := d.ProviderFactory(agentCfg)

	enqueued := d.pool.Submit(Job{
		RunID:    runID,
		Provider: provider,
		Input: RunInput{
			RunID:              runID,
			Task:               Task{ID: t.ID, Title: t.Title, Description: t.Description, Type: t.Type, Label: t.Label, WorkflowID: t.WorkflowID, AgentNotes: t.AgentNotes, Branch: t.Branch, Attachments: attachmentRels},
			AgentConfig:        agentCfg,
			RepoPath:           workDir,
			RepoRemoteURL:      derefStr(repo.RemoteUrl),
			Transitions:        transitions,
			Feedback:           feedback,
			PriorPlan:          agentNotes,
			OpenReviewComments: reviewComments,
			AttachmentAbsPaths: attachmentAbsPaths,
		},
	})
	if !enqueued {
		log.Warn("dispatcher: pool full, dropping job")
		_, _ = d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
			Status: "failed",
			ID:     runID,
		})
		_ = d.q.ClearActiveAgentRun(ctx, t.ID)
		return
	}

	log.Info("dispatcher: agent dispatched", "label", t.Label, "agent", matched.Name, "provider", matched.Provider, "agent_id", matched.ID, "agent_enabled", matched.Enabled)
}

func (d *Dispatcher) buildTransitionHints(ctx context.Context, taskID, workflowID, fromLabel string) []TransitionHint {
	all, err := d.q.ListWorkflowTransitions(ctx, workflowID)
	if err != nil {
		slog.Warn("dispatcher: build transition hints", "component", "dispatcher", "task_id", taskID, "workflow_id", workflowID, "err", err)
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

// copyAttachmentsToWorktree copies attachment files into <worktreePath>/.task_attachments/
// so the agent can read them using its file-access tools.
func copyAttachmentsToWorktree(worktreePath string, absPaths []string) error {
	dst := filepath.Join(worktreePath, ".task_attachments")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, src := range absPaths {
		filename := filepath.Base(src)
		dstFile := filepath.Join(dst, filename)
		if err := copyFile(src, dstFile); err != nil {
			slog.Warn("copyAttachmentsToWorktree: skip file", "component", "dispatcher", "src", src, "err", err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck
	_, err = io.Copy(out, in)
	return err
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toAgentConfig(cfg gen.AgentConfig) AgentConfig {
	var env map[string]string
	_ = json.Unmarshal([]byte(cfg.Env), &env)
	if env == nil {
		env = map[string]string{}
	}
	var enabledPlugins []string
	_ = json.Unmarshal([]byte(cfg.EnabledPlugins), &enabledPlugins)
	var enabledMCPServers []string
	_ = json.Unmarshal([]byte(cfg.EnabledMcpServers), &enabledMCPServers)
	var commandAllowlist []string
	_ = json.Unmarshal([]byte(cfg.CommandAllowlist), &commandAllowlist)
	var commandDenylist []string
	_ = json.Unmarshal([]byte(cfg.CommandDenylist), &commandDenylist)
	return AgentConfig{
		ID:                cfg.ID,
		Name:              cfg.Name,
		Provider:          cfg.Provider,
		Model:             cfg.Model,
		SystemPrompt:      cfg.SystemPrompt,
		MaxTokens:         cfg.MaxTokens,
		TimeoutSecs:       cfg.TimeoutSecs,
		MaxTurns:          cfg.MaxTurns,
		MaxRetries:        cfg.MaxRetries,
		RetryBackoffSecs:  cfg.RetryBackoffSecs,
		Env:               env,
		EnabledPlugins:    enabledPlugins,
		EnabledMCPServers: enabledMCPServers,
		CommandAllowlist:  commandAllowlist,
		CommandDenylist:   commandDenylist,
	}
}
