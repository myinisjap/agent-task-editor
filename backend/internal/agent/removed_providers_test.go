package agent

import (
	"strings"
	"testing"
)

func TestProviderUnavailableMessage(t *testing.T) {
	for _, p := range []string{"anthropic", "llm", "openai"} {
		if !IsRemovedProvider(p) {
			t.Fatalf("%s should be removed", p)
		}
		m := ProviderUnavailableMessage(p)
		if !strings.Contains(m, "provider "+p+" was removed in "+RemovedProvidersVersion) || !strings.Contains(m, "claude/codex_cli/opencode/qwen_code") {
			t.Errorf("%s: unexpected message %q", p, m)
		}
	}
	if IsRemovedProvider("claude") {
		t.Error("claude is not removed")
	}
	if m := ProviderUnavailableMessage("bogus"); !strings.Contains(m, "disabled or unknown") {
		t.Errorf("unexpected message %q", m)
	}
}
