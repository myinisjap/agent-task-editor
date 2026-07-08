package agent

import "testing"

// TestBuildGeminiArgs_Basic verifies the core non-interactive/headless flags
// are always present.
func TestBuildGeminiArgs_Basic(t *testing.T) {
	args := buildGeminiArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{},
	}, false)
	if got := findFlagValue(args, "--output-format"); got != "stream-json" {
		t.Fatalf("expected --output-format stream-json, got %q (args=%v)", got, args)
	}
	if !containsArg(args, "--yolo") {
		t.Fatalf("expected --yolo for unattended runs, args=%v", args)
	}
	if containsArg(args, "--skip-trust") {
		t.Fatalf("did not expect --skip-trust without MCP configured, args=%v", args)
	}
}

// TestBuildGeminiArgs_MCPConfigured verifies --skip-trust is added only when
// MCP is configured (it's required to unblock MCP servers in an untrusted
// workspace during headless runs).
func TestBuildGeminiArgs_MCPConfigured(t *testing.T) {
	args := buildGeminiArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{},
	}, true)
	if !containsArg(args, "--skip-trust") {
		t.Fatalf("expected --skip-trust when MCP is configured, args=%v", args)
	}
}

// TestBuildGeminiArgs_Model verifies a configured model is passed via --model.
func TestBuildGeminiArgs_Model(t *testing.T) {
	args := buildGeminiArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{Model: "gemini-2.5-pro"},
	}, false)
	if got := findFlagValue(args, "--model"); got != "gemini-2.5-pro" {
		t.Fatalf("expected --model gemini-2.5-pro, got %q (args=%v)", got, args)
	}
}

// TestBuildGeminiArgs_NoModel verifies no --model flag is added when unset.
func TestBuildGeminiArgs_NoModel(t *testing.T) {
	args := buildGeminiArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{},
	}, false)
	if containsArg(args, "--model") {
		t.Fatalf("did not expect --model flag when Model is unset, args=%v", args)
	}
}

// containsArg reports whether args contains the exact string v.
func containsArg(args []string, v string) bool {
	for _, a := range args {
		if a == v {
			return true
		}
	}
	return false
}

// TestClassifyGeminiJSON_Init verifies the init event's session_id is extracted.
func TestClassifyGeminiJSON_Init(t *testing.T) {
	line := `{"type":"init","timestamp":"2026-01-01T00:00:00Z","session_id":"abc-123","model":"auto"}`
	entry, outcome, usage, class, sid := classifyGeminiJSON(line)
	if sid != "abc-123" {
		t.Errorf("session_id = %q, want abc-123", sid)
	}
	if outcome != "" || usage != nil || class != ClassNone {
		t.Errorf("unexpected outcome/usage/class for init event: %q %v %q", outcome, usage, class)
	}
	if entry.Type != LogSystem {
		t.Errorf("entry.Type = %q, want system", entry.Type)
	}
}

// TestClassifyGeminiJSON_AssistantMessage verifies assistant message content
// is surfaced as stdout and scanned for an OUTCOME marker.
func TestClassifyGeminiJSON_AssistantMessage(t *testing.T) {
	line := `{"type":"message","role":"assistant","content":"Done. OUTCOME: success","delta":true}`
	entry, outcome, _, _, _ := classifyGeminiJSON(line)
	if entry.Type != LogStdout {
		t.Errorf("entry.Type = %q, want stdout", entry.Type)
	}
	if outcome != "success" {
		t.Errorf("outcome = %q, want success", outcome)
	}
}

// TestClassifyGeminiJSON_UserMessageIgnored verifies user-role messages don't
// leak an outcome (only assistant text should be scanned).
func TestClassifyGeminiJSON_UserMessageIgnored(t *testing.T) {
	line := `{"type":"message","role":"user","content":"OUTCOME: success"}`
	_, outcome, _, _, _ := classifyGeminiJSON(line)
	if outcome != "" {
		t.Errorf("outcome = %q, want empty for a user message", outcome)
	}
}

// TestClassifyGeminiJSON_Result verifies the terminal result event maps to
// success/failure outcomes, extracts stats as usage, and classifies errors.
func TestClassifyGeminiJSON_Result(t *testing.T) {
	success := `{"type":"result","timestamp":"t","status":"success","stats":{"input_tokens":10,"output_tokens":20}}`
	_, outcome, usage, class, _ := classifyGeminiJSON(success)
	if outcome != "success" {
		t.Errorf("outcome = %q, want success", outcome)
	}
	if usage == nil || usage.InputTokens != 10 || usage.OutputTokens != 20 {
		t.Errorf("usage = %+v, want input=10 output=20", usage)
	}
	if class != ClassNone {
		t.Errorf("class = %q, want none for a clean success", class)
	}

	failure := `{"type":"result","timestamp":"t","status":"error","error":{"type":"unknown","message":"API key not valid"}}`
	_, outcome, _, class, _ = classifyGeminiJSON(failure)
	if outcome != "failure" {
		t.Errorf("outcome = %q, want failure", outcome)
	}
	if class != ClassAuth {
		t.Errorf("class = %q, want auth for an invalid API key error", class)
	}
}

// TestClassifyGeminiJSON_ToolEvents verifies tool_use/tool_result map to the
// LogToolCall/LogToolResult log types.
func TestClassifyGeminiJSON_ToolEvents(t *testing.T) {
	toolUse := `{"type":"tool_use","tool_name":"run_shell_command","tool_id":"1","parameters":{}}`
	entry, _, _, _, _ := classifyGeminiJSON(toolUse)
	if entry.Type != LogToolCall {
		t.Errorf("tool_use entry.Type = %q, want tool_call", entry.Type)
	}

	toolResult := `{"type":"tool_result","tool_id":"1","status":"success","output":"ok"}`
	entry, _, _, class, _ := classifyGeminiJSON(toolResult)
	if entry.Type != LogToolResult {
		t.Errorf("tool_result entry.Type = %q, want tool_result", entry.Type)
	}
	if class != ClassNone {
		t.Errorf("successful tool_result class = %q, want none", class)
	}
}

// TestClassifyGeminiJSON_NonJSONLine verifies a non-JSON line degrades to a
// raw stdout entry rather than erroring.
func TestClassifyGeminiJSON_NonJSONLine(t *testing.T) {
	entry, outcome, usage, class, sid := classifyGeminiJSON("not json at all")
	if entry.Type != LogStdout || entry.Content != "not json at all" {
		t.Errorf("unexpected entry for non-JSON line: %+v", entry)
	}
	if outcome != "" || usage != nil || class != ClassNone || sid != "" {
		t.Errorf("expected all-zero extras for non-JSON line, got %q %v %q %q", outcome, usage, class, sid)
	}
}
