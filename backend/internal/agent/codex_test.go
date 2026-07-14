package agent

import (
	"slices"
	"testing"
)

// TestBuildCodexArgs_Basic verifies the core exec/non-interactive flags are
// always present, and the prompt is the final positional argument.
func TestBuildCodexArgs_Basic(t *testing.T) {
	args := buildCodexArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{},
	})
	if args[0] != "exec" {
		t.Fatalf("expected first arg to be the exec subcommand, got %v", args)
	}
	if !containsArg(args, "--json") {
		t.Fatalf("expected --json for structured output, args=%v", args)
	}
	if !containsArg(args, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("expected --dangerously-bypass-approvals-and-sandbox for unattended runs, args=%v", args)
	}
	if !containsArg(args, "--skip-git-repo-check") {
		t.Fatalf("expected --skip-git-repo-check, args=%v", args)
	}
	if args[len(args)-1] == "" {
		t.Fatalf("expected a non-empty trailing prompt argument, args=%v", args)
	}
}

// TestBuildCodexArgs_Model verifies a configured model is passed via --model.
func TestBuildCodexArgs_Model(t *testing.T) {
	args := buildCodexArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{Model: "gpt-5-codex"},
	})
	if got := findFlagValue(args, "--model"); got != "gpt-5-codex" {
		t.Fatalf("expected --model gpt-5-codex, got %q (args=%v)", got, args)
	}
}

// TestBuildCodexArgs_NoModel verifies no --model flag is added when unset.
func TestBuildCodexArgs_NoModel(t *testing.T) {
	args := buildCodexArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{},
	})
	if containsArg(args, "--model") {
		t.Fatalf("did not expect --model flag when Model is unset, args=%v", args)
	}
}

// TestBuildCodexArgs_Resume verifies the `resume <id>` subcommand is inserted
// after the exec flags and before the trailing prompt (codex's resume is a
// subcommand, not an appendable flag like other providers).
func TestBuildCodexArgs_Resume(t *testing.T) {
	args := buildCodexArgs(RunInput{
		Task:            Task{Title: "t"},
		AgentConfig:     AgentConfig{},
		ResumeSessionID: "sess-123",
	})
	ri := slices.Index(args,"resume")
	if ri < 0 {
		t.Fatalf("expected a resume subcommand, args=%v", args)
	}
	if args[ri+1] != "sess-123" {
		t.Fatalf("expected session id right after resume, args=%v", args)
	}
	if ji := slices.Index(args,"--json"); ji < 0 || ji > ri {
		t.Fatalf("expected --json flag before resume subcommand, args=%v", args)
	}
	if args[len(args)-1] == "resume" || args[len(args)-1] == "sess-123" {
		t.Fatalf("expected prompt as final arg, not the resume id, args=%v", args)
	}
}

// TestBuildCodexArgs_NoResume verifies no resume subcommand when unset.
func TestBuildCodexArgs_NoResume(t *testing.T) {
	args := buildCodexArgs(RunInput{Task: Task{Title: "t"}, AgentConfig: AgentConfig{}})
	if containsArg(args, "resume") {
		t.Fatalf("did not expect a resume subcommand when ResumeSessionID is empty, args=%v", args)
	}
}

// TestRenderCodexMCPTOML verifies the generated TOML matches the shape
// `codex mcp add` writes to config.toml (verified against a live invocation).
func TestRenderCodexMCPTOML(t *testing.T) {
	entry := mcpServerEntry{
		Command: "/opt/mcp-server",
		Args:    []string{},
		Env:     map[string]string{"RUN_ID": "abc"},
	}
	toml := renderCodexMCPTOML("task-editor", entry)
	if !contains(toml, `[mcp_servers.task-editor]`) {
		t.Errorf("missing server section header, got:\n%s", toml)
	}
	if !contains(toml, `command = "/opt/mcp-server"`) {
		t.Errorf("missing command line, got:\n%s", toml)
	}
	if !contains(toml, `[mcp_servers.task-editor.env]`) {
		t.Errorf("missing env section header, got:\n%s", toml)
	}
	if !contains(toml, `RUN_ID = "abc"`) {
		t.Errorf("missing env entry, got:\n%s", toml)
	}
}

// TestRenderCodexMCPTOML_NoEnv verifies no env section is emitted when there
// are no env vars to set.
func TestRenderCodexMCPTOML_NoEnv(t *testing.T) {
	entry := mcpServerEntry{Command: "/opt/mcp-server", Args: []string{}}
	toml := renderCodexMCPTOML("task-editor", entry)
	if contains(toml, ".env]") {
		t.Errorf("did not expect an env section with no env vars, got:\n%s", toml)
	}
}

// TestClassifyCodexJSON_ThreadStarted verifies the thread_id is surfaced as
// the session id.
func TestClassifyCodexJSON_ThreadStarted(t *testing.T) {
	line := `{"type":"thread.started","thread_id":"019f3f4b-e798-7812-8d18-cfd4ab5ade09"}`
	entry, _, _, class, sid := classifyCodexJSON(line)
	if sid != "019f3f4b-e798-7812-8d18-cfd4ab5ade09" {
		t.Errorf("session id = %q, want the thread_id", sid)
	}
	if class != ClassNone {
		t.Errorf("class = %q, want none", class)
	}
	if entry.Type != LogSystem {
		t.Errorf("entry.Type = %q, want system", entry.Type)
	}
}

// TestClassifyCodexJSON_TurnCompleted verifies token usage is extracted.
func TestClassifyCodexJSON_TurnCompleted(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":15,"cached_input_tokens":0,"output_tokens":25,"reasoning_output_tokens":5}}`
	_, outcome, usage, class, _ := classifyCodexJSON(line)
	if outcome != "" {
		t.Errorf("outcome = %q, want empty (turn.completed isn't terminal)", outcome)
	}
	if usage == nil || usage.InputTokens != 15 || usage.OutputTokens != 25 {
		t.Errorf("usage = %+v, want input=15 output=25", usage)
	}
	if class != ClassNone {
		t.Errorf("class = %q, want none", class)
	}
}

// TestClassifyCodexJSON_TurnFailed verifies a turn.failed event yields a
// failure outcome and a classification derived from the error message.
func TestClassifyCodexJSON_TurnFailed(t *testing.T) {
	line := `{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header"}}`
	_, outcome, _, class, _ := classifyCodexJSON(line)
	if outcome != "failure" {
		t.Errorf("outcome = %q, want failure", outcome)
	}
	if class != ClassAuth {
		t.Errorf("class = %q, want auth", class)
	}
}

// TestClassifyCodexJSON_TurnFailedGenuine verifies a turn.failed event whose
// message carries no recognizable infra/auth signal classifies as genuine.
func TestClassifyCodexJSON_TurnFailedGenuine(t *testing.T) {
	line := `{"type":"turn.failed","error":{"message":"the model declined to continue"}}`
	_, _, _, class, _ := classifyCodexJSON(line)
	if class != ClassGenuine {
		t.Errorf("class = %q, want genuine", class)
	}
}

// TestClassifyCodexJSON_AgentMessageCompleted verifies a completed
// agent_message item is scanned for an OUTCOME marker.
func TestClassifyCodexJSON_AgentMessageCompleted(t *testing.T) {
	line := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"All done. OUTCOME: success"}}`
	entry, outcome, _, _, _ := classifyCodexJSON(line)
	if outcome != "success" {
		t.Errorf("outcome = %q, want success", outcome)
	}
	if entry.Type != LogStdout {
		t.Errorf("entry.Type = %q, want stdout", entry.Type)
	}
}

// TestClassifyCodexJSON_AgentMessageStartedNoOutcome verifies an in-progress
// (item.started) agent_message is NOT scanned for an outcome (only the
// terminal item.completed event should resolve one).
func TestClassifyCodexJSON_AgentMessageStartedNoOutcome(t *testing.T) {
	line := `{"type":"item.started","item":{"id":"item_0","type":"agent_message","text":"OUTCOME: success"}}`
	_, outcome, _, _, _ := classifyCodexJSON(line)
	if outcome != "" {
		t.Errorf("outcome = %q, want empty for an in-progress item", outcome)
	}
}

// TestClassifyCodexJSON_CommandExecution verifies command_execution items map
// to LogToolCall (in-progress) / LogToolResult (completed).
func TestClassifyCodexJSON_CommandExecution(t *testing.T) {
	started := `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"ls","status":"in_progress"}}`
	entry, _, _, _, _ := classifyCodexJSON(started)
	if entry.Type != LogToolCall {
		t.Errorf("entry.Type = %q, want tool_call", entry.Type)
	}

	completed := `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"ls","aggregated_output":"a.txt","exit_code":0,"status":"completed"}}`
	entry, _, _, class, _ := classifyCodexJSON(completed)
	if entry.Type != LogToolResult {
		t.Errorf("entry.Type = %q, want tool_result", entry.Type)
	}
	if class != ClassNone {
		t.Errorf("class = %q, want none for a successful command", class)
	}

	failed := `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"curl x","aggregated_output":"connection reset by peer","exit_code":1,"status":"failed"}}`
	_, _, _, class, _ = classifyCodexJSON(failed)
	if class != ClassTransient {
		t.Errorf("class = %q, want transient for a connection-reset failure", class)
	}
}

// TestClassifyCodexJSON_McpToolCall verifies mcp_tool_call items map to
// LogToolCall/LogToolResult and classify failures from the error message.
func TestClassifyCodexJSON_McpToolCall(t *testing.T) {
	completed := `{"type":"item.completed","item":{"id":"item_2","type":"mcp_tool_call","server":"task-editor","tool":"signal_complete","status":"failed","error":{"message":"429 rate limit"}}}`
	entry, _, _, class, _ := classifyCodexJSON(completed)
	if entry.Type != LogToolResult {
		t.Errorf("entry.Type = %q, want tool_result", entry.Type)
	}
	if class != ClassRateLimit {
		t.Errorf("class = %q, want rate_limit", class)
	}
}

// TestClassifyCodexJSON_NonJSONLine verifies interleaved plain-text log lines
// (Codex mixes Rust tracing ERROR lines into stdout) degrade to a raw stdout
// entry rather than erroring.
func TestClassifyCodexJSON_NonJSONLine(t *testing.T) {
	line := `2026-07-08T01:16:07.304228Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket`
	entry, outcome, usage, class, sid := classifyCodexJSON(line)
	if entry.Type != LogStdout || entry.Content != line {
		t.Errorf("unexpected entry for non-JSON line: %+v", entry)
	}
	if outcome != "" || usage != nil || class != ClassNone || sid != "" {
		t.Errorf("expected all-zero extras for non-JSON line, got %q %v %q %q", outcome, usage, class, sid)
	}
}
