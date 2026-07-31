package providers

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestExtractOutcome is a table test for extractOutcome (parse.go), which
// scans free-text agent output for an "OUTCOME: success|failure" marker.
// Previously untested directly (only indirectly exercised via each
// provider's classify*JSON tests) — see #79, where the exact matching rules
// here (case-insensitivity of the value, first-token-only, embedded-in-text)
// matter for whether a run's outcome is ever correctly resolved.
func TestExtractOutcome(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"success", "Some preamble.\nOUTCOME: success", "success"},
		{"failure", "OUTCOME: failure", "failure"},
		{"no marker at all", "I finished the task successfully.", ""},
		{"empty string", "", ""},
		{"marker with no value", "OUTCOME:", ""},
		{"marker with trailing whitespace only", "OUTCOME:    ", ""},
		{"mixed case value is normalized", "OUTCOME: SUCCESS", "success"},
		{"mixed case value failure", "OUTCOME: Failure", "failure"},
		{"unrecognized value yields empty", "OUTCOME: maybe", ""},
		{"embedded in a larger sentence, marker still found", "Wrapping up now. OUTCOME: success\nThanks!", "success"},
		{"first token only — extra trailing words ignored, still matches", "OUTCOME: success and done", "success"},
		{"multiple markers — the first one wins", "OUTCOME: failure\nOUTCOME: success", "failure"},
		{"marker prefix without colon does not match", "OUTCOME success", ""},
		{"tab/newline whitespace between marker and value", "OUTCOME:\n\tsuccess", "success"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractOutcome(tc.text)
			if got != tc.want {
				t.Errorf("extractOutcome(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestApplyUsage verifies applyUsage copies fields when non-nil and is a
// no-op (leaves res untouched) when u is nil.
func TestApplyUsage(t *testing.T) {
	t.Run("nil usage leaves result untouched", func(t *testing.T) {
		res := &agent.Result{Status: "completed", InputTokens: 999}
		applyUsage(res, nil)
		if res.InputTokens != 999 {
			t.Errorf("applyUsage(nil) mutated res: %+v", res)
		}
	})

	t.Run("non-nil usage copies input/output tokens and cost", func(t *testing.T) {
		res := &agent.Result{Status: "completed"}
		applyUsage(res, &runUsage{InputTokens: 12, OutputTokens: 34, CostUSD: 0.5})
		if res.InputTokens != 12 || res.OutputTokens != 34 || res.CostUSD != 0.5 {
			t.Errorf("applyUsage did not copy fields: %+v", res)
		}
	})
}

// TestIs429Line / TestIsTransientLine verify the thin agent.ClassifyLine
// wrappers used by every CLI provider's stdout/stderr scan loop.
func TestIs429Line(t *testing.T) {
	if !is429Line("Error: 429 Too Many Requests") {
		t.Error("want true for a 429 line")
	}
	if is429Line("Error: 500 Internal Server Error") {
		t.Error("want false for a non-429 line")
	}
}

func TestIsTransientLine(t *testing.T) {
	if !isTransientLine("connection reset by peer") {
		t.Error("want true for a transient infra line")
	}
	if isTransientLine("the model declined to continue") {
		t.Error("want false for a genuine-failure line")
	}
}
