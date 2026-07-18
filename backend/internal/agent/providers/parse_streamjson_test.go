package providers

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestClassifyStreamJSON_ResultUsage verifies that a "result" message
// containing both a usage object and total_cost_usd is parsed into a
// runUsage with the expected fields, and that the outcome is still parsed
// correctly from the accompanying OUTCOME marker.
func TestClassifyStreamJSON_ResultUsage(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"OUTCOME: success","usage":{"input_tokens":123,"output_tokens":456,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"total_cost_usd":0.0789,"duration_ms":1500}`

	ev := classifyStreamJSON(line)

	if ev.Entry.Type != agent.LogSystem {
		t.Errorf("want LogSystem entry, got %v", ev.Entry.Type)
	}
	if ev.Outcome != "success" {
		t.Errorf("want outcome=success, got %q", ev.Outcome)
	}
	if ev.Usage == nil {
		t.Fatalf("want non-nil usage, got nil")
	}
	if ev.Usage.InputTokens != 123 {
		t.Errorf("want InputTokens=123, got %d", ev.Usage.InputTokens)
	}
	if ev.Usage.OutputTokens != 456 {
		t.Errorf("want OutputTokens=456, got %d", ev.Usage.OutputTokens)
	}
	if ev.Usage.CostUSD != 0.0789 {
		t.Errorf("want CostUSD=0.0789, got %v", ev.Usage.CostUSD)
	}
}

// TestClassifyStreamJSON_ResultNoUsage verifies a "result" message with no
// usage/total_cost_usd fields (e.g. an older CLI version) returns a nil
// usage rather than a zero-valued struct, so callers don't overwrite a
// previously-seen usage with zeros.
func TestClassifyStreamJSON_ResultNoUsage(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"OUTCOME: success"}`

	ev := classifyStreamJSON(line)

	if ev.Outcome != "success" {
		t.Errorf("want outcome=success, got %q", ev.Outcome)
	}
	if ev.Usage != nil {
		t.Errorf("want nil usage, got %+v", ev.Usage)
	}
}

// TestClassifyStreamJSON_NonResultMessagesReturnNilUsage verifies that
// non-"result" message types never populate usage.
func TestClassifyStreamJSON_NonResultMessagesReturnNilUsage(t *testing.T) {
	for _, line := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"tool_use"}`,
		`{"type":"tool_result"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result"}]}}`,
	} {
		ev := classifyStreamJSON(line)
		if ev.Usage != nil {
			t.Errorf("line %q: want nil usage, got %+v", line, ev.Usage)
		}
	}
}

// TestClassifyStreamJSON_SessionID verifies the session id is extracted from
// any stream-json envelope carrying one, and is empty otherwise.
func TestClassifyStreamJSON_SessionID(t *testing.T) {
	cases := map[string]string{
		`{"type":"system","subtype":"init","session_id":"abc-123"}`:        "abc-123",
		`{"type":"result","subtype":"success","session_id":"abc-123"}`:     "abc-123",
		`{"type":"assistant","message":{"content":[]},"session_id":"xyz"}`: "xyz",
		`{"type":"assistant","message":{"content":[]}}`:                    "",
		`not json at all`: "",
	}
	for line, want := range cases {
		ev := classifyStreamJSON(line)
		if ev.SessionID != want {
			t.Errorf("line %q: want session %q, got %q", line, want, ev.SessionID)
		}
	}
}

// TestIs429AndTransientWrappers verifies the thin bool wrappers the CLI
// providers use stay consistent with agent.ClassifyLine.
func TestIs429AndTransientWrappers(t *testing.T) {
	if !is429Line("HTTP 429 rate limit") {
		t.Error("is429Line should be true for a 429 line")
	}
	if is429Line("connection reset by peer") {
		t.Error("is429Line should be false for a transient line")
	}
	if !isTransientLine("connection reset by peer") {
		t.Error("isTransientLine should be true for a transient line")
	}
	if isTransientLine("HTTP 429 rate limit") {
		t.Error("isTransientLine should be false for a rate-limit line")
	}
	// An auth line is neither a 429 nor a transient signal at the provider
	// level — the pool escalates it separately via hasLoginError.
	if is429Line("Not logged in") || isTransientLine("Not logged in") {
		t.Error("auth line must not be classified as 429 or transient")
	}
}

// TestClassifyResultMessage verifies the structured stream-json "result"
// classification preferred by the claude/qwen providers.
func TestClassifyResultMessage(t *testing.T) {
	cases := []struct {
		name string
		line string
		want agent.Classification
	}{
		{
			name: "clean success",
			line: `{"type":"result","subtype":"success","result":"OUTCOME: success"}`,
			want: agent.ClassNone,
		},
		{
			name: "error_max_turns is genuine (no infra signal)",
			line: `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"reached max turns"}`,
			want: agent.ClassNone,
		},
		{
			name: "error with rate-limit text",
			line: `{"type":"result","subtype":"error","is_error":true,"result":"API error: 429 rate limit"}`,
			want: agent.ClassRateLimit,
		},
		{
			name: "error with transient text",
			line: `{"type":"result","subtype":"error","is_error":true,"result":"connection reset by peer"}`,
			want: agent.ClassTransient,
		},
		{
			name: "is_error flag without error subtype",
			line: `{"type":"result","subtype":"","is_error":true,"result":"upstream 503 service unavailable"}`,
			want: agent.ClassTransient,
		},
		{
			// The exact sample from the task: Claude's session-limit message
			// contains no "429"/"rate limit" substring in the text itself —
			// only the structured api_error_status field carries the 429.
			// classifyResultMessage must treat that field as authoritative.
			name: "session limit result with api_error_status 429",
			line: `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"You've hit your session limit · resets 6pm (America/Chicago)"}`,
			want: agent.ClassRateLimit,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyStreamJSON(tc.line).Class
			if got != tc.want {
				t.Errorf("classifyStreamJSON(%q) classification = %q, want %q", tc.line, got, tc.want)
			}
		})
	}
}

// TestClassifyStreamJSON_NonResultNoClassification verifies non-result message
// types never carry a failure classification (only the typed terminal result
// event does).
func TestClassifyStreamJSON_NonResultNoClassification(t *testing.T) {
	for _, line := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"connection reset"}]}}`,
		`{"type":"tool_use"}`,
		`{"type":"tool_result"}`,
		`not json at all`,
	} {
		if class := classifyStreamJSON(line).Class; class != agent.ClassNone {
			t.Errorf("line %q: want ClassNone, got %q", line, class)
		}
	}
}
