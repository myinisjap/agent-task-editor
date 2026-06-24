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
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func main() {
	dbPath := env("DB_PATH", "agent-task-editor.db")
	port := env("PORT", "8080")
	corsOrigins := env("CORS_ORIGINS", "*")
	mcpBinary := env("MCP_SERVER_PATH", "")
	llmBaseURL := env("LLM_BASE_URL", "https://api.openai.com/v1")
	llmAPIKey := env("LLM_API_KEY", "")

	db, err := storage.Open(dbPath)
	if err != nil {
		slog.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("database ready", "path", dbPath)

	seedCtx := context.Background()
	if err := storage.SeedDefaultWorkflow(seedCtx, db); err != nil {
		slog.Error("failed to seed default workflow", "err", err)
		os.Exit(1)
	}

	// Shared workflow engine (publisher wired in Phase 6)
	engine := workflow.New(db.SQL(), nil)

	// Agent provider factory — selects backend based on AgentConfig.Provider
	providerFactory := func(cfg agent.AgentConfig) agent.Provider {
		switch cfg.Provider {
		case "claude":
			var mcp *agent.MCPManager
			if mcpBinary != "" {
				mcp = &agent.MCPManager{ServerBinary: mcpBinary}
			}
			return &agent.ClaudeRunner{MCP: mcp}
		default:
			return &agent.LLMRunner{BaseURL: llmBaseURL, APIKey: llmAPIKey}
		}
	}

	pool := agent.NewPool(5, db.SQL(), engine, nil) // publisher wired in Phase 6
	dispatcher := agent.NewDispatcher(db.SQL(), pool, engine, providerFactory)

	router := api.NewRouter(db, engine, corsOrigins)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
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
		slog.Info("server starting", "port", port)
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
