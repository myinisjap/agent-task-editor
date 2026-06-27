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
		{"no match", []gen.AgentConfig{cfg("a", `["todo"]`)}, "review", ""},
		{"single match", []gen.AgentConfig{cfg("a", `["todo","review"]`)}, "review", "a"},
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
