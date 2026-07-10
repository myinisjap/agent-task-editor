package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
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
	// Subtasks coordinates child→parent merge-back (optional — nil disables the
	// subtask branching model). Used here to branch a child off its parent's
	// branch and to inject merge-conflict context into a parent's run.
	Subtasks *SubtaskCoordinator
	// Publisher broadcasts WS events (optional — no-op when nil). Used by the
	// cost-budget guard (see checkCostBudget) to publish task.needs_human
	// when a sweep-dispatch is skipped for budget-exhaustion, mirroring how
	// Pool.handleTransientFailure publishes the same event on escalation.
	Publisher Publisher
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
	metrics.DispatchEligibleTasks.Set(float64(len(tasks)))
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

	// Cost-budget guard: only gates the sweep path. DispatchReply (a human
	// actively replying/intervening) is intentionally never budget-gated.
	if blocked, err := d.checkCostBudget(ctx, t, *matched); err != nil {
		log.Error("dispatcher: cost budget check", "err", err)
	} else if blocked {
		return
	}

	if _, err := d.startRun(ctx, t, *matched, runOptions{}); err != nil {
		log.Error("dispatcher: start run", "err", err)
	}
}

// effectiveBudget resolves the effective cost budget for a task from its
// own max_cost_usd and its matched agent config's max_cost_usd. A value of
// 0 means "no cap from that source" (consistent with max_retries=0 meaning
// "disabled" elsewhere). When both are set, the lower (stricter) of the two
// wins; when only one is set, that one wins; when neither is set, the
// result is 0 (unlimited).
func effectiveBudget(taskBudget, configBudget float64) float64 {
	switch {
	case taskBudget <= 0:
		return configBudget
	case configBudget <= 0:
		return taskBudget
	case taskBudget < configBudget:
		return taskBudget
	default:
		return configBudget
	}
}

// checkCostBudget compares a task's cumulative recorded run cost (across
// every run regardless of status — see SumTaskCost) against its effective
// cost budget (the min of the task's and its matched agent config's
// max_cost_usd, whichever nonzero value is lower). If the budget is
// exhausted, it escalates the task to waiting_human WITHOUT starting a new
// provider run: waiting_human is a run-status, not a task label (the task
// itself stays on its current label — see Pool.handleTransientFailure for
// the analogous pattern), so this creates a "phantom" agent_runs row
// directly in that status, locks it as the task's active run so the
// dispatcher skips it on future sweeps, and publishes task.needs_human so
// the dashboard/task-detail UI picks it up live, exactly like a real
// waiting_human escalation. Returns true if dispatch should be skipped.
func (d *Dispatcher) checkCostBudget(ctx context.Context, t gen.Task, matched gen.AgentConfig) (bool, error) {
	budget := effectiveBudget(t.MaxCostUsd, matched.MaxCostUsd)
	if budget <= 0 {
		return false, nil
	}

	spent, err := d.q.SumTaskCost(ctx, t.ID)
	if err != nil {
		return false, fmt.Errorf("sum task cost: %w", err)
	}
	if spent < budget {
		return false, nil
	}

	log := slog.With("component", "dispatcher", "task_id", t.ID)
	msg := fmt.Sprintf("budget exhausted: $%.2f of $%.2f", spent, budget)
	log.Warn("dispatcher: skipping dispatch, cost budget exhausted", "spent", spent, "budget", budget)

	runID := uuid.NewString()
	if _, err := d.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:            runID,
		TaskID:        t.ID,
		AgentConfigID: &matched.ID,
	}); err != nil {
		return true, fmt.Errorf("create budget-exhausted run: %w", err)
	}
	if _, err := d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
		Status: "waiting_human",
		Notes:  &msg,
		ID:     runID,
	}); err != nil {
		return true, fmt.Errorf("set budget-exhausted run status: %w", err)
	}
	// Lock the task on this run, same as a real waiting_human escalation —
	// stays locked until a human acts (raises the budget, or replies via
	// DispatchReply, which is not budget-gated).
	if err := d.q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID,
		ActiveAgentRunID:  &runID,
		ID:                t.ID,
	}); err != nil {
		return true, fmt.Errorf("lock task on budget-exhausted run: %w", err)
	}

	if d.Publisher != nil {
		d.Publisher.Publish("task.needs_human", map[string]any{
			"task_id": t.ID,
			"run_id":  runID,
			"message": msg,
		})
	}

	return true, nil
}

// Sentinel errors for DispatchReply, mapped to HTTP statuses by the handler.
var (
	// ErrRunNotWaiting means the task has no active run in waiting_human state.
	ErrRunNotWaiting = errors.New("task has no agent run waiting for human input")
	// ErrNoMatchingConfig means no enabled agent config could serve the reply run.
	ErrNoMatchingConfig = errors.New("no enabled agent config available for this task")
	// ErrPoolSaturated means the worker pool queue was full and the run was dropped.
	ErrPoolSaturated = errors.New("agent worker pool is full")
)

// DispatchReply starts a new run for a task whose active run is waiting_human,
// carrying the human's textual answer to the agent's request_human question.
// The new run resumes the prior provider session where supported (claude,
// unless the config opts out via resume_sessions), so the reply lands as the
// next message of the same conversation; otherwise it starts cold with the
// reply injected into the prompt. The replied-to run keeps its waiting_human
// status (matching the approve/reject flows); the task's active-run lock moves
// to the new run. Returns the new run's ID.
func (d *Dispatcher) DispatchReply(ctx context.Context, taskID, message string) (string, error) {
	t, err := d.q.GetTask(ctx, taskID)
	if err != nil {
		return "", err // sql.ErrNoRows → 404 in the handler
	}
	if t.ActiveAgentRunID == nil {
		return "", ErrRunNotWaiting
	}
	run, err := d.q.GetAgentRun(ctx, *t.ActiveAgentRunID)
	if err != nil || run.Status != "waiting_human" {
		return "", ErrRunNotWaiting
	}

	// Prefer the config that asked the question; fall back to label matching
	// if it has since been deleted or disabled.
	var matched *gen.AgentConfig
	if run.AgentConfigID != nil {
		if cfg, cerr := d.q.GetAgentConfig(ctx, *run.AgentConfigID); cerr == nil && cfg.Enabled == 1 {
			matched = &cfg
		}
	}
	if matched == nil {
		configs, cerr := d.q.ListAgentConfigs(ctx)
		if cerr != nil {
			return "", cerr
		}
		matched = matchConfig(configs, t.Label)
	}
	if matched == nil {
		return "", ErrNoMatchingConfig
	}

	return d.startRun(ctx, t, *matched, runOptions{humanReply: &message})
}

// runOptions carries the extras a non-sweep dispatch (currently only the
// human-reply flow) layers on top of a standard run.
type runOptions struct {
	humanReply *string
}

// startRun provisions the task's worktree if needed, creates the run row,
// marks it as the task's active run, and submits the job to the pool. Shared
// by the sweep dispatch path and DispatchReply.
func (d *Dispatcher) startRun(ctx context.Context, t gen.Task, matched gen.AgentConfig, opts runOptions) (string, error) {
	log := slog.With("component", "dispatcher", "task_id", t.ID)

	repo, err := d.q.GetRepo(ctx, t.RepoID)
	if err != nil {
		return "", fmt.Errorf("get repo: %w", err)
	}

	// Each task works in its own git worktree on its own branch so concurrent
	// agents on the same repo don't conflict. Reuse the task's worktree across
	// re-runs; provision it on first dispatch. A subtask's branch is cut from its
	// parent's branch (not the repo base) so its work merges back cleanly.
	workDir := t.WorktreePath
	if workDir == "" {
		var wtPath, branch, baseRef string
		var perr error
		if base := d.parentBranchBase(ctx, t); base != "" {
			wtPath, branch, baseRef, perr = provisionWorktreeFrom(ctx, repo.Path, t.ID, t.Title, base)
		} else {
			wtPath, branch, baseRef, perr = provisionWorktree(ctx, repo.Path, t.ID, t.Title)
		}
		if perr != nil {
			return "", fmt.Errorf("provision worktree: %w", perr)
		}
		if err := d.q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{
			Branch:       branch,
			WorktreePath: wtPath,
			BaseRef:      baseRef,
			ID:           t.ID,
		}); err != nil {
			return "", fmt.Errorf("persist worktree: %w", err)
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

	agentCfg := toAgentConfig(matched)

	// Resume the previous run's provider session, when there is one and the
	// config hasn't opted out. Only the claude provider honors this today; the
	// runner falls back to a cold start if the session no longer exists.
	var resumeSessionID string
	if agentCfg.Provider == "claude" && agentCfg.ResumeSessions {
		if sid, serr := d.q.GetLatestTaskSession(ctx, gen.GetLatestTaskSessionParams{
			TaskID:        t.ID,
			AgentConfigID: &matched.ID,
		}); serr == nil && sid != "" {
			resumeSessionID = sid
		}
	}

	// Create the run row and mark the task's active run in a single transaction.
	// These two writes must be atomic: if the run were created but the task never
	// pointed at it (a crash or error between the statements), an orphaned
	// 'pending' run would linger with nothing gating re-dispatch. Committing them
	// together means either both land or neither does.
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	tq := d.q.WithTx(tx)
	if _, err := tq.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:            runID,
		TaskID:        t.ID,
		AgentConfigID: &matched.ID,
		Feedback:      feedback,
	}); err != nil {
		_ = tx.Rollback()
		return "", fmt.Errorf("create agent run: %w", err)
	}
	// Mark the task's active run so the next sweep skips it.
	if err := tq.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &runID,
		ActiveAgentRunID:  &runID,
		ID:                t.ID,
	}); err != nil {
		_ = tx.Rollback()
		return "", fmt.Errorf("set task active run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit run creation: %w", err)
	}

	// Record the human's reply at the top of the new run's log so the
	// conversation reads coherently in the UI (and in WS replay).
	if opts.humanReply != nil && *opts.humanReply != "" {
		if err := d.q.CreateAgentLog(ctx, gen.CreateAgentLogParams{
			ID:         uuid.NewString(),
			AgentRunID: runID,
			Timestamp:  time.Now(),
			Type:       "system",
			Content:    "Human reply: " + *opts.humanReply,
		}); err != nil {
			log.Warn("dispatcher: record human reply log", "err", err)
		}
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

	// If this is a parent with subtasks that conflicted on merge-back, hand the
	// work agent the conflict context so it resolves the merges on this branch.
	var subtaskConflicts *string
	if d.Subtasks != nil {
		subtaskConflicts = d.Subtasks.BuildConflictContext(ctx, t.ID)
	}

	enqueued := d.pool.Submit(Job{
		RunID:    runID,
		Provider: provider,
		Input: RunInput{
			RunID:              runID,
			Task:               Task{ID: t.ID, Title: t.Title, Description: t.Description, Type: t.Type, Label: t.Label, WorkflowID: t.WorkflowID, AgentNotes: t.AgentNotes, Branch: t.Branch, ParentID: derefStr(t.ParentTaskID), RepoPath: repo.Path, Attachments: attachmentRels},
			AgentConfig:        agentCfg,
			RepoPath:           workDir,
			RepoRemoteURL:      derefStr(repo.RemoteUrl),
			Transitions:        transitions,
			Feedback:           feedback,
			PriorPlan:          agentNotes,
			OpenReviewComments: reviewComments,
			AttachmentAbsPaths: attachmentAbsPaths,
			ResumeSessionID:    resumeSessionID,
			HumanReply:         opts.humanReply,
			SubtaskConflicts:   subtaskConflicts,
		},
	})
	if !enqueued {
		_, _ = d.q.SetAgentRunCompleted(ctx, gen.SetAgentRunCompletedParams{
			Status: "failed",
			ID:     runID,
		})
		_ = d.q.ClearActiveAgentRun(ctx, t.ID)
		return "", ErrPoolSaturated
	}

	metrics.DispatchedRunsTotal.Inc()
	log.Info("dispatcher: agent dispatched", "label", t.Label, "agent", matched.Name, "provider", matched.Provider, "agent_id", matched.ID, "agent_enabled", matched.Enabled, "resume_session", resumeSessionID != "", "human_reply", opts.humanReply != nil)
	return runID, nil
}

// parentBranchBase returns the branch a subtask should fork from: its parent's
// branch. Returns "" for a top-level task, or when the parent has no branch yet
// (falls back to the repo base). The parent's branch always exists by the time a
// child is dispatched — the planning run that created the child provisioned the
// parent's worktree at dispatch.
func (d *Dispatcher) parentBranchBase(ctx context.Context, t gen.Task) string {
	if t.ParentTaskID == nil || *t.ParentTaskID == "" {
		return ""
	}
	parent, err := d.q.GetTask(ctx, *t.ParentTaskID)
	if err != nil || parent.Branch == "" {
		return ""
	}
	return parent.Branch
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
		ResumeSessions:    cfg.ResumeSessions != 0,
		SubtasksEnabled:   cfg.SubtasksEnabled != 0,
		MaxSubtasks:       cfg.MaxSubtasks,
		MaxCostUSD:        cfg.MaxCostUsd,
		Env:               env,
		EnabledPlugins:    enabledPlugins,
		EnabledMCPServers: enabledMCPServers,
		CommandAllowlist:  commandAllowlist,
		CommandDenylist:   commandDenylist,
	}
}
