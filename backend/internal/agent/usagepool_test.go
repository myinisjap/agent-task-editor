package agent

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// poolOfAgentConfig returns the usage pool the dispatcher checks for an agent
// config, so tests can block it in a RateLimitRegistry.
func poolOfAgentConfig(t *testing.T, q *gen.Queries, agentConfigID string) string {
	t.Helper()
	ctx := context.Background()
	cfg, err := q.GetAgentConfig(ctx, agentConfigID)
	if err != nil {
		t.Fatalf("get agent config: %v", err)
	}
	pc, err := q.GetProviderConfig(ctx, cfg.ProviderConfigID)
	if err != nil {
		t.Fatalf("get provider config: %v", err)
	}
	return usagePoolOf(pc)
}

func TestUsagePoolKey(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		env      map[string]string
		want     string
	}{
		{"claude login is shared", "claude", nil, "claude:login"},
		{"claude config dir is its own login", "claude", map[string]string{"CLAUDE_CONFIG_DIR": "/a"}, "claude:login:/a"},
		{"claude api key is its own pool", "claude", map[string]string{"ANTHROPIC_API_KEY": "k"}, "pc1"},
		{"claude bedrock is its own pool", "claude", map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, "pc1"},
		{"unrelated env keeps the shared login", "claude", map[string]string{"FOO": "bar"}, "claude:login"},
		{"codex chatgpt login is shared", "codex_cli", map[string]string{}, "codex_cli:login"},
		{"codex home is its own login", "codex_cli", map[string]string{"CODEX_HOME": "/c"}, "codex_cli:login:/c"},
		{"codex api key is its own pool", "codex_cli", map[string]string{"OPENAI_API_KEY": "k"}, "pc1"},
		{"opencode is per provider config", "opencode", nil, "pc1"},
		{"qwen is per provider config", "qwen_code", nil, "pc1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := UsagePoolKey("pc1", c.provider, c.env); got != c.want {
				t.Errorf("UsagePoolKey = %q, want %q", got, c.want)
			}
		})
	}
}

// TestBlockReasonResolver_RateLimited_SharedLogin verifies a limit recorded
// against one agent config's account blocks every other agent config on the
// same account: two claude provider configs on the shared login, one block.
func TestBlockReasonResolver_RateLimited_SharedLogin(t *testing.T) {
	ctx := context.Background()
	f := newBlockReasonFixture(t)
	var cfgIDs []string
	for i := 0; i < 2; i++ {
		pc, err := f.q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
			ID: uuid.NewString(), Name: "claude", Provider: "claude", Model: "", Env: `{}`,
		})
		if err != nil {
			t.Fatalf("create provider config: %v", err)
		}
		cfg, err := f.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
			ID: uuid.NewString(), Name: "agent", ProviderConfigID: pc.ID,
			Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1, Priority: int64(i),
		})
		if err != nil {
			t.Fatalf("create agent config: %v", err)
		}
		cfgIDs = append(cfgIDs, cfg.ID)
	}
	task := f.newTask(t, "ready")

	rl := NewRateLimitRegistry()
	until := time.Now().Add(time.Hour)
	// Block via the first config's pool, as the pool does after its run.
	rl.Block(poolOfAgentConfig(t, f.q, cfgIDs[0]), until)

	reason := resolveOne(t, NewBlockReasonResolver(f.q, nil, rl, nil), task)
	if reason == nil || reason.Code != BlockRateLimited {
		t.Fatalf("expected both configs on the shared login blocked (%q), got %+v", BlockRateLimited, reason)
	}
}
