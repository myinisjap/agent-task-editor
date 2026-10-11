package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent/providers"
	"github.com/myinisjap/agent-task-editor/backend/internal/api"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/backup"
	"github.com/myinisjap/agent-task-editor/backend/internal/config"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghsync"
	"github.com/myinisjap/agent-task-editor/backend/internal/health"
	"github.com/myinisjap/agent-task-editor/backend/internal/logretention"
	"github.com/myinisjap/agent-task-editor/backend/internal/memguard"
	"github.com/myinisjap/agent-task-editor/backend/internal/notify"
	"github.com/myinisjap/agent-task-editor/backend/internal/schedule"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/tasksource"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/worktreesweep"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

// Version is the running build's version. Left as "dev" for local/unstamped
// builds; release images set it via -ldflags "-X main.Version=<tag>" (see
// backend/Dockerfile's VERSION build-arg and .github/workflows/release.yml).
// Surfaced in GET /healthz and the Health page's "Version" row.
var Version = "dev"

func main() {
	// Configure log level from LOG_LEVEL env var (default: INFO).
	logLevel := slog.LevelInfo
	if l := os.Getenv("LOG_LEVEL"); l != "" {
		_ = logLevel.UnmarshalText([]byte(l))
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	cfgPath := os.Getenv("CONFIG_FILE")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	agent.SetGitTimeout(cfg.GitTimeout)

	startTime := time.Now()
	slog.Info("agent-task-editor starting", "version", Version)
	if cfg.APIToken != "" {
		slog.Info("bearer auth enabled")
	} else if cfg.CORSOrigins == "*" {
		slog.Warn("API is unauthenticated (API_TOKEN not set) and CORS_ORIGINS=* — any website open in your browser can call this API from localhost (drive-by CSRF-style request execution, no auth needed). Set API_TOKEN and a specific CORS_ORIGINS before exposing this beyond a trusted single-user machine; see README.md#security")
	} else {
		slog.Warn("API is unauthenticated (API_TOKEN not set); anyone who can reach this port can register repos, create agent configs, and trigger shell-executing agent runs. Set API_TOKEN before exposing this beyond a trusted single-user machine; see README.md#security")
	}
	{
		hp := middleware.NewHostPolicy(cfg.AllowedHosts, cfg.CORSOrigins, cfg.APIToken != "" || len(cfg.APITokens) > 0)
		if hp.Enforced {
			slog.Info("host header check enforced (DNS-rebinding protection)", "allowed_hosts", hp.Hosts, "note", "literal IP hosts always allowed; extend with ALLOWED_HOSTS, or '*' to disable")
		} else {
			slog.Info("host header check not enforced", "reason", hp.Reason)
		}
	}
	switch {
	case cfg.MetricsToken != "":
		slog.Info("metrics endpoint auth: METRICS_TOKEN bearer token required")
	case cfg.MetricsPublic:
		slog.Warn("metrics endpoint auth: public (METRICS_PUBLIC=true); /metrics is unauthenticated")
	case cfg.APIToken != "" || len(cfg.APITokens) > 0:
		slog.Info("metrics endpoint auth: API token required (set METRICS_TOKEN for a dedicated scrape token, or METRICS_PUBLIC=true to leave it open)")
	default:
		slog.Info("metrics endpoint auth: open (no API auth configured)")
	}
	if cfg.RepoBaseDir == "" {
		slog.Warn("REPO_BASE_DIR is not set; any host path can be registered as a repo")
	} else {
		slog.Info("repo base dir enforced", "path", cfg.RepoBaseDir)
	}
	// Resolve upload directory — defaults to "uploads" next to the database.
	uploadDir := cfg.UploadDir
	if uploadDir == "" {
		uploadDir = "uploads"
	}
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		slog.Error("failed to create upload directory", "path", uploadDir, "err", err)
		os.Exit(1)
	}
	slog.Info("upload directory ready", "path", uploadDir)
	if cfg.MCPBinary == "" {
		slog.Warn("MCP_SERVER_PATH is not set; signal_complete/update_task_notes unavailable to claude/qwen agents")
	} else {
		slog.Info("MCP sidecar enabled", "binary", cfg.MCPBinary)
	}

	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	slog.Info("database ready", "path", cfg.DBPath)

	seedCtx := context.Background()
	if err := storage.SeedDefaultWorkflow(seedCtx, db); err != nil {
		slog.Error("failed to seed default workflow", "err", err)
		os.Exit(1)
	}

	// Mark runs left 'running'/'pending' by a previous crash as 'interrupted'
	// (not 'failed') and clear every task's dispatch lock so they re-dispatch;
	// see storage.RecoverInterruptedRuns.
	if n, err := storage.RecoverInterruptedRuns(seedCtx, db); err != nil {
		slog.Error("failed to recover interrupted runs", "err", err)
		os.Exit(1)
	} else if n > 0 {
		slog.Warn("marked stuck runs as interrupted", "count", n)
	}

	// WebSocket hub — satisfies workflow.Publisher and agent.Publisher
	hub := ws.NewHub()

	// Outbound webhook notifier (see internal/notify): POSTs a JSON payload
	// whenever a task needs a human (task.needs_human, arrival at a
	// human-gate label, task.cost_warning, system.cost_budget_tripped).
	// notify.New is nil-safe/no-op when NotifyWebhookURL is empty (the
	// default), so this is always constructed and always started; disabling
	// notifications is purely a matter of leaving NOTIFY_WEBHOOK_URL unset.
	termQ := gen.New(db.SQL())
	notifier := notify.New(cfg.NotifyWebhookURL, cfg.NotifyBaseURL, cfg.NotifyDebounce, termQ)

	// pub fans every WS event out to both the hub and the outbound
	// notifier. Every event producer below (workflow.Engine, agent.Pool,
	// agent.Dispatcher, ghsync.Syncer, tasksource.Importer,
	// schedule.Scheduler) takes its own named single-method Publisher
	// interface with an identical method set, all satisfied by this one
	// concrete *notify.MultiPublisher value. api.NewRouter and the
	// WS-specific handlers still take the concrete *ws.Hub directly (ticket
	// issuing/consuming, per-client broadcast), so hub itself is passed
	// there unchanged.
	pub := notify.FanOut(hub, notifier)

	// Shared workflow engine with the fan-out publisher
	engine := workflow.New(db.SQL(), pub)

	// Subtask coordinator: owns child→parent branch merge-back and parent
	// auto-advance (Mechanism 2). Git identity is filled in once resolved below.
	subtaskCoord := agent.NewSubtaskCoordinator(termQ, engine, pub, "", "")

	// The three hooks below are the bodies of the engine's durable side-effect
	// outbox (see workflow.Engine.RunSideEffects): a transition only records
	// that the work is owed, and a background worker runs it with a background
	// context, retrying with backoff when it returns an error.
	//
	// OnTerminal, on reaching a terminal label: for a subtask, merge its branch
	// back into the parent's branch (see SubtaskCoordinator). For an ordinary
	// task, push the branch (if the repo has a remote) and tear down its
	// worktree to reclaim disk — the branch is kept for human review/PR. The
	// worktree is only removed once the push succeeded, so a retry can push again.
	engine.OnTerminal = func(ctx context.Context, task gen.Task) error {
		repo, err := termQ.GetRepo(ctx, task.RepoID)
		if err != nil {
			return fmt.Errorf("get repo: %w", err)
		}
		if agent.IsSubtask(task) {
			// Children merge back into the parent's branch instead of pushing to
			// origin / keeping their branch.
			subtaskCoord.OnChildTerminal(ctx, task, repo.Path)
			return nil
		}
		if task.WorktreePath == "" {
			return nil
		}
		// PushBranch and RemoveWorktree mutate the repo's shared ref store;
		// serialize against sibling worktrees' concurrent git writes (safety-net
		// commits, subtask merges, dispatcher provisioning, the sweeper) via the
		// per-repo lock. Locked only around these two calls — never wrap the
		// subtask branch above, which takes the parent lock and then the repo
		// lock itself inside SubtaskCoordinator; wrapping here would invert that
		// ordering. See worktree.go's repoGitLocks doc comment / issue #344.
		lock := agent.RepoGitLock(repo.Path)
		lock.Lock()
		defer lock.Unlock()
		if repo.RemoteUrl != nil && *repo.RemoteUrl != "" && task.Branch != "" {
			if err := agent.PushBranch(ctx, task.WorktreePath, task.Branch); err != nil {
				return fmt.Errorf("push branch %s: %w", task.Branch, err)
			}
		}
		if err := agent.RemoveWorktree(ctx, repo.Path, task.WorktreePath); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
		return nil
	}

	// Status write-back to the source GitHub issue for imported tasks (opt-in
	// per repo via issue_writeback_enabled). This instance backs the
	// OnLeaveNotReady hook below; ghsync and the tasks handler construct their
	// own writeback.Writeback internally (see ghsync.New and
	// handlers.NewTasksHandler) since they own their own *gen.Queries.
	// See docs/task-sources.md.
	issueWriteback := writeback.New(termQ)
	engine.OnLeaveNotReady = func(ctx context.Context, task gen.Task) error {
		repo, err := termQ.GetRepo(ctx, task.RepoID)
		if err != nil {
			return fmt.Errorf("get repo: %w", err)
		}
		issueWriteback.OnLeaveNotReady(ctx, task, repo)
		return nil
	}

	// On reaching a label marked create_pr: push the branch and open (or reuse) a
	// PR, reusing the same path as the manual "Create PR" endpoint so both
	// produce an identical PR and issue write-back. A failure (gh
	// unauthenticated, push rejected, forge down) is retried with backoff and
	// surfaced on the task; a missing remote or branch is a precondition, not a
	// failure, and is skipped as before.
	engine.OnCreatePR = func(ctx context.Context, task gen.Task) error {
		repo, err := termQ.GetRepo(ctx, task.RepoID)
		if err != nil {
			return fmt.Errorf("get repo: %w", err)
		}
		if _, _, err := handlers.CreatePRForTask(ctx, termQ, issueWriteback, task, repo); err != nil {
			if handlers.IsPRPrecondition(err) {
				slog.Warn("onCreatePR: skipped", "task_id", task.ID, "err", err)
				return nil
			}
			return err
		}
		return nil
	}

	// Backend base URL the create_subtask MCP sidecar posts to (same container).
	backendURL := "http://localhost:" + cfg.Port

	// priceResolver resolves anthropic/llm model pricing from the
	// user-editable model_pricing table (GET/PUT /api/v1/settings/pricing),
	// falling back to the hardcoded map in providers/pricing.go for any
	// model not present in the DB. It reads the DB fresh on every run
	// completion, so edits via the settings UI take effect immediately.
	priceResolver := providers.DBPriceResolver{Q: termQ}

	// Agent provider factory — selects backend based on AgentConfig.Provider
	providerFactory := func(agentCfg agent.AgentConfig) agent.Provider {
		switch agentCfg.Provider {
		case "claude":
			var mcp *providers.MCPManager
			if cfg.MCPBinary != "" {
				mcp = &providers.MCPManager{ServerBinary: cfg.MCPBinary}
			}
			return &providers.ClaudeRunner{MCP: mcp, UploadDir: uploadDir, BackendURL: backendURL, PriceResolver: priceResolver}
		case "anthropic":
			// Deprecated: disabled for new/updated provider configs (see
			// deprecatedProviders in handlers/providers.go), retained here so
			// existing configs keep dispatching. Calls the Anthropic Messages
			// API directly — no CLI binary needed. Requires LLM_API_KEY to be
			// set. Billed per-token (not Claude Max).
			return &providers.AnthropicRunner{APIKey: cfg.LLMAPIKey, PriceResolver: priceResolver}
		case "opencode":
			return &providers.OpencodeRunner{}
		case "qwen_code":
			var mcp *providers.MCPManager
			if cfg.MCPBinary != "" {
				mcp = &providers.MCPManager{ServerBinary: cfg.MCPBinary}
			}
			return &providers.QwenRunner{MCP: mcp, UploadDir: uploadDir, BackendURL: backendURL, PriceResolver: priceResolver}
		case "codex_cli":
			var mcp *providers.MCPManager
			if cfg.MCPBinary != "" {
				mcp = &providers.MCPManager{ServerBinary: cfg.MCPBinary}
			}
			return &providers.CodexRunner{MCP: mcp, UploadDir: uploadDir, BackendURL: backendURL, PriceResolver: priceResolver}
		case "llm", "openai":
			// Deprecated: disabled for new/updated provider configs (see
			// deprecatedProviders in handlers/providers.go), retained here so
			// existing configs keep dispatching. "openai" is the historical
			// dead dropdown alias for this same OpenAI-compatible path.
			return &providers.LLMRunner{BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey, PriceResolver: priceResolver}
		default:
			// Unrecognized provider string: fail explicitly instead of
			// silently falling back to an OpenAI-compatible call.
			return nil
		}
	}

	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 5
	}
	rateLimits := agent.NewRateLimitRegistry()
	pool := agent.NewPool(maxWorkers, db.SQL(), engine, pub)
	pool.RateLimits = rateLimits
	// Provider circuit breaker: a run classified auth pauses dispatch for its
	// provider kind; in-memory (resets safely on restart), re-checked below.
	providerPauses := agent.NewProviderPauseRegistry(pub)
	pool.ProviderPauses = providerPauses
	// Register gh as git's credential helper for github.com so push/fetch over
	// HTTPS authenticate with GITHUB_TOKEN/gh auth instead of failing with
	// "could not read Username". Best-effort — no-op if gh isn't authenticated.
	if out, err := exec.Command("gh", "auth", "setup-git").CombinedOutput(); err != nil {
		slog.Warn("gh auth setup-git failed; git push to github.com will not be authenticated", "err", err, "out", strings.TrimSpace(string(out)))
	}

	pool.GitName, pool.GitEmail = resolveGitIdentity()
	subtaskCoord.GitName, subtaskCoord.GitEmail = pool.GitName, pool.GitEmail
	pool.Subtasks = subtaskCoord
	dispatcher := agent.NewDispatcher(db.SQL(), pool, engine, providerFactory)
	dispatcher.RateLimits = rateLimits
	dispatcher.ProviderPauses = providerPauses
	dispatcher.SetUploadDir(uploadDir)
	dispatcher.Subtasks = subtaskCoord
	dispatcher.Publisher = pub
	dispatcher.MaxDailyCostUSD = cfg.MaxDailyCostUSD
	dispatcher.MaxMonthlyCostUSD = cfg.MaxMonthlyCostUSD

	// Shares the dispatcher's own collaborators (queries, pool, rate-limit
	// registry, and the dispatcher itself for the global cost-ceiling status)
	// so the read-time /tasks block_reason field can never drift from what
	// the dispatcher itself would actually do — see issue #353.
	blockReasons := agent.NewBlockReasonResolver(gen.New(db.SQL()), pool, rateLimits, dispatcher)

	// Interactive chat terminal — runs each session's provider CLI in a PTY, one
	// live process per session (no shared concurrency budget; a session is a
	// single interactive process). Streamed over the /chat/sessions/{id}/terminal
	// WebSocket.
	terminal := agent.NewTerminalManager()
	// Scope each chat session's CLI subprocess env to that provider's
	// allowlist instead of the backend's full environment — the interactive
	// terminal launches the same claude/codex/qwen/opencode binaries (with
	// the same Bash access) as the headless runners, so it needs the same
	// #321 protection against leaking backend-only secrets (LLM_API_KEY,
	// API_TOKEN, DB creds, etc.) to the CLI's shell.
	terminal.EnvAllowlist = providers.EnvAllowlistFor
	// Expose the board MCP tools (list_repos/list_workflows/create_task) to chat
	// sessions when MCP_BOARD_PATH points at the mcp-board binary. This is a
	// human-driven surface (the chat tab), deliberately separate from the in-flow
	// kanban agents, which never get task-creation tools. Nil when unset.
	if cfg.MCPBoardBinary != "" {
		terminal.ChatMCP = providers.NewChatMCPProvisioner(cfg.MCPBoardBinary, backendURL)
		slog.Info("board MCP enabled for chat sessions", "binary", cfg.MCPBoardBinary)
	}
	// CHAT_MAX_SESSIONS/CHAT_IDLE_TIMEOUT bound the otherwise-unbounded growth
	// of concurrent PTY subprocesses + scrollback buffers a long-lived chat
	// tab can accumulate. Both default to 0 (unlimited/disabled), so this is a
	// no-op unless explicitly configured.
	terminal.MaxSessions = cfg.ChatMaxSessions
	terminal.IdleTimeout = cfg.ChatIdleTimeout
	if cfg.ChatMaxSessions > 0 {
		slog.Info("chat terminal session cap enabled", "max_sessions", cfg.ChatMaxSessions)
	}
	if cfg.ChatIdleTimeout > 0 {
		slog.Info("chat terminal idle reaper enabled", "idle_timeout", cfg.ChatIdleTimeout)
	}

	router := api.NewRouter(db, engine, hub, cfg.CORSOrigins, cfg.APIToken, cfg.APITokens, cfg.RepoBaseDir, uploadDir, cfg.MCPBinary, cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.BackupDir, cfg.BackupInterval, cfg.BackupKeep, pool, dispatcher, cfg.MetricsToken, cfg.MetricsPublic, Version, cfg.UpdateCheckEnabled, terminal, maxWorkers, dispatcher, blockReasons, cfg.AllowedHosts)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Root context cancelled on shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// GitHub PR status auto-sync: polls all eligible tasks on a configurable
	// interval and pushes "task.git_state_changed" WebSocket events so the
	// board refreshes automatically without a page reload. Also ingests PR
	// review feedback / failed GHA checks (see internal/ghsync/pr_review.go);
	// engine is passed through so a repo can opt into auto-transitioning a
	// task back to its failure-path label when new feedback arrives.
	ghSyncer := ghsync.New(db.SQL(), pub, cfg.GitHubSyncInterval, engine)
	slog.Info("github sync enabled", "interval", cfg.GitHubSyncInterval)

	// Issue import: polls repos with issue sync enabled, creates tasks from
	// matching open issues (deduped by tasks.source/source_ref), keeps
	// existing tasks in sync with title/body/label drift, ingests the issue's
	// comment thread when opted in, and reconciles issues that close or drop
	// out of the filter. engine is passed through so a repo can opt into the
	// "move" gone-action (issue_sync_gone_action) transitioning the task to a
	// designated label; nil-safe if omitted.
	//
	// Both GitHubIssues and GiteaIssues are always configured here (via
	// NewWithEngineMulti): Importer.resolveSource picks whichever Source's
	// AppliesTo matches a given repo's remote URL per sweep, so this single
	// importer handles both without needing separate pollers. GiteaIssues is
	// inert (AppliesTo never matches) unless GITEA_HOST is configured — see
	// internal/forge/gitea's package doc — so this is a no-op behavior change
	// for anyone not running a self-hosted Gitea.
	issueImporter := tasksource.NewWithEngineMulti(db.SQL(), pub, cfg.IssueSyncInterval,
		[]tasksource.Source{tasksource.GitHubIssues{}, tasksource.GiteaIssues{}}, engine)
	slog.Info("issue import enabled", "interval", cfg.IssueSyncInterval)

	// Recurring task schedules: fires task_templates on a cron expression
	// against a repo, deduped by an open task from a prior firing.
	taskScheduler := schedule.New(db.SQL(), pub, cfg.ScheduleInterval)
	slog.Info("task schedule sweep enabled", "interval", cfg.ScheduleInterval)

	// Automatic local backups: optional. When BACKUP_DIR is set, periodically
	// writes a rotated VACUUM INTO snapshot to that directory. Always
	// available regardless of this setting: GET /api/v1/backup (on-demand)
	// and the Health page's "Download backup" button. See docs/backup.md.
	//
	// Interval/keep are DB-backed (backup_settings, seeded from
	// cfg.BackupInterval/cfg.BackupKeep on first migration) and editable at
	// runtime via PUT /api/v1/backup/settings (or the Health page), without a
	// restart — the scheduler re-reads them from the DB before every run
	// (see backup.NewWithSettingsFunc). BACKUP_DIR itself remains a
	// deploy-time-only choice (enabling/disabling the scheduler requires a
	// restart), since it names a filesystem location.
	var backupScheduler *backup.Scheduler
	if cfg.BackupDir != "" {
		backupScheduler = backup.NewWithSettingsFunc(db, cfg.BackupDir, func() (time.Duration, int) {
			row, err := termQ.GetBackupSettings(context.Background())
			if err != nil {
				slog.Warn("backup: failed to load settings from DB; using config defaults", "err", err)
				return cfg.BackupInterval, cfg.BackupKeep
			}
			return time.Duration(row.IntervalSeconds) * time.Second, int(row.Keep)
		})
		slog.Info("automatic local backups enabled", "dir", cfg.BackupDir)
	} else {
		slog.Info("automatic local backups disabled; set BACKUP_DIR to enable (see docs/backup.md)")
	}

	// Agent-log retention: periodically deletes agent_logs rows for
	// terminal-status runs (completed/failed/waiting_human) whose
	// completed_at is older than the configured number of days. Disabled by
	// default (days=0) so DB growth behavior is unchanged unless explicitly
	// opted in. See docs/backup.md#agent-log-retention.
	//
	// Days/interval are DB-backed (log_retention_settings, seeded from
	// cfg.LogRetentionDays/cfg.LogRetentionInterval on first migration) and
	// editable at runtime via PUT /api/v1/log-retention/settings (or the
	// Health page), without a restart — the pruner re-reads them from the DB
	// before every run (see logretention.NewWithSettingsFunc). Unlike
	// backup (gated by BACKUP_DIR), there's no deploy-time enable gate here:
	// the pruner always runs and simply no-ops while days=0.
	logPruner := logretention.NewWithSettingsFunc(termQ, func() (int, time.Duration) {
		row, err := termQ.GetLogRetentionSettings(context.Background())
		if err != nil {
			slog.Warn("logretention: failed to load settings from DB; using config defaults", "err", err)
			return cfg.LogRetentionDays, cfg.LogRetentionInterval
		}
		return int(row.Days), time.Duration(row.IntervalSeconds) * time.Second
	})
	slog.Info("agent log retention pruner starting; effective settings are DB-backed and editable via PUT /api/v1/log-retention/settings (see docs/backup.md#agent-log-retention)", "seed_default_days", cfg.LogRetentionDays)

	// Orphaned-worktree sweeper: reconciles every repo's .ate-worktrees/<id>
	// directories against live (non-archived) task/chat-session ids on an
	// interval, reclaiming anything else. Always on — this is what bounds
	// .ate-worktrees/ disk usage by live tasks rather than by all tasks ever
	// created, catching both archive-time teardown misses and crash orphans.
	// See internal/worktreesweep and docs/backup.md.
	worktreeSweeper := worktreesweep.New(termQ, cfg.WorktreeSweepInterval)
	slog.Info("worktree sweeper starting", "interval", cfg.WorktreeSweepInterval)

	poolDone := make(chan struct{})
	go func() {
		defer close(poolDone)
		pool.Start(ctx)
	}()
	// Durable side-effect outbox worker (push, PR creation, write-back); it
	// stops when ctx is cancelled on shutdown.
	sideEffectsDone := make(chan struct{})
	go func() {
		defer close(sideEffectsDone)
		engine.RunSideEffects(ctx)
	}()
	go memguard.New(pool).Run(ctx)
	// The dispatcher has its own context so shutdown can stop it starting new
	// runs while in-flight ones drain (the root ctx stays live until then).
	dispatchCtx, stopDispatch := context.WithCancel(ctx)
	dispatchDone := make(chan struct{})
	go func() {
		defer close(dispatchDone)
		dispatcher.Run(dispatchCtx)
	}()
	// Auto-resume paused providers once their existing health check passes.
	go providerPauses.RunRecheck(ctx, 30*time.Second, func(provider string) bool {
		return health.ProviderReady(provider, health.Input{LLMBaseURL: cfg.LLMBaseURL, LLMAPIKey: cfg.LLMAPIKey}, nil)
	})
	go ghSyncer.Run(ctx)
	go issueImporter.Run(ctx)
	go taskScheduler.Run(ctx)
	if backupScheduler != nil {
		go backupScheduler.Run(ctx)
	}
	go logPruner.Run(ctx)
	go worktreeSweeper.Run(ctx)
	// No-op immediately when NOTIFY_WEBHOOK_URL is unset — notifier.Run
	// returns as soon as it observes the disabled Notifier.
	go notifier.Run(ctx)
	if cfg.ChatIdleTimeout > 0 {
		go terminal.ReapLoop(ctx)
	}

	go func() {
		slog.Info("server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			cancel()
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	// Log which signal triggered shutdown and how long we'd been up, so a
	// restart loop can be traced to its source (docker stop, OOM, an external
	// kill) instead of just showing a bare "shutting down".
	slog.Info("shutting down...", "signal", sig.String(), "uptime", time.Since(startTime).Round(time.Second).String())

	// Shutdown order:
	//  1. stop the dispatcher (no new runs) and wait for its sweep to finish;
	//  2. drain the pool: in-flight runs keep going for SHUTDOWN_DRAIN_TIMEOUT
	//     with HTTP (agent MCP sidecars post to it) and the side-effect worker
	//     still up, then the rest are cancelled and recorded as interrupted;
	//  3. cancel the root context, stopping every other background loop;
	//  4. shut down HTTP, wait for the side-effect worker to record its state;
	//  5. wait for the pool's workers, so nothing touches the DB after the
	//     deferred db.Close().
	stopDispatch()
	<-dispatchDone
	slog.Info("draining in-flight agent runs", "timeout", cfg.ShutdownDrainTimeout.String())
	pool.Shutdown(cfg.ShutdownDrainTimeout)
	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
	// An interrupted side effect is left pending in the outbox and resumes on
	// the next start; give the worker a moment to record that.
	select {
	case <-sideEffectsDone:
	case <-shutCtx.Done():
	}
	select {
	case <-poolDone:
	case <-shutCtx.Done():
		slog.Warn("agent pool workers did not exit before the shutdown deadline")
	}
}

// resolveGitIdentity returns (name, email) for safety-net commits.
// Tries `git config` first (covers local dev), then `gh api user` (covers containers with gh auth).
// Falls back to ("ate", "ate") if neither works.
func resolveGitIdentity() (string, string) {
	name := strings.TrimSpace(runOutput("git", "config", "user.name"))
	email := strings.TrimSpace(runOutput("git", "config", "user.email"))
	if name != "" && email != "" {
		slog.Info("git identity resolved from git config", "name", name, "email", email)
		return name, email
	}
	// gh api user returns JSON; parse with simple prefix matching to avoid import churn.
	out := runOutput("gh", "api", "user", "--jq", "[.name,.email,.login] | @tsv")
	parts := strings.Split(strings.TrimSpace(out), "\t")
	if len(parts) == 3 {
		ghName, ghEmail, ghLogin := parts[0], parts[1], parts[2]
		if name == "" && ghName != "" && ghName != "null" {
			name = ghName
		}
		if email == "" && ghEmail != "" && ghEmail != "null" {
			email = ghEmail
		}
		if email == "" && ghLogin != "" && ghLogin != "null" {
			email = ghLogin + "@users.noreply.github.com"
		}
		if name == "" {
			name = ghLogin
		}
	}
	if name == "" {
		name = "ate"
	}
	if email == "" {
		email = "ate"
	}
	slog.Info("git identity resolved", "name", name, "email", email)
	return name, email
}

func runOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}
