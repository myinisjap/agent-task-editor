package providers

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestClassifyOpencodeJSON_SessionIDFromErrorEvent verifies that the
// top-level "sessionID" field is extracted from an "error" event, matching
// the shape observed live against opencode-ai v1.18.6 (see issue #281/#283):
//
//	{"type":"error","timestamp":...,"sessionID":"ses_...","error":{...}}
//
// "error" isn't one of the explicitly-handled types in classifyOpencodeJSON,
// so it falls through to the default branch; the session id must still be
// extracted there since the CLI stamps it on every event regardless of type.
func TestClassifyOpencodeJSON_SessionIDFromErrorEvent(t *testing.T) {
	line := `{"type":"error","timestamp":1234567890,"sessionID":"ses_05b608ca5ffeHrFbuP1WD9i4zS","error":{"name":"UnknownError"}}`

	entry, outcome, usage, sid, _ := classifyOpencodeJSON(line)

	if usage != nil {
		t.Errorf("want nil usage for error event, got %+v", usage)
	}
	if sid != "ses_05b608ca5ffeHrFbuP1WD9i4zS" {
		t.Errorf("want sessionID extracted, got %q", sid)
	}
	if outcome != "" {
		t.Errorf("want empty outcome for error event, got %q", outcome)
	}
	if entry.Type != agent.LogStdout {
		t.Errorf("want LogStdout entry for unhandled type, got %v", entry.Type)
	}
}

// TestClassifyOpencodeJSON_TextEventCarriesSessionAndOutcome verifies a
// "text" event both extracts an OUTCOME marker (existing behavior) and the
// sessionID stamped on the same event.
func TestClassifyOpencodeJSON_TextEventCarriesSessionAndOutcome(t *testing.T) {
	line := `{"type":"text","sessionID":"ses_abc123","part":{"type":"text","text":"OUTCOME: success"}}`

	entry, outcome, usage, sid, _ := classifyOpencodeJSON(line)

	if outcome != "success" {
		t.Errorf("want outcome=success, got %q", outcome)
	}
	if usage != nil {
		t.Errorf("want nil usage for text event, got %+v", usage)
	}
	if sid != "ses_abc123" {
		t.Errorf("want sessionID=ses_abc123, got %q", sid)
	}
	if entry.Type != agent.LogStdout {
		t.Errorf("want LogStdout entry, got %v", entry.Type)
	}
	if entry.Content != "OUTCOME: success" {
		t.Errorf("want entry content to be part.text, got %q", entry.Content)
	}
}

// TestClassifyOpencodeJSON_StepFinishCarriesSessionID verifies session id
// extraction on a step_finish event, which drives an important system log
// line but not the outcome. This event carries neither cost nor tokens, so
// usage must stay nil (see extractOpencodeUsage's guard).
func TestClassifyOpencodeJSON_StepFinishCarriesSessionID(t *testing.T) {
	line := `{"type":"step_finish","sessionID":"ses_xyz789","part":{"reason":"stop"}}`

	entry, outcome, usage, sid, _ := classifyOpencodeJSON(line)

	if sid != "ses_xyz789" {
		t.Errorf("want sessionID=ses_xyz789, got %q", sid)
	}
	if outcome != "" {
		t.Errorf("want empty outcome for step_finish, got %q", outcome)
	}
	if usage != nil {
		t.Errorf("want nil usage when neither cost nor tokens present, got %+v", usage)
	}
	if entry.Type != agent.LogSystem {
		t.Errorf("want LogSystem entry, got %v", entry.Type)
	}
}

// TestClassifyOpencodeJSON_StepFinishCarriesUsage verifies that a
// step_finish event carrying "cost" and "tokens" (as opencode-ai v1.18.6
// emits) is parsed into a non-nil *runUsage with the expected fields.
func TestClassifyOpencodeJSON_StepFinishCarriesUsage(t *testing.T) {
	line := `{"type":"step_finish","sessionID":"ses_xyz789","part":{"reason":"stop","cost":0.042,"tokens":{"input":123,"output":456,"reasoning":0,"cache":{"read":0,"write":0}}}}`

	_, _, usage, _, _ := classifyOpencodeJSON(line)

	if usage == nil {
		t.Fatalf("want non-nil usage, got nil")
	}
	if usage.InputTokens != 123 {
		t.Errorf("InputTokens = %d, want 123", usage.InputTokens)
	}
	if usage.OutputTokens != 456 {
		t.Errorf("OutputTokens = %d, want 456", usage.OutputTokens)
	}
	if usage.CostUSD != 0.042 {
		t.Errorf("CostUSD = %v, want 0.042", usage.CostUSD)
	}
}

// TestClassifyOpencodeJSON_StepFinishNoUsageFields verifies the nil-guard:
// a step_finish part lacking both "cost" and "tokens" must return nil usage
// rather than a spurious zero-valued *runUsage, matching
// extractResultUsage's equivalent guard in parse_streamjson.go.
func TestClassifyOpencodeJSON_StepFinishNoUsageFields(t *testing.T) {
	line := `{"type":"step_finish","sessionID":"ses_xyz789","part":{"reason":"tool_calls"}}`

	_, _, usage, _, _ := classifyOpencodeJSON(line)

	if usage != nil {
		t.Errorf("want nil usage when neither cost nor tokens present, got %+v", usage)
	}
}

// TestClassifyOpencodeJSON_NoSessionIDField verifies that a line lacking a
// sessionID field (or malformed JSON) degrades gracefully to an empty id
// rather than panicking or erroring.
func TestClassifyOpencodeJSON_NoSessionIDField(t *testing.T) {
	entry, outcome, usage, sid, parsedJSON := classifyOpencodeJSON(`{"type":"tool_use"}`)
	if sid != "" {
		t.Errorf("want empty sessionID, got %q", sid)
	}
	if outcome != "" {
		t.Errorf("want empty outcome, got %q", outcome)
	}
	if usage != nil {
		t.Errorf("want nil usage, got %+v", usage)
	}
	if entry.Type != agent.LogToolCall {
		t.Errorf("want LogToolCall entry, got %v", entry.Type)
	}
	if !parsedJSON {
		t.Errorf("want parsedJSON=true for valid JSON, got false")
	}

	entry2, _, usage2, sid2, parsedJSON2 := classifyOpencodeJSON("not json")
	if sid2 != "" {
		t.Errorf("want empty sessionID for malformed json, got %q", sid2)
	}
	if usage2 != nil {
		t.Errorf("want nil usage for malformed json, got %+v", usage2)
	}
	if entry2.Type != agent.LogStdout {
		t.Errorf("want LogStdout fallback entry, got %v", entry2.Type)
	}
	if parsedJSON2 {
		t.Errorf("want parsedJSON=false for malformed json, got true")
	}
}
