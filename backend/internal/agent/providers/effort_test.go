package providers

import "testing"

func TestClaudeEffort(t *testing.T) {
	cases := []struct {
		level  string
		want   string
		wantOK bool
	}{
		{"", "", false},
		{"low", "low", true},
		{"medium", "medium", true},
		{"high", "high", true},
		{"xhigh", "xhigh", true},
		{"max", "max", true},
		{"off", "", false},
		{"bogus", "", false},
	}
	for _, c := range cases {
		got, ok := claudeEffort(c.level)
		if got != c.want || ok != c.wantOK {
			t.Errorf("claudeEffort(%q) = (%q, %v), want (%q, %v)", c.level, got, ok, c.want, c.wantOK)
		}
	}
}

func TestCodexReasoningEffort(t *testing.T) {
	cases := []struct {
		level  string
		want   string
		wantOK bool
	}{
		{"", "", false},
		{"low", "low", true},
		{"medium", "medium", true},
		{"high", "high", true},
		// codex has no xhigh/max tier — both clamp down to high rather than
		// passing an unrecognized value through to the CLI.
		{"xhigh", "high", true},
		{"max", "high", true},
		{"bogus", "", false},
	}
	for _, c := range cases {
		got, ok := codexReasoningEffort(c.level)
		if got != c.want || ok != c.wantOK {
			t.Errorf("codexReasoningEffort(%q) = (%q, %v), want (%q, %v)", c.level, got, ok, c.want, c.wantOK)
		}
	}
}
