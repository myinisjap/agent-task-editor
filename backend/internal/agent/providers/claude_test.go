package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestMain supports the subprocess helper pattern: when the test binary is
// re-invoked with CLAUDE_TEST_HELPER=1, it acts as a fake "claude" binary
// instead of running tests.
//
// This same TestMain is shared by every CLI provider test in this package
// (codex_test.go, gemini_test.go, qwen_test.go, opencode_test.go) because Go
// only allows one TestMain per package and all providers live in "providers".
// Each provider's fake-binary helper is keyed off its own env var
// (CODEX_TEST_HELPER, GEMINI_TEST_HELPER, QWEN_TEST_HELPER,
// OPENCODE_TEST_HELPER) to avoid collisions when a test only sets one of
// them. A shared "hang" mode (HANG_TEST_HELPER, in seconds) is used by the
// context-cancel/timeout tests across providers.
func TestMain(m *testing.M) {
	switch os.Getenv("CLAUDE_TEST_HELPER") {
	case "exit1":
		// Simulate: claude exits with code 1 (auth error, crash, etc.).
		// Emit a stream-json result line that looks like a success so we can
		// verify the exit code still wins over the parsed outcome.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success"}`)
		os.Exit(1)
	case "exit1_no_output":
		// Simulate: claude exits with code 1 and no useful output.
		os.Exit(1)
	case "exit0_success":
		// Simulate: claude exits cleanly with a success outcome.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success"}`)
		os.Exit(0)
	case "exit0_no_outcome":
		// Simulate: claude exits cleanly with no outcome (empty result).
		os.Exit(0)
	case "exit0_success_with_usage":
		// Simulate: claude exits cleanly with a success outcome and a
		// result message carrying usage/total_cost_usd, as the real CLI
		// does — total_cost_usd here is authoritative and should be used
		// as-is rather than re-estimated.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success","usage":{"input_tokens":111,"output_tokens":222},"total_cost_usd":0.05}`)
		os.Exit(0)
	case "session_limit_429":
		// Simulate: claude hits its session limit — the exact sample JSON
		// from the task, with a non-zero exit (as real 429s from the CLI do).
		fmt.Println(`{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"duration_ms":844,"duration_api_ms":0,"num_turns":1,"result":"You've hit your session limit ` + "·" + ` resets 6pm (America/Chicago)","stop_reason":"stop_sequence","session_id":"16228fd1-bcd9-4dee-b14d-7537b3bce8ea","total_cost_usd":0,"usage":{"input_tokens":0,"output_tokens":0},"modelUsage":{},"permission_denials":[],"terminal_reason":"completed","fast_mode_state":"off","uuid":"044c12cd-40a6-4e81-8ee8-e7da2e1f9c23"}`)
		os.Exit(1)
	case "error_max_turns":
		// Simulate: claude exhausts its configured --max-turns and exits 0
		// (unlike auth errors/crashes, which exit non-zero) with a
		// subtype:"error_max_turns" result — the case that must NOT be
		// treated as a normal "completed" run.
		fmt.Println(`{"type":"result","subtype":"error_max_turns","is_error":true,"result":"reached max turns","session_id":"max-turns-session","total_cost_usd":0.01,"usage":{"input_tokens":10,"output_tokens":20}}`)
		os.Exit(0)
	case "cost_watchdog_kill":
		// Simulate: a long-running agent whose incremental per-turn usage
		// (assistant-message usage, real CLIs never report total_cost_usd on
		// these) projects well past its configured cost budget partway
		// through. The watchdog (see cost_watchdog.go) should cancel this
		// subprocess before it reaches the final "result" line below — the
		// test's RunInput sets a tiny CostBudgetUSD so a single assistant
		// message's tokens (priced via the test's fakePriceResolver) already
		// exceed it. Sleep briefly after each message so the scan goroutine
		// has time to observe usage and call cancel() before the process
		// would otherwise exit on its own, keeping the test deterministic
		// about *why* the process ended (watchdog kill, not a natural exit).
		fmt.Println(`{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":1000000,"output_tokens":1000000}},"session_id":"cost-watchdog-session"}`)
		time.Sleep(2 * time.Second)
		// Only reached if the watchdog failed to cancel the run in time.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success","usage":{"input_tokens":1000000,"output_tokens":1000000},"total_cost_usd":100}`)
		os.Exit(0)
	case "oversized_line":
		// Simulate: a single stdout line (e.g. an assistant message quoting a
		// large file a tool Read/Wrote) exceeding the scan buffer limit
		// (see scan.go's maxScanLineBytes). Emitted well over that cap so the
		// scanner hits bufio.ErrTooLong regardless of the exact configured
		// limit. Followed by more output that must NOT be observed by the
		// runner — proving the truncation, rather than a parse error on this
		// specific line, is what's detected.
		fmt.Println(`{"type":"assistant","message":{"role":"assistant","content":"` + strings.Repeat("A", 9*1024*1024) + `"}}`)
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success"}`)
		os.Exit(0)
	}
	switch os.Getenv("CODEX_TEST_HELPER") {
	case "exit0_success":
		fmt.Println(`{"type":"thread.started","thread_id":"thread-1"}`)
		fmt.Println(`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"OUTCOME: success"}}`)
		fmt.Println(`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":20}}`)
		os.Exit(0)
	case "exit1":
		fmt.Println(`{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized"}}`)
		os.Exit(1)
	case "oversized_line":
		// Mirrors claude's "oversized_line" case above — proves the shared
		// scanLines helper (scan.go) fixes the same bug across every CLI
		// provider, not just claude.
		fmt.Println(`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"` + strings.Repeat("A", 9*1024*1024) + `"}}`)
		fmt.Println(`{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"OUTCOME: success"}}`)
		os.Exit(0)
	}

	switch os.Getenv("GEMINI_TEST_HELPER") {
	case "exit0_success":
		fmt.Println(`{"type":"init","session_id":"gem-1","model":"auto"}`)
		fmt.Println(`{"type":"message","role":"assistant","content":"OUTCOME: success"}`)
		fmt.Println(`{"type":"result","status":"success","stats":{"input_tokens":10,"output_tokens":20}}`)
		os.Exit(0)
	case "exit1":
		fmt.Println(`{"type":"result","status":"error","error":{"type":"unknown","message":"boom"}}`)
		os.Exit(1)
	}

	// Qwen reuses this same subprocess-helper pattern (and the same
	// stream-json envelope/parser as claude — see parse_qwen.go), via a
	// separate env var so both providers' helper tests can share one
	// TestMain/binary.
	switch os.Getenv("QWEN_TEST_HELPER") {
	case "exit0_success":
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success","usage":{"input_tokens":5,"output_tokens":7}}`)
		os.Exit(0)
	case "exit1_no_output":
		os.Exit(1)
	case "exit1_with_outcome":
		// Unlike claude (which explicitly overrides a parsed outcome when the
		// exit code is non-zero), qwen.go has no such override: a non-empty
		// parsed outcome wins regardless of exit code. This case documents
		// that actual (if surprising) behavior.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success"}`)
		os.Exit(1)
	case "error_max_turns":
		// Simulate: qwen exhausts its configured --max-session-turns and
		// exits 0 (mirrors claude's error_max_turns behavior).
		fmt.Println(`{"type":"result","subtype":"error_max_turns","is_error":true,"result":"reached max turns","session_id":"qwen-max-turns-session","total_cost_usd":0.02,"usage":{"input_tokens":30,"output_tokens":40}}`)
		os.Exit(0)
	case "cost_watchdog_kill":
		// Mirrors claude's "cost_watchdog_kill" helper mode above — qwen
		// shares the same stream-json envelope/watchdog wiring (see qwen.go).
		fmt.Println(`{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":1000000,"output_tokens":1000000}},"session_id":"qwen-cost-watchdog-session"}`)
		time.Sleep(2 * time.Second)
		// Only reached if the watchdog failed to cancel the run in time.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success","usage":{"input_tokens":1000000,"output_tokens":1000000},"total_cost_usd":100}`)
		os.Exit(0)
	case "oversized_line":
		// Mirrors claude's "oversized_line" case above (same stream-json
		// envelope/parser as claude — see parse_qwen.go).
		fmt.Println(`{"type":"assistant","message":{"role":"assistant","content":"` + strings.Repeat("A", 9*1024*1024) + `"}}`)
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success"}`)
		os.Exit(0)
	}

	switch os.Getenv("OPENCODE_TEST_HELPER") {
	case "exit0_success":
		fmt.Println(`{"type":"text","sessionID":"oc-1","part":{"type":"text","text":"OUTCOME: success"}}`)
		fmt.Println(`{"type":"step_finish","sessionID":"oc-1","part":{"reason":"stop"}}`)
		os.Exit(0)
	case "exit1":
		os.Exit(1)
	case "oversized_line":
		// Mirrors claude's "oversized_line" case above — proves the shared
		// scanLines helper (scan.go) fixes the same bug for opencode too.
		fmt.Println(`{"type":"text","sessionID":"oc-1","part":{"type":"text","text":"` + strings.Repeat("A", 9*1024*1024) + `"}}`)
		fmt.Println(`{"type":"text","sessionID":"oc-1","part":{"type":"text","text":"OUTCOME: success"}}`)
		fmt.Println(`{"type":"step_finish","sessionID":"oc-1","part":{"reason":"stop"}}`)
		os.Exit(0)
	case "exit0_success_with_usage":
		// Simulate: opencode emits two step_finish events (one per step),
		// each carrying cumulative-to-date cost/tokens (see classifyOpencodeJSON's
		// doc comment on the cumulative-vs-per-step assumption). The runner
		// must take the *last* one's values, not sum across steps — so the
		// final Result should reflect only the second step_finish's numbers.
		fmt.Println(`{"type":"text","sessionID":"oc-1","part":{"type":"text","text":"working"}}`)
		fmt.Println(`{"type":"step_finish","sessionID":"oc-1","part":{"reason":"tool_calls","cost":0.01,"tokens":{"input":10,"output":20}}}`)
		fmt.Println(`{"type":"text","sessionID":"oc-1","part":{"type":"text","text":"OUTCOME: success"}}`)
		fmt.Println(`{"type":"step_finish","sessionID":"oc-1","part":{"reason":"stop","cost":0.05,"tokens":{"input":100,"output":200}}}`)
		os.Exit(0)
	}

	if secs := os.Getenv("HANG_TEST_HELPER"); secs != "" {
		var n int
		_, _ = fmt.Sscanf(secs, "%d", &n)
		if n <= 0 {
			n = 30
		}
		time.Sleep(time.Duration(n) * time.Second)
		os.Exit(0)
	}

	os.Exit(m.Run())
}

// helperRunner returns a ClaudeRunner whose binary re-invokes the current test
// binary as the given helper mode.
func helperRunner(mode string) *ClaudeRunner {
	return &ClaudeRunner{
		BinaryPath: os.Args[0], // re-invoke the test binary itself
	}
}

// makeInput builds a minimal RunInput sufficient for ClaudeRunner.Run.
func makeInput(mode string) agent.RunInput {
	return agent.RunInput{
		RunID: "test-run",
		Task:  agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{
			// Pass the mode via Env so the test binary knows which helper to be.
			Env:         map[string]string{"CLAUDE_TEST_HELPER": mode},
			TimeoutSecs: 10,
		},
		RepoPath: os.TempDir(),
	}
}

func drainLogs(logCh <-chan agent.LogEntry) []agent.LogEntry {
	var entries []agent.LogEntry
	for e := range logCh {
		entries = append(entries, e)
	}
	return entries
}

func runWithHelper(t *testing.T, mode string) (agent.Result, []agent.LogEntry) {
	t.Helper()
	runner := helperRunner(mode)
	logCh := make(chan agent.LogEntry, 256)

	// Run in a goroutine so we can drain logs concurrently.
	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), makeInput(mode), logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()

	logs := drainLogs(logCh)
	res := <-ch
	if res.err != nil {
		t.Fatalf("Run returned unexpected error: %v", res.err)
	}
	return res.r, logs
}

// TestClaudeExitCode1_IsFailed verifies that a non-zero exit from the claude
// binary is always treated as failure, even when the stream output contained a
// success outcome.
func TestClaudeExitCode1_IsFailed(t *testing.T) {
	result, logs := runWithHelper(t, "exit1")

	if result.Status != "failed" {
		t.Errorf("want Status=%q, got %q", "failed", result.Status)
	}
	// Log a warning if outcome was discarded — verify we emitted the discrepancy log.
	var found bool
	for _, e := range logs {
		if e.Type == agent.LogSystem {
			if contains(e.Content, "treating as failed") {
				found = true
			}
		}
	}
	if !found {
		t.Logf("(optional) expected a 'treating as failed' log entry; logs: %v", logContents(logs))
	}
}

// TestClaudeExitCode1_NoOutput_IsFailed verifies exit-code-1 with no output
// is treated as failure.
func TestClaudeExitCode1_NoOutput_IsFailed(t *testing.T) {
	result, _ := runWithHelper(t, "exit1_no_output")
	if result.Status != "failed" {
		t.Errorf("want Status=%q, got %q", "failed", result.Status)
	}
}

// TestClaudeExitCode0_Success verifies normal success path still works.
func TestClaudeExitCode0_Success(t *testing.T) {
	result, _ := runWithHelper(t, "exit0_success")
	if result.Status != "completed" {
		t.Errorf("want Status=%q, got %q", "completed", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("want Outcome=%q, got %q", "success", result.Outcome)
	}
}

// TestClaudeExitCode0_NoOutcome verifies exit-0 with no outcome returns
// completed with empty outcome (not failed).
func TestClaudeExitCode0_NoOutcome(t *testing.T) {
	result, _ := runWithHelper(t, "exit0_no_outcome")
	if result.Status != "completed" {
		t.Errorf("want Status=%q, got %q", "completed", result.Status)
	}
}

// TestClaudeRunner_PropagatesUsageFromResultMessage verifies that a full
// Run() invocation propagates the token usage and CLI-authoritative
// total_cost_usd parsed from the stream-json "result" message onto the
// returned Result (the non-MCP code path).
func TestClaudeRunner_PropagatesUsageFromResultMessage(t *testing.T) {
	result, _ := runWithHelper(t, "exit0_success_with_usage")
	if result.Status != "completed" {
		t.Fatalf("want Status=completed, got %q", result.Status)
	}
	if result.InputTokens != 111 {
		t.Errorf("want InputTokens=111, got %d", result.InputTokens)
	}
	if result.OutputTokens != 222 {
		t.Errorf("want OutputTokens=222, got %d", result.OutputTokens)
	}
	if result.CostUSD != 0.05 {
		t.Errorf("want CostUSD=0.05, got %v", result.CostUSD)
	}
}

// TestClaudeRunner_RateLimitResetAtFromResultText verifies that a session-
// limit 429 (the exact stream-json sample from the task) is surfaced as an
// *ErrRateLimit with ResetAt populated from the parsed "resets 6pm
// (America/Chicago)" clue in the result text, roughly 1 minute after 6pm
// Chicago time (today or tomorrow, depending on when the test runs).
func TestClaudeRunner_RateLimitResetAtFromResultText(t *testing.T) {
	runner := helperRunner("session_limit_429")
	logCh := make(chan agent.LogEntry, 256)

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), makeInput("session_limit_429"), logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()
	drainLogs(logCh)
	res := <-ch

	var rl *agent.ErrRateLimit
	if !asErrRateLimit(res.err, &rl) {
		t.Fatalf("want *ErrRateLimit, got err=%v", res.err)
	}
	if rl.ResetAt.IsZero() {
		t.Fatalf("want non-zero ResetAt, got zero")
	}
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	inChicago := rl.ResetAt.In(chicago)
	if inChicago.Hour() != 18 || inChicago.Minute() != 1 {
		t.Errorf("want 18:01 America/Chicago, got %v", inChicago)
	}
	if res.r.Status != "failed" {
		t.Errorf("want Status=failed, got %q", res.r.Status)
	}
}

// asErrRateLimit is a small errors.As wrapper local to this test file to
// avoid importing "errors" solely for one call site.
func asErrRateLimit(err error, target **agent.ErrRateLimit) bool {
	rl, ok := err.(*agent.ErrRateLimit)
	if !ok {
		return false
	}
	*target = rl
	return true
}

// asErrMaxTurns is a small errors.As wrapper local to this test file,
// mirroring asErrRateLimit above.
func asErrMaxTurns(err error, target **agent.ErrMaxTurns) bool {
	mt, ok := err.(*agent.ErrMaxTurns)
	if !ok {
		return false
	}
	*target = mt
	return true
}

// TestClaudeRunner_ErrorMaxTurns verifies that a subtype:"error_max_turns"
// stream-json result — which the real CLI emits with exit code 0, unlike
// auth errors/crashes which exit non-zero — drives Run to return
// *agent.ErrMaxTurns rather than reporting a normal "completed" result. This
// is the Path B regression from the issue: exiting 0 must not let the
// MCP/outcome fallthrough win.
func TestClaudeRunner_ErrorMaxTurns(t *testing.T) {
	runner := helperRunner("error_max_turns")
	logCh := make(chan agent.LogEntry, 256)

	input := makeInput("error_max_turns")
	input.AgentConfig.MaxTurns = 7

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), input, logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()
	drainLogs(logCh)
	res := <-ch

	var mt *agent.ErrMaxTurns
	if !asErrMaxTurns(res.err, &mt) {
		t.Fatalf("want *agent.ErrMaxTurns, got err=%v (%T)", res.err, res.err)
	}
	if mt.MaxTurns != 7 {
		t.Errorf("want MaxTurns=7, got %d", mt.MaxTurns)
	}
	if res.r.Status != "failed" {
		t.Errorf("want Status=failed, got %q", res.r.Status)
	}
	if res.r.SessionID != "max-turns-session" {
		t.Errorf("want session id preserved, got %q", res.r.SessionID)
	}
	if res.r.InputTokens != 10 || res.r.OutputTokens != 20 {
		t.Errorf("want usage preserved (10/20), got (%d/%d)", res.r.InputTokens, res.r.OutputTokens)
	}
}

// TestClaudeRunner_CostWatchdogKillsRun verifies the mid-run cost watchdog
// (see cost_watchdog.go): a subprocess that emits an assistant-message usage
// block whose projected cost (via the runner's PriceResolver) crosses a tiny
// configured CostBudgetUSD is cancelled before it reaches its own "result"
// line, and Run returns *agent.ErrCostBudgetExceeded rather than treating the
// resulting context-cancellation as a plain failure/transient error. Run()
// must also not attempt any resume/cold-start fallback in this case.
func TestClaudeRunner_CostWatchdogKillsRun(t *testing.T) {
	runner := helperRunner("cost_watchdog_kill")
	runner.PriceResolver = fakePriceResolver{inPer1M: 10, outPer1M: 10, known: true}
	logCh := make(chan agent.LogEntry, 256)

	input := makeInput("cost_watchdog_kill")
	// 1,000,000 input + 1,000,000 output tokens at $10/1M each = $10 + $10 =
	// $20 projected, comfortably over this tiny budget.
	input.CostBudgetUSD = 1.00
	input.CostWarnRatio = 0.8
	input.AgentConfig.TimeoutSecs = 10

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), input, logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()
	logs := drainLogs(logCh)

	var res outcome
	select {
	case res = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return — watchdog likely failed to cancel the subprocess")
	}

	var ce *agent.ErrCostBudgetExceeded
	if !errors.As(res.err, &ce) {
		t.Fatalf("want *agent.ErrCostBudgetExceeded, got err=%v (%T)", res.err, res.err)
	}
	if ce.BudgetUSD != 1.00 {
		t.Errorf("want BudgetUSD=1.00, got %v", ce.BudgetUSD)
	}
	if ce.SpentUSD <= ce.BudgetUSD {
		t.Errorf("want SpentUSD (%v) to exceed BudgetUSD (%v)", ce.SpentUSD, ce.BudgetUSD)
	}
	if res.r.Status != "failed" {
		t.Errorf("want Status=failed, got %q", res.r.Status)
	}
	if !res.r.CostWarned {
		t.Error("want CostWarned=true — crossing the budget also crosses the warn ratio")
	}
	if res.r.SessionID != "cost-watchdog-session" {
		t.Errorf("want session id preserved from the assistant message, got %q", res.r.SessionID)
	}
	// Regression coverage: a killed run never reaches its terminal "result"
	// event, so the persisted Result must come from the watchdog's own
	// cumulative-usage snapshot (attemptInfo.costExceededUsage), not from
	// applyUsage(nil) silently leaving everything at zero — see
	// pool.handleCostBudgetExceeded / SetAgentRunCompleted, which persist
	// res.CostUSD/InputTokens/OutputTokens onto the run row, and
	// SumTaskCost, which sums cost_usd across every run including killed
	// ones for the pre-dispatch budget guard.
	if res.r.InputTokens != 1_000_000 {
		t.Errorf("want InputTokens=1,000,000 (from the assistant-message usage), got %d", res.r.InputTokens)
	}
	if res.r.OutputTokens != 1_000_000 {
		t.Errorf("want OutputTokens=1,000,000 (from the assistant-message usage), got %d", res.r.OutputTokens)
	}
	// $10/1M input + $10/1M output * 1M each = $20; CostSpentUSD defaults to
	// 0 in this test (makeInput doesn't set it), so this run's own
	// incremental cost equals the full projection.
	if res.r.CostUSD != 20.0 {
		t.Errorf("want CostUSD=20.0 (this run's own incremental cost, not left at 0), got %v", res.r.CostUSD)
	}

	var sawKillLog bool
	for _, e := range logs {
		if e.Type == agent.LogSystem && contains(e.Content, "cost watchdog") {
			sawKillLog = true
		}
	}
	if !sawKillLog {
		t.Error("expected a cost-watchdog system log line")
	}
}

// TestClaudeRunner_CostWatchdogKillsRun_SubtractsPriorSpend mirrors the qwen
// test of the same name: when a task already has prior recorded spend
// (input.CostSpentUSD > 0), the killed run's own persisted CostUSD must be
// only its *own* incremental cost — the watchdog's projected total minus the
// prior-spend baseline — not the full task-wide projection, or SumTaskCost
// would double-count prior runs' cost every time it sums cost_usd across a
// task's runs.
func TestClaudeRunner_CostWatchdogKillsRun_SubtractsPriorSpend(t *testing.T) {
	runner := helperRunner("cost_watchdog_kill")
	runner.PriceResolver = fakePriceResolver{inPer1M: 10, outPer1M: 10, known: true}
	logCh := make(chan agent.LogEntry, 256)

	input := makeInput("cost_watchdog_kill")
	// Budget 25, 6 already spent by prior runs on this task -> only $19 of
	// headroom, well below the $20 this run's own usage will project.
	input.CostBudgetUSD = 25.00
	input.CostSpentUSD = 6.00
	input.CostWarnRatio = 0.8
	input.AgentConfig.TimeoutSecs = 10

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), input, logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()
	drainLogs(logCh)

	var res outcome
	select {
	case res = <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return — watchdog likely failed to cancel the subprocess")
	}

	var ce *agent.ErrCostBudgetExceeded
	if !errors.As(res.err, &ce) {
		t.Fatalf("want *agent.ErrCostBudgetExceeded, got err=%v (%T)", res.err, res.err)
	}
	// ce.SpentUSD is the task-wide projection (prior 6 + this run's 20 = 26).
	if ce.SpentUSD != 26.0 {
		t.Errorf("want ErrCostBudgetExceeded.SpentUSD=26.0 (prior 6 + this run's 20), got %v", ce.SpentUSD)
	}
	// res.r.CostUSD must be only this run's own incremental cost (26 - 6 =
	// 20), not the task-wide 26 -- SumTaskCost will separately add the prior
	// runs' own persisted 6.
	if res.r.CostUSD != 20.0 {
		t.Errorf("want Result.CostUSD=20.0 (this run's own cost, prior spend subtracted out), got %v", res.r.CostUSD)
	}
}

// TestClaude_OversizedLine_Truncation is a regression test for the bug where
// a single stdout/stderr line exceeding the scanner's buffer cap made the
// scanning goroutine exit silently (bufio.ErrTooLong from Scan()), dropping
// the rest of that stream with no log entry and leaving nothing draining the
// pipe — so a still-writing child could block on a full pipe and the run
// would only end at the outer timeout. Verifies the fix: the run surfaces a
// visible LogSystem warning, ends promptly (well under the test's 10s
// TimeoutSecs), and is NOT reported as "completed" despite the fake CLI
// itself exiting 0 with what looks like a normal success result line.
//
// runWithHelper can't be reused here because it t.Fatalf's on any non-nil
// error, and the truncation path intentionally returns a non-nil
// *agent.ErrTransient so the run counts against the bounded retry budget
// instead of silently reporting completed.
func TestClaude_OversizedLine_Truncation(t *testing.T) {
	runner := helperRunner("oversized_line")
	logCh := make(chan agent.LogEntry, 256)

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), makeInput("oversized_line"), logCh)
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
		t.Errorf("expected a LogSystem entry warning about the truncated output stream, got logs: %v", logContents(logs))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func logContents(logs []agent.LogEntry) []string {
	out := make([]string, len(logs))
	for i, l := range logs {
		out[i] = fmt.Sprintf("[%s] %s @ %s", l.Type, l.Content, l.At.Format(time.RFC3339))
	}
	return out
}

// findFlagValue returns the argument immediately following the given flag
// name in args, or "" if not found.
func findFlagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestBuildClaudeArgs_MaxTurnsDefault verifies that when AgentConfig.MaxTurns
// is unset (zero), the constructed args default --max-turns to 50 (today's
// previously-hardcoded behavior).
func TestBuildClaudeArgs_MaxTurnsDefault(t *testing.T) {
	args, err := buildClaudeArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}, false, nil)
	if err != nil {
		t.Fatalf("buildClaudeArgs: %v", err)
	}
	if got := findFlagValue(args, "--max-turns"); got != "50" {
		t.Fatalf("expected default --max-turns 50, got %q (args=%v)", got, args)
	}
}

// TestBuildClaudeArgs_MaxTurnsConfigured verifies that a custom
// AgentConfig.MaxTurns value is passed through to the --max-turns flag
// instead of the hardcoded default.
func TestBuildClaudeArgs_MaxTurnsConfigured(t *testing.T) {
	args, err := buildClaudeArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{MaxTurns: 12},
	}, false, nil)
	if err != nil {
		t.Fatalf("buildClaudeArgs: %v", err)
	}
	if got := findFlagValue(args, "--max-turns"); got != "12" {
		t.Fatalf("expected --max-turns 12, got %q (args=%v)", got, args)
	}
}

// TestBuildClaudeArgs_NoImageFlag verifies that buildClaudeArgs never emits
// an --image flag, even when the input carries attachment paths. The claude
// CLI has no --image flag and rejects it at argument parsing, so passing
// attachments this way must never regress (see docs/providers/claude.md §
// Image Attachments). Attachments reach the agent instead as files under
// .task_attachments/ in the worktree, listed in the prompt.
func TestBuildClaudeArgs_NoImageFlag(t *testing.T) {
	args, err := buildClaudeArgs(agent.RunInput{
		Task:               agent.Task{Title: "t"},
		AgentConfig:        agent.AgentConfig{},
		AttachmentAbsPaths: []string{"/tmp/uploads/abc/photo.png"},
	}, false, nil)
	if err != nil {
		t.Fatalf("buildClaudeArgs: %v", err)
	}
	for _, a := range args {
		if a == "--image" {
			t.Fatalf("buildClaudeArgs emitted unsupported --image flag: %v", args)
		}
	}
}

// TestBuildClaudeSettingsJSON_FallbackNoInventory verifies that a selected
// plugin is explicitly enabled even when it isn't present in the discovered
// inventory (or discovery finds nothing at all). HOME is pointed at an empty
// temp dir so this is deterministic regardless of the host's real
// ~/.claude/plugins/installed_plugins.json contents.
func TestBuildClaudeSettingsJSON_FallbackNoInventory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := buildClaudeSettingsJSON([]string{"some-plugin@marketplace"}, nil, nil)
	if err != nil {
		t.Fatalf("buildClaudeSettingsJSON: %v", err)
	}
	var parsed struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("unmarshal settings json: %v", err)
	}
	if !parsed.EnabledPlugins["some-plugin@marketplace"] {
		t.Fatalf("want selected plugin enabled, got %+v", parsed.EnabledPlugins)
	}
}

func TestBuildClaudeSettingsJSON_NoSelection_EmptyMap(t *testing.T) {
	// Isolate from the real user's ~/.claude/plugins/installed_plugins.json:
	// point HOME at an empty temp dir so plugin discovery finds nothing and
	// the fallback (empty map) path is exercised deterministically.
	t.Setenv("HOME", t.TempDir())
	got, err := buildClaudeSettingsJSON(nil, nil, nil)
	if err != nil {
		t.Fatalf("buildClaudeSettingsJSON: %v", err)
	}
	var parsed struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("unmarshal settings json: %v", err)
	}
	if len(parsed.EnabledPlugins) != 0 {
		t.Fatalf("want empty enabledPlugins map, got %+v", parsed.EnabledPlugins)
	}
}

// TestBuildClaudeSettingsJSON_CommandPermissions verifies that non-empty
// command allow/deny lists are translated into Bash(pattern) entries under
// the "permissions" key of the settings JSON, and that an empty pair of
// lists produces no "permissions" key at all (backward compatible).
func TestBuildClaudeSettingsJSON_CommandPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("both lists populated", func(t *testing.T) {
		got, err := buildClaudeSettingsJSON(nil, []string{"git *", "npm test"}, []string{"rm -rf *"})
		if err != nil {
			t.Fatalf("buildClaudeSettingsJSON: %v", err)
		}
		var parsed struct {
			Permissions struct {
				Allow []string `json:"allow"`
				Deny  []string `json:"deny"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("unmarshal settings json: %v", err)
		}
		wantAllow := []string{"Bash(git *)", "Bash(npm test)"}
		if len(parsed.Permissions.Allow) != len(wantAllow) {
			t.Fatalf("allow = %+v, want %+v", parsed.Permissions.Allow, wantAllow)
		}
		for i, w := range wantAllow {
			if parsed.Permissions.Allow[i] != w {
				t.Fatalf("allow[%d] = %q, want %q", i, parsed.Permissions.Allow[i], w)
			}
		}
		wantDeny := []string{"Bash(rm -rf *)"}
		if len(parsed.Permissions.Deny) != len(wantDeny) || parsed.Permissions.Deny[0] != wantDeny[0] {
			t.Fatalf("deny = %+v, want %+v", parsed.Permissions.Deny, wantDeny)
		}
	})

	t.Run("denylist only", func(t *testing.T) {
		got, err := buildClaudeSettingsJSON(nil, nil, []string{"sudo *"})
		if err != nil {
			t.Fatalf("buildClaudeSettingsJSON: %v", err)
		}
		var parsed struct {
			Permissions struct {
				Allow []string `json:"allow"`
				Deny  []string `json:"deny"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("unmarshal settings json: %v", err)
		}
		if len(parsed.Permissions.Allow) != 0 {
			t.Fatalf("expected no allow entries, got %+v", parsed.Permissions.Allow)
		}
		if len(parsed.Permissions.Deny) != 1 || parsed.Permissions.Deny[0] != "Bash(sudo *)" {
			t.Fatalf("deny = %+v, want [Bash(sudo *)]", parsed.Permissions.Deny)
		}
	})

	t.Run("empty lists omit permissions key entirely", func(t *testing.T) {
		got, err := buildClaudeSettingsJSON(nil, nil, nil)
		if err != nil {
			t.Fatalf("buildClaudeSettingsJSON: %v", err)
		}
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("unmarshal settings json: %v", err)
		}
		if _, ok := parsed["permissions"]; ok {
			t.Fatalf("expected no permissions key when both lists are empty, got %s", got)
		}
	})
}

// TestBuildClaudeArgs_CommandPermissions verifies buildClaudeArgs threads the
// agent config's command allow/deny lists through into the --settings JSON
// payload passed to the claude CLI.
func TestBuildClaudeArgs_CommandPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	args, err := buildClaudeArgs(agent.RunInput{
		Task: agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{
			CommandAllowlist: []string{"git *"},
			CommandDenylist:  []string{"rm -rf *"},
		},
	}, false, nil)
	if err != nil {
		t.Fatalf("buildClaudeArgs: %v", err)
	}
	settingsJSON := findFlagValue(args, "--settings")
	if settingsJSON == "" {
		t.Fatalf("expected --settings flag in args: %v", args)
	}
	if !strings.Contains(settingsJSON, `"Bash(git *)"`) {
		t.Fatalf("expected allow entry in settings JSON, got %s", settingsJSON)
	}
	if !strings.Contains(settingsJSON, `"Bash(rm -rf *)"`) {
		t.Fatalf("expected deny entry in settings JSON, got %s", settingsJSON)
	}
}

// TestBuildClaudeArgs_ResumeSession verifies that a resumed run passes
// --resume with the session id and sends the condensed resume prompt (the
// resumed conversation already contains the task context) instead of the full
// task prompt.
func TestBuildClaudeArgs_ResumeSession(t *testing.T) {
	reply := "use approach B"
	input := agent.RunInput{
		Task:            agent.Task{Title: "Fix the bug", Description: "long description"},
		AgentConfig:     agent.AgentConfig{},
		ResumeSessionID: "sess-1",
		HumanReply:      &reply,
	}
	args, err := buildClaudeArgs(input, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	resumeIdx := -1
	for i, a := range args {
		if a == "--resume" {
			resumeIdx = i
		}
	}
	if resumeIdx < 0 || resumeIdx+1 >= len(args) || args[resumeIdx+1] != "sess-1" {
		t.Fatalf("expected --resume sess-1 in args, got %v", args)
	}
	prompt := args[1] // "-p" value
	if !strings.Contains(prompt, "RESPONSE FROM HUMAN") || !strings.Contains(prompt, "use approach B") {
		t.Errorf("resume prompt should carry the human reply, got %q", prompt)
	}
	if strings.Contains(prompt, "Task: Fix the bug") {
		t.Errorf("resume prompt should not repeat the full task context, got %q", prompt)
	}
}

// TestBuildClaudeArgs_NoResumeFlagOnColdStart verifies --resume is absent for a
// cold run and the full task prompt is sent (including any human reply).
func TestBuildClaudeArgs_NoResumeFlagOnColdStart(t *testing.T) {
	reply := "use approach B"
	input := agent.RunInput{
		Task:        agent.Task{Title: "Fix the bug", Description: "long description"},
		AgentConfig: agent.AgentConfig{},
		HumanReply:  &reply,
	}
	args, err := buildClaudeArgs(input, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range args {
		if a == "--resume" {
			t.Fatalf("did not expect --resume in cold-start args: %v", args)
		}
	}
	prompt := args[1]
	if !strings.Contains(prompt, "RESPONSE FROM HUMAN") || !strings.Contains(prompt, "Task: Fix the bug") {
		t.Errorf("cold prompt should carry both the reply and the task, got %q", prompt)
	}
}
