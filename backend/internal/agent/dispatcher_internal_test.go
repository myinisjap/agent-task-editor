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

func TestMatchConfigs(t *testing.T) {
	tests := []struct {
		name    string
		configs []gen.AgentConfig
		label   string
		want    []string // matched config names, in expected order; nil for no matches
	}{
		{"no match", []gen.AgentConfig{cfg("a", `["plan"]`)}, "review", nil},
		{"single match", []gen.AgentConfig{cfg("a", `["plan","review"]`)}, "review", []string{"a"}},
		// configs are newest-first; both matches returned in slice order.
		{"multiple matches returned in order", []gen.AgentConfig{cfg("new", `["review"]`), cfg("old", `["review"]`)}, "review", []string{"new", "old"}},
		// unparseable labels are skipped, not fatal — the valid config still matches.
		{"skips bad json", []gen.AgentConfig{cfg("broken", `not json`), cfg("good", `["review"]`)}, "review", []string{"good"}},
		{"all bad json", []gen.AgentConfig{cfg("broken", `{`)}, "review", nil},
		// disabled configs are skipped even if their label matches.
		{"skips disabled", []gen.AgentConfig{disabledCfg("off", `["review"]`)}, "review", nil},
		{"disabled then enabled", []gen.AgentConfig{disabledCfg("off", `["review"]`), cfg("on", `["review"]`)}, "review", []string{"on"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchConfigs(tt.configs, tt.label)
			if len(got) != len(tt.want) {
				t.Fatalf("want %v, got %v", tt.want, namesOf(got))
			}
			for i, w := range tt.want {
				if got[i].Name != w {
					t.Fatalf("want %v, got %v", tt.want, namesOf(got))
				}
			}
		})
	}
}

func namesOf(configs []*gen.AgentConfig) []string {
	names := make([]string, len(configs))
	for i, c := range configs {
		names[i] = c.Name
	}
	return names
}

// TestMatchConfigs_PriorityOrdering verifies matchConfigs preserves whatever
// order the input slice is given in — the priority-asc/created_at-desc sort
// happens in SQL (ListAgentConfigs), not in matchConfigs itself, so this
// confirms matchConfigs doesn't re-sort or otherwise disturb that order.
func TestMatchConfigs_PriorityOrdering(t *testing.T) {
	// Simulates the SQL order for three configs sharing a label with
	// priorities 0, 0, 1 (tie broken by created_at DESC, i.e. newest first).
	configs := []gen.AgentConfig{
		cfg("newest-prio0", `["review"]`),
		cfg("oldest-prio0", `["review"]`),
		cfg("prio1-backup", `["review"]`),
	}
	got := matchConfigs(configs, "review")
	want := []string{"newest-prio0", "oldest-prio0", "prio1-backup"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, namesOf(got))
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("want %v, got %v", want, namesOf(got))
		}
	}
}

// TestEffectiveBudget covers the min-of-(task, config)-nonzero-values
// semantics used by the dispatcher's cost-budget guard: a zero value from
// either source means "no cap from that source", and when both are set the
// stricter (lower) one wins.
func TestEffectiveBudget(t *testing.T) {
	tests := []struct {
		name       string
		taskBudget float64
		cfgBudget  float64
		wantBudget float64
	}{
		{"both zero: unlimited", 0, 0, 0},
		{"only task set", 5, 0, 5},
		{"only config set", 0, 10, 10},
		{"both set, task lower wins", 5, 10, 5},
		{"both set, config lower wins", 10, 5, 5},
		{"both set, equal", 7.5, 7.5, 7.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveBudget(tt.taskBudget, tt.cfgBudget)
			if got != tt.wantBudget {
				t.Fatalf("effectiveBudget(%v, %v) = %v, want %v", tt.taskBudget, tt.cfgBudget, got, tt.wantBudget)
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
