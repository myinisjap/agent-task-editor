package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/api"
	"github.com/myinisjap/agent-task-editor/backend/internal/config"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

func main() {
	cfgPath := os.Getenv("CONFIG_FILE")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	if cfg.APIToken != "" {
		slog.Info("bearer auth enabled")
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

	// Mark any runs left in 'running' from a previous crash as 'failed'.
	if res, err := db.SQL().ExecContext(seedCtx,
		`UPDATE agent_runs SET status='failed', completed_at=CURRENT_TIMESTAMP WHERE status='running'`); err != nil {
		slog.Error("failed to sweep stuck runs", "err", err)
		os.Exit(1)
	} else if n, _ := res.RowsAffected(); n > 0 {
		slog.Warn("marked stuck runs as failed", "count", n)
	}

	// WebSocket hub — satisfies workflow.Publisher and agent.Publisher
	hub := ws.NewHub()

	// Shared workflow engine with WS publisher
	engine := workflow.New(db.SQL(), hub)

	// Agent provider factory — selects backend based on AgentConfig.Provider
	providerFactory := func(agentCfg agent.AgentConfig) agent.Provider {
		switch agentCfg.Provider {
		case "claude":
			var mcp *agent.MCPManager
			if cfg.MCPBinary != "" {
				mcp = &agent.MCPManager{ServerBinary: cfg.MCPBinary}
			}
			return &agent.ClaudeRunner{MCP: mcp}
		case "anthropic":
			// Calls the Anthropic Messages API directly — no CLI binary needed.
			// Requires LLM_API_KEY to be set. Billed per-token (not Claude Max).
			return &agent.AnthropicRunner{APIKey: cfg.LLMAPIKey}
		default:
			return &agent.LLMRunner{BaseURL: cfg.LLMBaseURL, APIKey: cfg.LLMAPIKey}
		}
	}

	maxWorkers := cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 5
	}
	pool := agent.NewPool(maxWorkers, db.SQL(), engine, hub)
	dispatcher := agent.NewDispatcher(db.SQL(), pool, engine, providerFactory)

	router := api.NewRouter(db, engine, hub, cfg.CORSOrigins, cfg.APIToken)

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

	go pool.Start(ctx)
	go dispatcher.Run(ctx)

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
