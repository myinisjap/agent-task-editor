package agent

import "fmt"

// RemovedProvidersVersion is the release in which the direct-API providers
// were removed.
const RemovedProvidersVersion = "v0.17.0"

// removedProviders are provider strings that used to be supported but no
// longer have a runner. Rows referencing them may still exist in the database
// (user data is never deleted); they fail at dispatch with a clear message
// rather than crashing the server. "openai" was a historical alias of "llm".
var removedProviders = map[string]bool{"anthropic": true, "llm": true, "openai": true}

// IsRemovedProvider reports whether provider is one of the removed providers.
func IsRemovedProvider(provider string) bool { return removedProviders[provider] }

// RemovedProviderMessage is the operator-facing explanation for a removed provider.
func RemovedProviderMessage(provider string) string {
	return fmt.Sprintf("provider %s was removed in %s; switch this agent config to claude/codex_cli/opencode/qwen_code",
		provider, RemovedProvidersVersion)
}

// ProviderUnavailableMessage explains why no runner exists for provider.
func ProviderUnavailableMessage(provider string) string {
	if IsRemovedProvider(provider) {
		return RemovedProviderMessage(provider)
	}
	return fmt.Sprintf("agent config's provider is disabled or unknown: %q", provider)
}
