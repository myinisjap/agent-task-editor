package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// containsArg reports whether v is present in args.
func containsArg(args []string, v string) bool {
	return slices.Contains(args, v)
}

// TestBuildCodexArgs_Basic verifies the core exec/non-interactive flags are
// always present, and the prompt is the final positional argument.
func TestBuildCodexArgs_Basic(t *testing.T) {
	args := buildCodexArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
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
	args := buildCodexArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{Model: "gpt-5-codex"},
	})
	if got := findFlagValue(args, "--model"); got != "gpt-5-codex" {
		t.Fatalf("expected --model gpt-5-codex, got %q (args=%v)", got, args)
	}
}

// TestBuildCodexArgs_NoModel verifies no --model flag is added when unset.
func TestBuildCodexArgs_NoModel(t *testing.T) {
	args := buildCodexArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	})
	if containsArg(args, "--model") {
		t.Fatalf("did not expect --model flag when Model is unset, args=%v", args)
	}
}

// TestBuildCodexArgs_Effort verifies AgentConfig.Effort is translated into a
// `-c model_reasoning_effort="<level>"` override, with xhigh/max clamped
// down to high (codex only accepts minimal|low|medium|high), and omitted
// entirely when unset.
func TestBuildCodexArgs_Effort(t *testing.T) {
	t.Run("unset omits the override", func(t *testing.T) {
		args := buildCodexArgs(agent.RunInput{
			Task:        agent.Task{Title: "t"},
			AgentConfig: agent.AgentConfig{},
		})
		if containsArg(args, "-c") {
			t.Fatalf("did not expect a -c model_reasoning_effort override when Effort is unset, args=%v", args)
		}
	})

	cases := []struct {
		level string
		want  string
	}{
		{"low", `model_reasoning_effort="low"`},
		{"medium", `model_reasoning_effort="medium"`},
		{"high", `model_reasoning_effort="high"`},
		{"xhigh", `model_reasoning_effort="high"`},
		{"max", `model_reasoning_effort="high"`},
	}
	for _, c := range cases {
		t.Run(c.level, func(t *testing.T) {
			args := buildCodexArgs(agent.RunInput{
				Task:        agent.Task{Title: "t"},
				AgentConfig: agent.AgentConfig{Effort: c.level},
			})
			if got := findFlagValue(args, "-c"); got != c.want {
				t.Fatalf("expected -c %s, got %q (args=%v)", c.want, got, args)
			}
		})
	}
}

// TestBuildCodexArgs_Resume verifies the `resume <id>` subcommand is inserted
// after the exec flags and before the trailing prompt (codex's resume is a
// subcommand, not an appendable flag like other providers).
func TestBuildCodexArgs_Resume(t *testing.T) {
	args := buildCodexArgs(agent.RunInput{
		Task:            agent.Task{Title: "t"},
		AgentConfig:     agent.AgentConfig{},
		ResumeSessionID: "sess-123",
	})
	ri := slices.Index(args, "resume")
	if ri < 0 {
		t.Fatalf("expected a resume subcommand, args=%v", args)
	}
	if args[ri+1] != "sess-123" {
		t.Fatalf("expected session id right after resume, args=%v", args)
	}
	if ji := slices.Index(args, "--json"); ji < 0 || ji > ri {
		t.Fatalf("expected --json flag before resume subcommand, args=%v", args)
	}
	if args[len(args)-1] == "resume" || args[len(args)-1] == "sess-123" {
		t.Fatalf("expected prompt as final arg, not the resume id, args=%v", args)
	}
}

// TestBuildCodexArgs_NoResume verifies no resume subcommand when unset.
func TestBuildCodexArgs_NoResume(t *testing.T) {
	args := buildCodexArgs(agent.RunInput{Task: agent.Task{Title: "t"}, AgentConfig: agent.AgentConfig{}})
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
	entry, _, _, class, sid, _ := classifyCodexJSON(line)
	if sid != "019f3f4b-e798-7812-8d18-cfd4ab5ade09" {
		t.Errorf("session id = %q, want the thread_id", sid)
	}
	if class != agent.ClassNone {
		t.Errorf("class = %q, want none", class)
	}
	if entry.Type != agent.LogSystem {
		t.Errorf("entry.Type = %q, want system", entry.Type)
	}
}

// TestClassifyCodexJSON_TurnCompleted verifies token usage is extracted.
func TestClassifyCodexJSON_TurnCompleted(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":15,"cached_input_tokens":0,"output_tokens":25,"reasoning_output_tokens":5}}`
	_, outcome, usage, class, _, _ := classifyCodexJSON(line)
	if outcome != "" {
		t.Errorf("outcome = %q, want empty (turn.completed isn't terminal)", outcome)
	}
	if usage == nil || usage.InputTokens != 15 || usage.OutputTokens != 25 {
		t.Errorf("usage = %+v, want input=15 output=25", usage)
	}
	if class != agent.ClassNone {
		t.Errorf("class = %q, want none", class)
	}
}

// TestClassifyCodexJSON_TurnFailed verifies a turn.failed event yields a
// failure outcome and a classification derived from the error message.
func TestClassifyCodexJSON_TurnFailed(t *testing.T) {
	line := `{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header"}}`
	_, outcome, _, class, _, _ := classifyCodexJSON(line)
	if outcome != "failure" {
		t.Errorf("outcome = %q, want failure", outcome)
	}
	if class != agent.ClassAuth {
		t.Errorf("class = %q, want auth", class)
	}
}

// TestClassifyCodexJSON_TurnFailedGenuine verifies a turn.failed event whose
// message carries no recognizable infra/auth signal classifies as genuine.
func TestClassifyCodexJSON_TurnFailedGenuine(t *testing.T) {
	line := `{"type":"turn.failed","error":{"message":"the model declined to continue"}}`
	_, _, _, class, _, _ := classifyCodexJSON(line)
	if class != agent.ClassGenuine {
		t.Errorf("class = %q, want genuine", class)
	}
}

// TestClassifyCodexJSON_AgentMessageCompleted verifies a completed
// agent_message item is scanned for an OUTCOME marker.
func TestClassifyCodexJSON_AgentMessageCompleted(t *testing.T) {
	line := `{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"All done. OUTCOME: success"}}`
	entry, outcome, _, _, _, _ := classifyCodexJSON(line)
	if outcome != "success" {
		t.Errorf("outcome = %q, want success", outcome)
	}
	if entry.Type != agent.LogStdout {
		t.Errorf("entry.Type = %q, want stdout", entry.Type)
	}
}

// TestClassifyCodexJSON_AgentMessageStartedNoOutcome verifies an in-progress
// (item.started) agent_message is NOT scanned for an outcome (only the
// terminal item.completed event should resolve one).
func TestClassifyCodexJSON_AgentMessageStartedNoOutcome(t *testing.T) {
	line := `{"type":"item.started","item":{"id":"item_0","type":"agent_message","text":"OUTCOME: success"}}`
	_, outcome, _, _, _, _ := classifyCodexJSON(line)
	if outcome != "" {
		t.Errorf("outcome = %q, want empty for an in-progress item", outcome)
	}
}

// TestClassifyCodexJSON_CommandExecution verifies command_execution items map
// to agent.LogToolCall (in-progress) / agent.LogToolResult (completed).
func TestClassifyCodexJSON_CommandExecution(t *testing.T) {
	started := `{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"ls","status":"in_progress"}}`
	entry, _, _, _, _, _ := classifyCodexJSON(started)
	if entry.Type != agent.LogToolCall {
		t.Errorf("entry.Type = %q, want tool_call", entry.Type)
	}

	completed := `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"ls","aggregated_output":"a.txt","exit_code":0,"status":"completed"}}`
	entry, _, _, class, _, _ := classifyCodexJSON(completed)
	if entry.Type != agent.LogToolResult {
		t.Errorf("entry.Type = %q, want tool_result", entry.Type)
	}
	if class != agent.ClassNone {
		t.Errorf("class = %q, want none for a successful command", class)
	}

	failed := `{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"curl x","aggregated_output":"connection reset by peer","exit_code":1,"status":"failed"}}`
	_, _, _, class, _, _ = classifyCodexJSON(failed)
	if class != agent.ClassTransient {
		t.Errorf("class = %q, want transient for a connection-reset failure", class)
	}
}

// TestClassifyCodexJSON_McpToolCall verifies mcp_tool_call items map to
// agent.LogToolCall/agent.LogToolResult and classify failures from the error message.
func TestClassifyCodexJSON_McpToolCall(t *testing.T) {
	completed := `{"type":"item.completed","item":{"id":"item_2","type":"mcp_tool_call","server":"task-editor","tool":"signal_complete","status":"failed","error":{"message":"429 rate limit"}}}`
	entry, _, _, class, _, _ := classifyCodexJSON(completed)
	if entry.Type != agent.LogToolResult {
		t.Errorf("entry.Type = %q, want tool_result", entry.Type)
	}
	if class != agent.ClassRateLimit {
		t.Errorf("class = %q, want rate_limit", class)
	}
}

// TestClassifyCodexJSON_NonJSONLine verifies interleaved plain-text log lines
// (Codex mixes Rust tracing ERROR lines into stdout) degrade to a raw stdout
// entry rather than erroring.
func TestClassifyCodexJSON_NonJSONLine(t *testing.T) {
	line := `2026-07-08T01:16:07.304228Z ERROR codex_api::endpoint::responses_websocket: failed to connect to websocket`
	entry, outcome, usage, class, sid, _ := classifyCodexJSON(line)
	if entry.Type != agent.LogStdout || entry.Content != line {
		t.Errorf("unexpected entry for non-JSON line: %+v", entry)
	}
	if outcome != "" || usage != nil || class != agent.ClassNone || sid != "" {
		t.Errorf("expected all-zero extras for non-JSON line, got %q %v %q %q", outcome, usage, class, sid)
	}
}

// --- Subprocess lifecycle tests (generalized from claude_test.go's
// CLAUDE_TEST_HELPER re-exec harness — see TestMain in claude_test.go) ---

// codexHelperInput builds a minimal RunInput sufficient for CodexRunner.Run,
// passing the helper mode via Env so the re-exec'd test binary knows which
// fake-CLI behavior to emit.
func codexHelperInput(mode string) agent.RunInput {
	return agent.RunInput{
		RunID:       "codex-test-run",
		Task:        agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{Env: map[string]string{"CODEX_TEST_HELPER": mode}, TimeoutSecs: 10},
		RepoPath:    os.TempDir(),
	}
}

func TestCodexRunner_Binary_DefaultsAndOverrides(t *testing.T) {
	var r CodexRunner
	if got := r.binary(); got != "codex" {
		t.Errorf("binary() = %q, want default %q", got, "codex")
	}
	r.BinaryPath = "/opt/codex"
	if got := r.binary(); got != "/opt/codex" {
		t.Errorf("binary() = %q, want override %q", got, "/opt/codex")
	}
}

func TestCodexRunner_Run_Exit0Success(t *testing.T) {
	runner := &CodexRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), codexHelperInput("exit0_success"), logCh)
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
	if result.SessionID != "thread-1" {
		t.Errorf("SessionID = %q, want thread-1", result.SessionID)
	}
	if result.InputTokens != 10 || result.OutputTokens != 20 {
		t.Errorf("usage = %d/%d, want 10/20", result.InputTokens, result.OutputTokens)
	}
}

// TestCodexRunner_Run_PricesTokensAgainstResolver is the regression test for
// issue #245 (codex_cli cost budgets never trip, because codex never priced
// its captured tokens — see classifyCodexJSON/applyUsage before this fix).
// With a PriceResolver that recognizes the configured model, Run must price
// the "turn.completed" event's 10/20 input/output tokens (see
// codexHelperInput("exit0_success") above) into a non-zero CostUSD and
// leave CostUnknown false — the same estimation path qwen/anthropic/llm use
// (applyUsageWithCost, providers/pricing.go).
func TestCodexRunner_Run_PricesTokensAgainstResolver(t *testing.T) {
	runner := &CodexRunner{
		BinaryPath:    os.Args[0],
		PriceResolver: fakePriceResolver{inPer1M: 10, outPer1M: 20, known: true},
	}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := codexHelperInput("exit0_success")
	input.AgentConfig.Model = "priced-model"
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	// 10 input tokens * $10/1M + 20 output tokens * $20/1M = 0.0001 + 0.0004 = 0.0005
	wantCost := (float64(10)/1_000_000)*10 + (float64(20)/1_000_000)*20
	if result.CostUSD != wantCost {
		t.Errorf("CostUSD = %v, want %v (priced from 10/20 tokens)", result.CostUSD, wantCost)
	}
	if result.CostUSD <= 0 {
		t.Error("expected a non-zero CostUSD for a run against a priced model")
	}
	if result.CostUnknown {
		t.Error("expected CostUnknown=false when the model is in the pricing table")
	}
}

// TestCodexRunner_Run_UnpricedModelFlagsCostUnknown covers the other half of
// #245's acceptance criteria: a run whose cost cannot be determined must be
// flagged, not silently reported as $0. With a PriceResolver that doesn't
// recognize the configured model (known=false), Run must leave CostUSD at 0
// but set CostUnknown=true, since tokens were genuinely consumed.
func TestCodexRunner_Run_UnpricedModelFlagsCostUnknown(t *testing.T) {
	runner := &CodexRunner{
		BinaryPath:    os.Args[0],
		PriceResolver: fakePriceResolver{known: false},
	}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := codexHelperInput("exit0_success")
	input.AgentConfig.Model = "some-unpriced-model"
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.CostUSD != 0 {
		t.Errorf("CostUSD = %v, want 0 for an unpriced model", result.CostUSD)
	}
	if !result.CostUnknown {
		t.Error("expected CostUnknown=true for a run with consumed tokens against an unpriced model")
	}
	if result.InputTokens != 10 || result.OutputTokens != 20 {
		t.Errorf("usage = %d/%d, want 10/20 (tokens still recorded even when unpriced)", result.InputTokens, result.OutputTokens)
	}
}

// TestCodexRunner_Run_FailurePathPersistsPricedCost verifies that a run
// whose turn reports usage before ultimately failing (exit1 with
// turn.completed usage followed by turn.failed, resolving Status=failed —
// see TestCodexRunner_Run_Exit1WithTurnFailed) still prices whatever usage
// was captured, mirroring qwen's equivalent failure-path coverage. Before
// the #245 fix, codex captured tokens but never priced them on any path,
// including this one; a run that failed after spending money still needs an
// accurate CostUSD/CostUnknown so the pre-dispatch budget guard sees the
// real accumulated spend.
func TestCodexRunner_Run_FailurePathPersistsPricedCost(t *testing.T) {
	runner := &CodexRunner{
		BinaryPath:    os.Args[0],
		PriceResolver: fakePriceResolver{inPer1M: 10, outPer1M: 20, known: true},
	}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := codexHelperInput("exit1_with_usage")
	input.AgentConfig.Model = "priced-model"
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
	if result.InputTokens != 10 || result.OutputTokens != 20 {
		t.Errorf("usage = %d/%d, want 10/20", result.InputTokens, result.OutputTokens)
	}
	wantCost := (float64(10)/1_000_000)*10 + (float64(20)/1_000_000)*20
	if result.CostUSD != wantCost {
		t.Errorf("CostUSD = %v, want %v (priced even on a failed run)", result.CostUSD, wantCost)
	}
}

// TestCodexRunner_Run_WithMCP_PreparesAndCleansUpCodexHome verifies the MCP
// sidecar wiring path: when r.MCP is configured with a ServerBinary, Run
// must call prepareCodexHome to write a per-run $CODEX_HOME/config.toml
// (rather than touching any shared/global config) and pass CODEX_HOME to the
// child process, then Cleanup must remove that directory once Run returns.
//
// Regression coverage for a review finding on #251: prepareCodexHome and
// codexRunConfig.Cleanup were still at 0% coverage because no lifecycle test
// ever set runner.MCP, so this branch of Run was never exercised at all.
func TestCodexRunner_Run_WithMCP_PreparesAndCleansUpCodexHome(t *testing.T) {
	mcpBinary := os.Args[0] // any non-empty path; MCPManager.Prepare only writes it into config files, never executes it
	runner := &CodexRunner{
		BinaryPath: os.Args[0],
		MCP:        &MCPManager{ServerBinary: mcpBinary},
	}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()

	input := codexHelperInput("exit0_success")

	// Run synchronously; the helper process exits immediately, so by the
	// time Run returns, its deferred codexHome.Cleanup() has already fired
	// and we can only assert the directory is gone afterward. To also
	// observe the directory *while it exists* (proving prepareCodexHome
	// actually ran, not just that Cleanup ran on a nil config), call
	// prepareCodexHome directly first and compare its deterministic path
	// convention against what Run would have used for the same RunID.
	wantHomeDir := filepath.Join(os.TempDir(), fmt.Sprintf("ate-codex-home-%s", input.RunID))

	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" || result.Outcome != "success" {
		t.Fatalf("Run result = %+v, want completed/success", result)
	}

	// Cleanup (deferred inside Run) must have removed the per-run CODEX_HOME
	// directory by the time Run returns.
	if _, statErr := os.Stat(wantHomeDir); !os.IsNotExist(statErr) {
		t.Errorf("expected CODEX_HOME dir %q to be removed by Cleanup after Run, stat err = %v", wantHomeDir, statErr)
	}

	// Directly exercise prepareCodexHome/Cleanup's own behavior (config.toml
	// contents + directory lifecycle), independent of Run's timing, so this
	// test doesn't rely solely on the directory already being gone above.
	entry := mcpServerEntry{Command: mcpBinary, Args: []string{}, Env: map[string]string{"RUN_ID": input.RunID}}
	cfg, err := prepareCodexHome("standalone-"+input.RunID, &entry)
	if err != nil {
		t.Fatalf("prepareCodexHome: %v", err)
	}
	if cfg == nil {
		t.Fatal("prepareCodexHome returned a nil config for a non-nil entry")
	}
	tomlPath := filepath.Join(cfg.HomeDir, "config.toml")
	if _, statErr := os.Stat(tomlPath); statErr != nil {
		t.Errorf("expected config.toml to exist at %q: %v", tomlPath, statErr)
	}
	cfg.Cleanup()
	if _, statErr := os.Stat(cfg.HomeDir); !os.IsNotExist(statErr) {
		t.Errorf("expected Cleanup to remove %q, stat err = %v", cfg.HomeDir, statErr)
	}

	// prepareCodexHome returns (nil, nil) when there's no MCP server entry to
	// configure (the r.MCP == nil / ServerBinary == "" branch in Run).
	if nilCfg, nilErr := prepareCodexHome("no-entry", nil); nilCfg != nil || nilErr != nil {
		t.Errorf("prepareCodexHome(nil entry) = (%v, %v), want (nil, nil)", nilCfg, nilErr)
	}

	// Cleanup on a nil *codexRunConfig (the r.MCP == nil branch's `defer
	// codexHome.Cleanup()` in Run) must be a safe no-op.
	var nilCfgPtr *codexRunConfig
	nilCfgPtr.Cleanup()
}

// TestCodexRunner_Run_Exit1WithTurnFailed verifies a non-zero exit whose
// stream carries a "turn.failed" event surfaces Status=failed. codex's
// turn.failed event resolves a non-empty outcome ("failure"), but a non-zero
// exit always overrides to Status=failed regardless of any parsed outcome
// (parity with claude's runner — see TestClaudeExitCode1_IsFailed — a
// non-zero exit means the run's outcome is unverified).
func TestCodexRunner_Run_Exit1WithTurnFailed(t *testing.T) {
	runner := &CodexRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), codexHelperInput("exit1"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestCodexRunner_Run_Exit1NoOutcomeIsFailed verifies that a non-zero exit
// with no parsed outcome at all (e.g. the process crashed before emitting a
// terminal event) is reported as Status=failed.
func TestCodexRunner_Run_Exit1NoOutcomeIsFailed(t *testing.T) {
	runner := &CodexRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := codexHelperInput("")
	input.AgentConfig.Env["CODEX_TEST_HELPER"] = "" // no case in TestMain -> falls through to real test run
	// Instead of relying on a TestMain case, directly exercise a binary that
	// exits 1 with no stdout at all via /bin/false-equivalent: reuse the test
	// binary but with an unrecognized helper mode so it runs `go test` (not
	// desired) — so instead point BinaryPath at a real "false" command that
	// always exits 1 with no output, independent of the re-exec harness.
	runner.BinaryPath = "false"
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestCodexRunner_Run_TimeoutKillsProcess verifies that when AgentConfig's
// timeout elapses while the (fake) CLI is still running, Run returns an
// *agent.ErrTransient rather than hanging forever. Regression coverage for
// the previously-untested context-cancel/timeout branch shared by every CLI
// provider (see codex.go's runCtx, context.WithTimeout).
func TestCodexRunner_Run_TimeoutKillsProcess(t *testing.T) {
	runner := &CodexRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := codexHelperInput("")
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

// TestCodexRunner_Run_OversizedLineTruncation mirrors
// TestClaude_OversizedLine_Truncation in claude_test.go — proves the shared
// scanLines helper (scan.go) fixes the silent-truncation bug for codex too:
// a stdout line exceeding the scan buffer cap must surface a visible
// LogSystem warning and end the run with a non-"completed" status, promptly
// (not wedged until the outer timeout).
func TestCodexRunner_Run_OversizedLineTruncation(t *testing.T) {
	runner := &CodexRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), codexHelperInput("oversized_line"), logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()
	logs := drainLogs(logCh)

	var res outcome
	select {
	case res = <-ch:
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for Run to return — oversized line likely wedged the run instead of failing promptly")
	}

	if res.r.Status == "completed" {
		t.Errorf("want Status != completed (output was truncated), got %q", res.r.Status)
	}
	if res.err == nil {
		t.Error("want a non-nil error signalling the truncated run, got nil")
	}

	var sawTruncationLog bool
	for _, e := range logs {
		if e.Type == agent.LogSystem && (contains(e.Content, "truncated") || contains(e.Content, "scan limit")) {
			sawTruncationLog = true
		}
	}
	if !sawTruncationLog {
		t.Errorf("expected a LogSystem entry warning about the truncated output stream, got logs: %v", logs)
	}
}
