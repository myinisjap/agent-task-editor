package agent

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func cfg(name, labelsJSON string) gen.AgentConfig {
	return gen.AgentConfig{ID: name, Name: name, Labels: labelsJSON, Enabled: 1}
}

func disabledCfg(name, labelsJSON string) gen.AgentConfig {
	return gen.AgentConfig{ID: name, Name: name, Labels: labelsJSON, Enabled: 0}
}

func TestMatchConfig(t *testing.T) {
	tests := []struct {
		name    string
		configs []gen.AgentConfig
		label   string
		want    string // matched config name, "" for nil
	}{
		{"no match", []gen.AgentConfig{cfg("a", `["plan"]`)}, "review", ""},
		{"single match", []gen.AgentConfig{cfg("a", `["plan","review"]`)}, "review", "a"},
		// configs are newest-first; first match wins on ambiguity.
		{"ambiguous picks first", []gen.AgentConfig{cfg("new", `["review"]`), cfg("old", `["review"]`)}, "review", "new"},
		// unparseable labels are skipped, not fatal — the valid config still matches.
		{"skips bad json", []gen.AgentConfig{cfg("broken", `not json`), cfg("good", `["review"]`)}, "review", "good"},
		{"all bad json", []gen.AgentConfig{cfg("broken", `{`)}, "review", ""},
		// disabled configs are skipped even if their label matches.
		{"skips disabled", []gen.AgentConfig{disabledCfg("off", `["review"]`)}, "review", ""},
		{"disabled then enabled", []gen.AgentConfig{disabledCfg("off", `["review"]`), cfg("on", `["review"]`)}, "review", "on"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchConfig(tt.configs, tt.label)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("want nil, got %q", got.Name)
				}
				return
			}
			if got == nil || got.Name != tt.want {
				t.Fatalf("want %q, got %v", tt.want, got)
			}
		})
	}
}

// TestToAgentConfig_CommandFilters verifies that CommandAllowlist/CommandDenylist
// JSON columns are unmarshalled into the corresponding AgentConfig slice fields,
// and that malformed/empty JSON falls back to nil (no restriction) rather than
// erroring, mirroring the existing EnabledPlugins/EnabledMCPServers behavior.
func TestToAgentConfig_CommandFilters(t *testing.T) {
	t.Run("populated lists round-trip", func(t *testing.T) {
		g := gen.AgentConfig{
			ID:               "a",
			Env:              "{}",
			CommandAllowlist: `["git *", "npm test"]`,
			CommandDenylist:  `["rm -rf *"]`,
		}
		got := toAgentConfig(g)
		wantAllow := []string{"git *", "npm test"}
		if len(got.CommandAllowlist) != len(wantAllow) {
			t.Fatalf("CommandAllowlist = %+v, want %+v", got.CommandAllowlist, wantAllow)
		}
		for i, w := range wantAllow {
			if got.CommandAllowlist[i] != w {
				t.Fatalf("CommandAllowlist[%d] = %q, want %q", i, got.CommandAllowlist[i], w)
			}
		}
		wantDeny := []string{"rm -rf *"}
		if len(got.CommandDenylist) != len(wantDeny) || got.CommandDenylist[0] != wantDeny[0] {
			t.Fatalf("CommandDenylist = %+v, want %+v", got.CommandDenylist, wantDeny)
		}
	})

	t.Run("default empty-array JSON yields nil slices", func(t *testing.T) {
		g := gen.AgentConfig{
			ID:               "a",
			Env:              "{}",
			CommandAllowlist: "[]",
			CommandDenylist:  "[]",
		}
		got := toAgentConfig(g)
		if len(got.CommandAllowlist) != 0 {
			t.Fatalf("expected empty CommandAllowlist, got %+v", got.CommandAllowlist)
		}
		if len(got.CommandDenylist) != 0 {
			t.Fatalf("expected empty CommandDenylist, got %+v", got.CommandDenylist)
		}
	})

	t.Run("malformed JSON falls back to nil, not an error", func(t *testing.T) {
		g := gen.AgentConfig{
			ID:               "a",
			Env:              "{}",
			CommandAllowlist: "not json",
			CommandDenylist:  "",
		}
		got := toAgentConfig(g)
		if got.CommandAllowlist != nil {
			t.Fatalf("expected nil CommandAllowlist on malformed JSON, got %+v", got.CommandAllowlist)
		}
		if got.CommandDenylist != nil {
			t.Fatalf("expected nil CommandDenylist on empty string, got %+v", got.CommandDenylist)
		}
	})
}
