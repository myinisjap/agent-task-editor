package handlers

import (
	"net/http"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/health"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// HealthHandler serves provider/onboarding readiness checks.
type HealthHandler struct {
	q              *gen.Queries
	mcpBinary      string
	repoBaseDir    string
	llmBaseURL     string
	llmAPIKey      string
	backupDir      string
	backupInterval time.Duration
	backupKeep     int
}

// NewHealthHandler constructs a HealthHandler from the relevant server config.
func NewHealthHandler(q *gen.Queries, mcpBinary, repoBaseDir, llmBaseURL, llmAPIKey, backupDir string, backupInterval time.Duration, backupKeep int) *HealthHandler {
	return &HealthHandler{
		q:              q,
		mcpBinary:      mcpBinary,
		repoBaseDir:    repoBaseDir,
		llmBaseURL:     llmBaseURL,
		llmAPIKey:      llmAPIKey,
		backupDir:      backupDir,
		backupInterval: backupInterval,
		backupKeep:     backupKeep,
	}
}

// Providers reports the readiness of each agent provider and supporting piece
// of infrastructure (claude/qwen/opencode binaries, API keys, MCP sidecar, gh
// auth, REPO_BASE_DIR). Provider-specific checks are only emitted for providers
// referenced by an enabled agent config, so the page stays relevant to the
// deployment's actual configuration.
//
// GET /api/v1/health/providers
func (h *HealthHandler) Providers(w http.ResponseWriter, r *http.Request) {
	providers := map[string]bool{}
	if cfgs, err := h.q.ListAgentConfigs(r.Context()); err == nil {
		for _, c := range cfgs {
			providers[c.Provider] = true
		}
	}

	checks := health.Checks(health.Input{
		MCPBinary:      h.mcpBinary,
		RepoBaseDir:    h.repoBaseDir,
		LLMBaseURL:     h.llmBaseURL,
		LLMAPIKey:      h.llmAPIKey,
		Providers:      providers,
		BackupDir:      h.backupDir,
		BackupInterval: h.backupInterval,
		BackupKeep:     h.backupKeep,
	}, nil)

	JSON(w, http.StatusOK, map[string]any{"checks": checks})
}
