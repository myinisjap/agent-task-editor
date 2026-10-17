package agent

import (
	"context"
	"encoding/json"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// A usage pool is the thing a provider's rate/usage limit belongs to: one
// account or API key. RateLimitRegistry blocks are keyed by usage pool, not
// by agent config or task, so once a limit is hit every agent config drawing
// on that account stops being dispatched until it resets, instead of each
// config (and each task) finding out with a run of its own.
//
// By default each provider config is its own pool (keyed by its id). The CLIs
// that keep a single login per container merge into one shared pool when the
// provider config doesn't point them at a separate credential:
//
//   - claude: the `claude login` subscription (or CLAUDE_CONFIG_DIR's login)
//   - codex_cli: the ChatGPT login (or CODEX_HOME's login)
//
// Env vars that select a different credential or endpoint keep the config in
// its own pool.
var usagePoolOwnCredentialEnv = map[string][]string{
	"claude": {
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
	},
	"codex_cli": {"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL"},
}

// usagePoolLoginDirEnv names the env var that moves a provider's shared login
// to another directory, i.e. another account.
var usagePoolLoginDirEnv = map[string]string{
	"claude":    "CLAUDE_CONFIG_DIR",
	"codex_cli": "CODEX_HOME",
}

// UsagePoolKey returns the usage pool for a provider config.
func UsagePoolKey(providerConfigID, provider string, env map[string]string) string {
	ownEnv, shared := usagePoolOwnCredentialEnv[provider]
	if !shared {
		return providerConfigID
	}
	for _, k := range ownEnv {
		if env[k] != "" {
			return providerConfigID
		}
	}
	key := provider + ":login"
	if dir := env[usagePoolLoginDirEnv[provider]]; dir != "" {
		key += ":" + dir
	}
	return key
}

// usagePoolOf returns pc's usage pool, parsing its env JSON.
func usagePoolOf(pc gen.ProviderConfig) string {
	var env map[string]string
	_ = json.Unmarshal([]byte(pc.Env), &env)
	return UsagePoolKey(pc.ID, pc.Provider, env)
}

// usagePools maps provider_config_id to its usage pool, loaded once per
// dispatcher sweep / block-reason pass.
type usagePools map[string]string

func loadUsagePools(ctx context.Context, q *gen.Queries) (usagePools, error) {
	pcs, err := q.ListProviderConfigs(ctx)
	if err != nil {
		return usagePools{}, err
	}
	pools := make(usagePools, len(pcs))
	for _, pc := range pcs {
		pools[pc.ID] = usagePoolOf(pc)
	}
	return pools, nil
}

// ofProvider returns a provider config's usage pool. One missing from the
// map (load failed, or created since) falls back to its own id, the default
// pool.
func (u usagePools) ofProvider(providerConfigID string) string {
	if k, ok := u[providerConfigID]; ok {
		return k
	}
	return providerConfigID
}

// agentProviderIDs returns cfg's providers in the order they are tried: the
// primary provider_config_id, then fallback_provider_config_ids (a JSON
// array; empty or malformed means no fallbacks). Duplicates are dropped.
func agentProviderIDs(cfg *gen.AgentConfig) []string {
	ids := []string{cfg.ProviderConfigID}
	var fallbacks []string
	_ = json.Unmarshal([]byte(cfg.FallbackProviderConfigIds), &fallbacks)
	seen := map[string]bool{cfg.ProviderConfigID: true}
	for _, id := range fallbacks {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// usagePool returns the pool a run's limits are recorded against. Configs
// built without one (tests constructing AgentConfig directly) fall back to
// the agent config id.
func (c AgentConfig) usagePool() string {
	if c.UsagePool != "" {
		return c.UsagePool
	}
	return c.ID
}
