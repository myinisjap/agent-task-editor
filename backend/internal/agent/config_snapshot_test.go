package agent

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestSnapshotApplyToKeepsIdentity(t *testing.T) {
	src := gen.AgentConfig{
		ID: "a", Name: "src", Labels: `["x"]`, Enabled: 1, Priority: 5,
		SystemPrompt: "p", ProviderConfigID: "pc", Effort: "high", PermissionMode: "plan",
		MaxTurns: 3, MaxTokens: 4, TimeoutSecs: 5, MaxCostUsd: 1.5, MaxRetries: 2, RetryBackoffSecs: 6,
		ResumeSessions: 1, SubtasksEnabled: 1, CrossRepoSubtasksEnabled: 1, MaxSubtasks: 7,
		EnabledPlugins: `["p"]`, EnabledMcpServers: `["m"]`, CommandAllowlist: `["a"]`, CommandDenylist: `["d"]`,
		FallbackProviderConfigIds: `["f"]`, EscalationLadder: `[{"x":1}]`,
	}
	other := gen.AgentConfig{ID: "b", Name: "other", Labels: `["y"]`, Enabled: 0, Priority: 9, EnabledPlugins: "garbage"}
	got := SnapshotOf(src).ApplyTo(other)
	if got.ID != "b" || got.Name != "other" || got.Labels != `["y"]` || got.Enabled != 0 || got.Priority != 9 {
		t.Fatalf("identity changed: %+v", got)
	}
	want := src
	want.ID, want.Name, want.Labels, want.Enabled, want.Priority = other.ID, other.Name, other.Labels, other.Enabled, other.Priority
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if other.SystemPrompt != "" {
		t.Fatal("ApplyTo mutated its argument")
	}
}

func TestSnapshotReviewFanoutRoundTripAndLegacy(t *testing.T) {
	src := gen.AgentConfig{ReviewFanoutEnabled: 1, ReviewFanoutMinLines: 50, ReviewFanoutMinFiles: 3}
	got := SnapshotOf(src).ApplyTo(gen.AgentConfig{})
	if got.ReviewFanoutEnabled != 1 || got.ReviewFanoutMinLines != 50 || got.ReviewFanoutMinFiles != 3 {
		t.Fatalf("round trip: %+v", got)
	}
	// A legacy snapshot (fields absent → zero) must not clobber current thresholds.
	cur := gen.AgentConfig{ReviewFanoutMinLines: 400, ReviewFanoutMinFiles: 15}
	legacy := ConfigSnapshot{}.ApplyTo(cur)
	if legacy.ReviewFanoutMinLines != 400 || legacy.ReviewFanoutMinFiles != 15 || legacy.ReviewFanoutEnabled != 0 {
		t.Fatalf("legacy restore clobbered: %+v", legacy)
	}
}
