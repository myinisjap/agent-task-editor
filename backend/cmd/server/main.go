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
	"github.com/myinisjap/agent-task-editor/backend/internal/api"
	"github.com/myinisjap/agent-task-editor/backend/internal/backup"
	"github.com/myinisjap/agent-task-editor/backend/internal/config"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghsync"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/tasksource"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

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

	if cfg.APIToken != "" {
		slog.Info("bearer auth enabled")
	}
	if cfg.MetricsToken != "" {
		slog.Info("metrics endpoint bearer auth enabled")
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

	// Mark any runs left in 'running' or 'pending' from a previous crash as
	// 'failed'. 'pending' runs are ones the dispatcher created but the pool never
	// started (or the process died between creating the run and enqueuing it);
	// without this sweep they'd linger forever. The following statement then
	// clears every task's active_agent_run_id, so these tasks become dispatchable
	// again on the next sweep.
	if res, err := db.SQL().ExecContext(seedCtx,
		`UPDATE agent_runs SET status='failed', completed_at=CURRENT_TIMESTAMP WHERE status IN ('running','pending')`); err != nil {
		slog.Error("failed to sweep stuck runs", "err", err)
		os.Exit(1)
	} else if n, _ := res.RowsAffected(); n > 0 {
		slog.Warn("marked stuck runs as failed", "count", n)
	}

	// Clear active_agent_run_id for all tasks — the worker pool has restarted
	// so nothing is genuinely active from the previous process.
	if _, err := db.SQL().ExecContext(seedCtx,
		`UPDATE tasks SET active_agent_run_id = NULL WHERE active_agent_run_id IS NOT NULL`); err != nil {
		slog.Error("failed to clear active agent runs", "err", err)
		os.Exit(1)
	}

	// WebSocket hub — satisfies workflow.Publisher and agent.Publisher
	hub := ws.NewHub()

	// Shared workflow engine with WS publisher
	engine := workflow.New(db.SQL(), hub)

	// Subtask coordinator: owns child→parent branch merge-back and parent
	// auto-advance (Mechanism 2). Git identity is filled in once resolved below.
	termQ := gen.New(db.SQL())
	subtaskCoord := agent.NewSubtaskCoordinator(termQ, engine, hub, "", "")

	// On reaching a terminal label: for a subtask, merge its branch back into the
	// parent's branch (see SubtaskCoordinator). For an ordinary task, push the
	// branch (if the repo has a remote) and tear down its worktree to reclaim
	// disk — the branch is kept for human review/PR. Best-effort.
	engine.OnTerminal = func(ctx context.Context, task gen.Task) {
		repo, err := termQ.GetRepo(ctx, task.RepoID)
		if err != nil {
			slog.Warn("onTerminal: get repo", "task_id", task.ID, "err", err)
			return
		}
		if agent.IsSubtask(task) {
			// Children merge back into the parent's branch instead of pushing to
			// origin / keeping their branch.
			subtaskCoord.OnChildTerminal(ctx, task, repo.Path)
			return
		}
		if task.WorktreePath == "" {
			return
		}
		if repo.RemoteUrl != nil && *repo.RemoteUrl != "" && task.Branch != "" {
			if err := agent.PushBranch(ctx, task.WorktreePath, task.Branch); err != nil {
				slog.Warn("onTerminal: push branch", "task_id", task.ID, "branch", task.Branch, "err", err)
			}
		}
		if err := agent.RemoveWorktree(ctx, repo.Path, task.WorktreePath); err != nil {
			slog.Warn("onTerminal: remove worktree", "task_id", task.ID, "err", err)
		}
	}

	// Backend base URL the create_subtask MCP sidecar posts to (same container).
	backendURL := "http://localhost:" + cfg.Port

	// Agent provider factory — selects backend based on AgentConfig.Provider
	providerFactory := func(agentCfg agent.AgentConfig) agent.Provider {
		switch agentCfg.Provider {
		case "claude":
			var mcp *agent.MCPManager
			if cfg.MCPBinary != "" {
				mcp = &agent.MCPManager{ServerBinary: cfg.MCPBinary}
			}
			return &agent.ClaudeRunner{MCP: mcp, UploadDir: uploadDir, BackendURL: backendURL, APIToken: cfg.APIToken}
		case "anthropic":
			// Calls the Anthropic Messages API directly — no CLI binary needed.
			// Requires LLM_API_KEY to be set. Billed per-token (not Claude Max).
			return &agent.AnthropicRunner{APIKey: cfg.LLMAPIKey}
		case "opencode":
			return &agent.OpencodeRunner{}
		case "qwen_code":
			var mcp *agent.MCPManager
			if cfg.MCPBinary != "" {
				mcp = &agent.MCPManager{ServerBinary: cfg.MCPBinary}
			}
			return &agent.QwenRunner{MCP: mcp, UploadDir: uploadDir, BackendURL: backendURL, APIToken: cfg.APIToken}
		case "gemini_cli":
			var mcp *agent.MCPManager
			if cfg.MCPBinary != "" {
				mcp = &agent.MCPManager{ServerBinary: cfg.MCPBinary}
			}
			return &agent.GeminiRunner{MCP: mcp, UploadDir: uploadDir, BackendURL: backendURL, APIToken: cfg.APIToken}
		case "codex_cli":
			var mcp *agent.MCPManager
			if cfg.MCPBinary != "" {
				mcp = &agent.MCPManager{ServerBinary: cfg.MCPBinary}
			}
			return &agent.CodexRunner{MCP: mcp, UploadDir: uploadDir, BackendURL: backendURL, APIToken: cfg.APIToken}
		default:
			return &agent.LLMRunner{BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey}
		}
	}

	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 5
	}
	rateLimits := agent.NewRateLimitRegistry()
	pool := agent.NewPool(maxWorkers, db.SQL(), engine, hub)
	pool.RateLimits = rateLimits
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
	dispatcher.SetUploadDir(uploadDir)
	dispatcher.Subtasks = subtaskCoord
	dispatcher.Publisher = hub

	router := api.NewRouter(db, engine, hub, cfg.CORSOrigins, cfg.APIToken, cfg.APITokens, cfg.RepoBaseDir, uploadDir, cfg.MCPBinary, cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.BackupDir, cfg.BackupInterval, cfg.BackupKeep, pool, dispatcher, cfg.MetricsToken)

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
	// board refreshes automatically without a page reload.
	ghSyncer := ghsync.New(db.SQL(), hub, cfg.GitHubSyncInterval)
	slog.Info("github sync enabled", "interval", cfg.GitHubSyncInterval)

	// GitHub Issues import: polls repos with issue sync enabled and creates
	// tasks from matching open issues (deduped by tasks.source/source_ref).
	issueImporter := tasksource.New(db.SQL(), hub, cfg.IssueSyncInterval, tasksource.GitHubIssues{})
	slog.Info("github issue import enabled", "interval", cfg.IssueSyncInterval)

	// Automatic local backups: optional. When BACKUP_DIR is set, periodically
	// writes a rotated VACUUM INTO snapshot to that directory. Always
	// available regardless of this setting: GET /api/v1/backup (on-demand)
	// and the Health page's "Download backup" button. See docs/backup.md.
	var backupScheduler *backup.Scheduler
	if cfg.BackupDir != "" {
		backupScheduler = backup.New(db, cfg.BackupDir, cfg.BackupInterval, cfg.BackupKeep)
		slog.Info("automatic local backups enabled", "dir", cfg.BackupDir, "interval", cfg.BackupInterval, "keep", cfg.BackupKeep)
	} else {
		slog.Info("automatic local backups disabled; set BACKUP_DIR to enable (see docs/backup.md)")
	}

	go pool.Start(ctx)
	go dispatcher.Run(ctx)
	go ghSyncer.Run(ctx)
	go issueImporter.Run(ctx)
	if backupScheduler != nil {
		go backupScheduler.Run(ctx)
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
	<-quit

	slog.Info("shutting down...")
	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("shutdown error", "err", err)
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
