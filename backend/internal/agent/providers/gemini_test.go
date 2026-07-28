package providers

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestBuildGeminiArgs_Basic verifies the core non-interactive/headless flags
// are always present.
// TestBuildGeminiArgs_Resume verifies ResumeSessionID is passed via --resume.
func TestBuildGeminiArgs_Resume(t *testing.T) {
	args := buildGeminiArgs(agent.RunInput{Task: agent.Task{Title: "t"}, AgentConfig: agent.AgentConfig{}, ResumeSessionID: "g1"}, false)
	if got := findFlagValue(args, "--resume"); got != "g1" {
		t.Fatalf("expected --resume g1, got %q (args=%v)", got, args)
	}
	none := buildGeminiArgs(agent.RunInput{Task: agent.Task{Title: "t"}, AgentConfig: agent.AgentConfig{}}, false)
	if containsArg(none, "--resume") {
		t.Fatalf("did not expect --resume when ResumeSessionID empty, args=%v", none)
	}
}

func TestBuildGeminiArgs_Basic(t *testing.T) {
	args := buildGeminiArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
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
	args := buildGeminiArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}, true)
	if !containsArg(args, "--skip-trust") {
		t.Fatalf("expected --skip-trust when MCP is configured, args=%v", args)
	}
}

// TestBuildGeminiArgs_Model verifies a configured model is passed via --model.
func TestBuildGeminiArgs_Model(t *testing.T) {
	args := buildGeminiArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{Model: "gemini-2.5-pro"},
	}, false)
	if got := findFlagValue(args, "--model"); got != "gemini-2.5-pro" {
		t.Fatalf("expected --model gemini-2.5-pro, got %q (args=%v)", got, args)
	}
}

// TestBuildGeminiArgs_NoModel verifies no --model flag is added when unset.
func TestBuildGeminiArgs_NoModel(t *testing.T) {
	args := buildGeminiArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
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
	if outcome != "" || usage != nil || class != agent.ClassNone {
		t.Errorf("unexpected outcome/usage/class for init event: %q %v %q", outcome, usage, class)
	}
	if entry.Type != agent.LogSystem {
		t.Errorf("entry.Type = %q, want system", entry.Type)
	}
}

// TestClassifyGeminiJSON_AssistantMessage verifies assistant message content
// is surfaced as stdout and scanned for an OUTCOME marker.
func TestClassifyGeminiJSON_AssistantMessage(t *testing.T) {
	line := `{"type":"message","role":"assistant","content":"Done. OUTCOME: success","delta":true}`
	entry, outcome, _, _, _ := classifyGeminiJSON(line)
	if entry.Type != agent.LogStdout {
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
	if class != agent.ClassNone {
		t.Errorf("class = %q, want none for a clean success", class)
	}

	failure := `{"type":"result","timestamp":"t","status":"error","error":{"type":"unknown","message":"API key not valid"}}`
	_, outcome, _, class, _ = classifyGeminiJSON(failure)
	if outcome != "failure" {
		t.Errorf("outcome = %q, want failure", outcome)
	}
	if class != agent.ClassAuth {
		t.Errorf("class = %q, want auth for an invalid API key error", class)
	}
}

// TestClassifyGeminiJSON_ToolEvents verifies tool_use/tool_result map to the
// agent.LogToolCall/agent.LogToolResult log types.
func TestClassifyGeminiJSON_ToolEvents(t *testing.T) {
	toolUse := `{"type":"tool_use","tool_name":"run_shell_command","tool_id":"1","parameters":{}}`
	entry, _, _, _, _ := classifyGeminiJSON(toolUse)
	if entry.Type != agent.LogToolCall {
		t.Errorf("tool_use entry.Type = %q, want tool_call", entry.Type)
	}

	toolResult := `{"type":"tool_result","tool_id":"1","status":"success","output":"ok"}`
	entry, _, _, class, _ := classifyGeminiJSON(toolResult)
	if entry.Type != agent.LogToolResult {
		t.Errorf("tool_result entry.Type = %q, want tool_result", entry.Type)
	}
	if class != agent.ClassNone {
		t.Errorf("successful tool_result class = %q, want none", class)
	}
}

// TestClassifyGeminiJSON_NonJSONLine verifies a non-JSON line degrades to a
// raw stdout entry rather than erroring.
func TestClassifyGeminiJSON_NonJSONLine(t *testing.T) {
	entry, outcome, usage, class, sid := classifyGeminiJSON("not json at all")
	if entry.Type != agent.LogStdout || entry.Content != "not json at all" {
		t.Errorf("unexpected entry for non-JSON line: %+v", entry)
	}
	if outcome != "" || usage != nil || class != agent.ClassNone || sid != "" {
		t.Errorf("expected all-zero extras for non-JSON line, got %q %v %q %q", outcome, usage, class, sid)
	}
}

// --- Subprocess lifecycle tests (generalized from claude_test.go's
// CLAUDE_TEST_HELPER re-exec harness — see TestMain in claude_test.go) ---

func geminiHelperInput(mode string) agent.RunInput {
	return agent.RunInput{
		RunID:       "gemini-test-run",
		Task:        agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{Env: map[string]string{"GEMINI_TEST_HELPER": mode}, TimeoutSecs: 10},
		RepoPath:    os.TempDir(),
	}
}

func TestGeminiRunner_Binary_DefaultsAndOverrides(t *testing.T) {
	var r GeminiRunner
	if got := r.binary(); got != "gemini" {
		t.Errorf("binary() = %q, want default %q", got, "gemini")
	}
	r.BinaryPath = "/opt/gemini"
	if got := r.binary(); got != "/opt/gemini" {
		t.Errorf("binary() = %q, want override %q", got, "/opt/gemini")
	}
}

func TestGeminiRunner_Run_Exit0Success(t *testing.T) {
	runner := &GeminiRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), geminiHelperInput("exit0_success"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", result.Outcome)
	}
	if result.SessionID != "gem-1" {
		t.Errorf("SessionID = %q, want gem-1", result.SessionID)
	}
	if result.InputTokens != 10 || result.OutputTokens != 20 {
		t.Errorf("usage = %d/%d, want 10/20", result.InputTokens, result.OutputTokens)
	}
}

// TestGeminiRunner_Run_Exit1WithErrorResult verifies a non-zero exit whose
// stream carries a typed "result" event with status=error surfaces
// outcome=failure. Like codex (see TestCodexRunner_Run_Exit1WithTurnFailed),
// this is itself a resolved outcome, not just discarded stdout noise, so
// Status stays "completed" while Outcome reports the failure — matching
// gemini.go's actual `err != nil && outcome == ""` gate.
func TestGeminiRunner_Run_Exit1WithErrorResult(t *testing.T) {
	runner := &GeminiRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), geminiHelperInput("exit1"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Outcome != "failure" {
		t.Errorf("Outcome = %q, want failure", result.Outcome)
	}
}

// TestGeminiRunner_Run_Exit1NoOutcomeIsFailed verifies a non-zero exit with
// no parsed outcome at all (process crashed before any terminal event) is
// reported as Status=failed.
func TestGeminiRunner_Run_Exit1NoOutcomeIsFailed(t *testing.T) {
	runner := &GeminiRunner{BinaryPath: "false"}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), geminiHelperInput(""), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestGeminiRunner_Run_TimeoutKillsProcess verifies the context-cancel/timeout
// branch (shared by every CLI provider) actually kills a hung process and
// returns *agent.ErrTransient instead of hanging.
func TestGeminiRunner_Run_TimeoutKillsProcess(t *testing.T) {
	runner := &GeminiRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := geminiHelperInput("")
	input.AgentConfig.Env["HANG_TEST_HELPER"] = "30"
	input.AgentConfig.TimeoutSecs = 1

	start := time.Now()
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("Run took %s, want the 1s timeout to kill the process quickly", elapsed)
	}
	var te *agent.ErrTransient
	if !errors.As(err, &te) {
		t.Fatalf("want *agent.ErrTransient, got %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}
