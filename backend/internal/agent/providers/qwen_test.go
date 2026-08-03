package providers

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// TestBuildQwenArgs_Resume verifies ResumeSessionID is passed via --resume,
// and omitted when empty.
func TestBuildQwenArgs_Resume(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{Task: agent.Task{Title: "t"}, AgentConfig: agent.AgentConfig{}, ResumeSessionID: "s1"}, nil)
	if got := findFlagValue(args, "--resume"); got != "s1" {
		t.Fatalf("expected --resume s1, got %q (args=%v)", got, args)
	}
	none := buildQwenArgs(agent.RunInput{Task: agent.Task{Title: "t"}, AgentConfig: agent.AgentConfig{}}, nil)
	if findFlagValue(none, "--resume") != "" {
		t.Fatalf("did not expect --resume when ResumeSessionID empty, args=%v", none)
	}
}

// TestBuildQwenArgs_MaxTurnsDefault verifies that when AgentConfig.MaxTurns
// is unset (zero), the constructed args default --max-session-turns to 50
// (matching the claude provider's fallback behavior).
func TestBuildQwenArgs_MaxTurnsDefault(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}, nil)
	if got := findFlagValue(args, "--max-session-turns"); got != "50" {
		t.Fatalf("expected default --max-session-turns 50, got %q (args=%v)", got, args)
	}
}

// TestBuildQwenArgs_MaxTurnsConfigured verifies that a custom
// AgentConfig.MaxTurns value is passed through to the --max-session-turns
// flag instead of the hardcoded default.
func TestBuildQwenArgs_MaxTurnsConfigured(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{MaxTurns: 7},
	}, nil)
	if got := findFlagValue(args, "--max-session-turns"); got != "7" {
		t.Fatalf("expected --max-session-turns 7, got %q (args=%v)", got, args)
	}
}

// countFlagOccurrences counts how many times flag appears in args followed
// by exactly value (used since --allowed-tools may be repeated).
func countFlagOccurrences(args []string, flag, value string) int {
	n := 0
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			n++
		}
	}
	return n
}

// TestBuildQwenArgs_CommandAllowlist_NotWired verifies that CommandAllowlist
// is intentionally NOT translated into --allowed-tools entries (that flag
// only bypasses confirmation and is moot under --approval-mode yolo; see
// qwen.go and docs/providers/qwen_code.md).
func TestBuildQwenArgs_CommandAllowlist_NotWired(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task: agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{
			CommandAllowlist: []string{"git *", "npm test"},
		},
	}, nil)
	for i, a := range args {
		if a == "--allowed-tools" {
			t.Fatalf("expected no --allowed-tools flags from CommandAllowlist (unwired), found one at index %d in args=%v", i, args)
		}
	}
}

// TestBuildQwenArgs_CommandDenylist verifies that each CommandDenylist
// pattern is appended as a Bash(pattern) entry to --exclude-tools.
func TestBuildQwenArgs_CommandDenylist(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task: agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{
			CommandDenylist: []string{"git *", "npm test"},
		},
	}, nil)
	if countFlagOccurrences(args, "--exclude-tools", "Bash(git *)") != 1 {
		t.Fatalf("expected one --exclude-tools Bash(git *) entry, got args=%v", args)
	}
	if countFlagOccurrences(args, "--exclude-tools", "Bash(npm test)") != 1 {
		t.Fatalf("expected one --exclude-tools Bash(npm test) entry, got args=%v", args)
	}
}

// TestBuildQwenArgs_CommandDenylist_BlankPatternSkipped verifies that blank
// entries in CommandDenylist are skipped (mirrors the allowlist guard).
func TestBuildQwenArgs_CommandDenylist_BlankPatternSkipped(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task: agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{
			CommandDenylist: []string{"", "git *", ""},
		},
	}, nil)
	if countFlagOccurrences(args, "--exclude-tools", "Bash()") != 0 {
		t.Fatalf("expected blank denylist pattern to be skipped, got args=%v", args)
	}
	if countFlagOccurrences(args, "--exclude-tools", "Bash(git *)") != 1 {
		t.Fatalf("expected one --exclude-tools Bash(git *) entry, got args=%v", args)
	}
	n := 0
	for _, a := range args {
		if a == "--exclude-tools" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly one --exclude-tools flag total, got %d in args=%v", n, args)
	}
}

// TestBuildQwenArgs_NoCommandFilters_NoExtraFlags verifies that with no
// allowlist/denylist and no mcpCfg, neither --allowed-tools nor
// --exclude-tools appear.
func TestBuildQwenArgs_NoCommandFilters_NoExtraFlags(t *testing.T) {
	args := buildQwenArgs(agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}, nil)
	for i, a := range args {
		if a == "--allowed-tools" || a == "--exclude-tools" {
			t.Fatalf("expected no --allowed-tools/--exclude-tools flags without mcpCfg/filters, found %q at index %d in args=%v", a, i, args)
		}
	}
}

// TestQwenRunner_ErrorMaxTurns verifies that a subtype:"error_max_turns"
// stream-json result — which qwen, like claude, may emit with exit code 0 —
// drives Run to return *agent.ErrMaxTurns rather than reporting a normal
// "completed" result. Reuses the claude_test.go TestMain subprocess-helper
// pattern via a separate QWEN_TEST_HELPER env var.
func TestQwenRunner_ErrorMaxTurns(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)

	input := agent.RunInput{
		RunID: "qwen-test-run",
		Task:  agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{
			Env:         map[string]string{"QWEN_TEST_HELPER": "error_max_turns"},
			TimeoutSecs: 10,
			MaxTurns:    9,
		},
		RepoPath: os.TempDir(),
	}

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
	for range logCh {
	}
	res := <-ch

	var mt *agent.ErrMaxTurns
	if e, ok := res.err.(*agent.ErrMaxTurns); ok {
		mt = e
	}
	if mt == nil {
		t.Fatalf("want *agent.ErrMaxTurns, got err=%v (%T)", res.err, res.err)
	}
	if mt.MaxTurns != 9 {
		t.Errorf("want MaxTurns=9, got %d", mt.MaxTurns)
	}
	if res.r.Status != "failed" {
		t.Errorf("want Status=failed, got %q", res.r.Status)
	}
	if res.r.SessionID != "qwen-max-turns-session" {
		t.Errorf("want session id preserved, got %q", res.r.SessionID)
	}
	if res.r.InputTokens != 30 || res.r.OutputTokens != 40 {
		t.Errorf("want usage preserved (30/40), got (%d/%d)", res.r.InputTokens, res.r.OutputTokens)
	}
}

// TestQwenRunner_RateLimit_PreservesUsage is a regression test for the bug
// where qwen.go's rate-limit return path built a bare
// agent.Result{Status, CostWarned} — dropping SessionID and usage entirely —
// so a run that had already spent money before hitting its rate limit was
// persisted as free (cost_usd=0), defeating max_cost_usd budget accounting
// and the global daily/monthly cost-ceiling aggregates that sum
// agent_runs.cost_usd across every terminal status. Mirrors
// TestClaudeRunner_RateLimit_PreservesUsage in claude_test.go.
func TestQwenRunner_RateLimit_PreservesUsage(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), qwenHelperInput("rate_limit_with_usage"), logCh)
	close(logCh)

	var rl *agent.ErrRateLimit
	if e, ok := err.(*agent.ErrRateLimit); ok {
		rl = e
	}
	if rl == nil {
		t.Fatalf("want *agent.ErrRateLimit, got err=%v (%T)", err, err)
	}
	if result.Status != "failed" {
		t.Errorf("want Status=failed, got %q", result.Status)
	}
	if result.SessionID != "qwen-rate-limit-usage-session" {
		t.Errorf("want SessionID preserved, got %q", result.SessionID)
	}
	if result.InputTokens != 700 {
		t.Errorf("want InputTokens=700, got %d", result.InputTokens)
	}
	if result.OutputTokens != 350 {
		t.Errorf("want OutputTokens=350, got %d", result.OutputTokens)
	}
	if result.CostUSD != 3.5 {
		t.Errorf("want CostUSD=3.5, got %v", result.CostUSD)
	}
}

// TestQwenRunner_TransientError_PreservesUsage mirrors
// TestQwenRunner_RateLimit_PreservesUsage above for the transient-exit
// return path (a non-429 infra error, e.g. ECONNRESET) — same bug, same fix.
func TestQwenRunner_TransientError_PreservesUsage(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), qwenHelperInput("transient_with_usage"), logCh)
	close(logCh)

	var te *agent.ErrTransient
	if !errors.As(err, &te) {
		t.Fatalf("want *agent.ErrTransient, got err=%v (%T)", err, err)
	}
	if result.Status != "failed" {
		t.Errorf("want Status=failed, got %q", result.Status)
	}
	if result.SessionID != "qwen-transient-usage-session" {
		t.Errorf("want SessionID preserved, got %q", result.SessionID)
	}
	if result.InputTokens != 600 {
		t.Errorf("want InputTokens=600, got %d", result.InputTokens)
	}
	if result.OutputTokens != 120 {
		t.Errorf("want OutputTokens=120, got %d", result.OutputTokens)
	}
	if result.CostUSD != 1.9 {
		t.Errorf("want CostUSD=1.9, got %v", result.CostUSD)
	}
}

// TestQwenRunner_Timeout_PreservesUsage is a regression test for the bug
// where qwen.go's timeout return path (runCtx.Err() ==
// context.DeadlineExceeded) built a bare agent.Result{Status, CostWarned}
// without calling applyUsage — so a run that had already spent money before
// wedging and hitting its configured TimeoutSecs was persisted as free
// (cost_usd=0). The fake CLI here prints a terminal "result" event carrying
// usage/cost and then hangs well past the configured timeout, so it's still
// alive (and gets killed via context deadline) when TimeoutSecs elapses.
func TestQwenRunner_Timeout_PreservesUsage(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := qwenHelperInput("timeout_with_usage")
	input.AgentConfig.TimeoutSecs = 1

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

	var res outcome
	select {
	case res = <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for Run to return — configured TimeoutSecs likely failed to cancel the subprocess")
	}

	var te *agent.ErrTransient
	if !errors.As(res.err, &te) {
		t.Fatalf("want *agent.ErrTransient, got err=%v (%T)", res.err, res.err)
	}
	if res.r.Status != "failed" {
		t.Errorf("want Status=failed, got %q", res.r.Status)
	}
	if res.r.SessionID != "qwen-timeout-usage-session" {
		t.Errorf("want SessionID preserved, got %q", res.r.SessionID)
	}
	if res.r.InputTokens != 250 {
		t.Errorf("want InputTokens=250, got %d", res.r.InputTokens)
	}
	if res.r.OutputTokens != 75 {
		t.Errorf("want OutputTokens=75, got %d", res.r.OutputTokens)
	}
	if res.r.CostUSD != 1.25 {
		t.Errorf("want CostUSD=1.25, got %v", res.r.CostUSD)
	}
}

// TestQwenRunner_CostWatchdogKillsRun mirrors
// TestClaudeRunner_CostWatchdogKillsRun: verifies qwen's mid-run cost
// watchdog wiring (see qwen.go, cost_watchdog.go) cancels a subprocess whose
// projected cost crosses a tiny configured CostBudgetUSD, returning
// *agent.ErrCostBudgetExceeded rather than a plain failure.
func TestQwenRunner_CostWatchdogKillsRun(t *testing.T) {
	runner := &QwenRunner{
		BinaryPath:    os.Args[0],
		PriceResolver: fakePriceResolver{inPer1M: 10, outPer1M: 10, known: true},
	}
	logCh := make(chan agent.LogEntry, 256)

	input := qwenHelperInput("cost_watchdog_kill")
	input.CostBudgetUSD = 1.00
	input.CostWarnRatio = 0.8

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
	for range logCh {
	}

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
	if res.r.SessionID != "qwen-cost-watchdog-session" {
		t.Errorf("want session id preserved from the assistant message, got %q", res.r.SessionID)
	}
	// Regression coverage: a killed run never reaches its terminal "result"
	// event, so the persisted Result must come from the watchdog's own
	// cumulative-usage snapshot (costExceededUsage in qwen.go's Run), not
	// from applyUsage(nil) silently leaving everything at zero — otherwise
	// SumTaskCost (which the pre-dispatch budget guard reads) would
	// undercount a mid-run-killed run's real spend.
	if res.r.InputTokens != 1_000_000 {
		t.Errorf("want InputTokens=1,000,000 (from the assistant-message usage), got %d", res.r.InputTokens)
	}
	if res.r.OutputTokens != 1_000_000 {
		t.Errorf("want OutputTokens=1,000,000 (from the assistant-message usage), got %d", res.r.OutputTokens)
	}
	// $10/1M input + $10/1M output * 1M each = $20; CostSpentUSD defaults to
	// 0 in this test (qwenHelperInput doesn't set it), so this run's own
	// incremental cost equals the full projection.
	if res.r.CostUSD != 20.0 {
		t.Errorf("want CostUSD=20.0 (this run's own incremental cost, not left at 0), got %v", res.r.CostUSD)
	}
}

// TestQwenRunner_CostWatchdogKillsRun_SubtractsPriorSpend verifies that when
// a task already has prior recorded spend (input.CostSpentUSD > 0, e.g. from
// an earlier run on the same task), the killed run's own persisted CostUSD
// is only its *own* incremental cost — the watchdog's projected total minus
// the prior-spend baseline — not the full task-wide projection. Getting this
// wrong would double-count prior runs' cost every time SumTaskCost sums
// cost_usd across all of a task's runs.
func TestQwenRunner_CostWatchdogKillsRun_SubtractsPriorSpend(t *testing.T) {
	runner := &QwenRunner{
		BinaryPath:    os.Args[0],
		PriceResolver: fakePriceResolver{inPer1M: 10, outPer1M: 10, known: true},
	}
	logCh := make(chan agent.LogEntry, 256)

	input := qwenHelperInput("cost_watchdog_kill")
	// Budget 25, 6 already spent by prior runs on this task -> only $19 of
	// headroom, well below the $20 this run's own usage will project.
	input.CostBudgetUSD = 25.00
	input.CostSpentUSD = 6.00
	input.CostWarnRatio = 0.8

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
	for range logCh {
	}

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

// --- Subprocess lifecycle tests (generalized from claude_test.go's
// CLAUDE_TEST_HELPER re-exec harness — see TestMain in claude_test.go) ---

func qwenHelperInput(mode string) agent.RunInput {
	return agent.RunInput{
		RunID:       "qwen-test-run",
		Task:        agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{Env: map[string]string{"QWEN_TEST_HELPER": mode}, TimeoutSecs: 10},
		RepoPath:    os.TempDir(),
	}
}

func TestQwenRunner_Binary_DefaultsAndOverrides(t *testing.T) {
	var r QwenRunner
	if got := r.binary(); got != "qwen" {
		t.Errorf("binary() = %q, want default %q", got, "qwen")
	}
	r.BinaryPath = "/opt/qwen"
	if got := r.binary(); got != "/opt/qwen" {
		t.Errorf("binary() = %q, want override %q", got, "/opt/qwen")
	}
}

func TestQwenRunner_Run_Exit0Success(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), qwenHelperInput("exit0_success"), logCh)
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
	if result.InputTokens != 5 || result.OutputTokens != 7 {
		t.Errorf("usage = %d/%d, want 5/7", result.InputTokens, result.OutputTokens)
	}
}

func TestQwenRunner_Run_Exit1NoOutputIsFailed(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), qwenHelperInput("exit1_no_output"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestQwenRunner_Run_Exit1WithOutcome_OutcomeWins documents that, unlike
// claude's runner (which explicitly re-classifies a parsed success outcome
// as failed when the exit code is non-zero — see
// TestClaudeExitCode1_IsFailed), qwen.go has no such override: a non-empty
// parsed outcome wins over the exit code. This is a real, if surprising,
// behavioral difference between the two providers worth having pinned down
// by a test rather than left as tribal knowledge.
func TestQwenRunner_Run_Exit1WithOutcome_OutcomeWins(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), qwenHelperInput("exit1_with_outcome"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed (outcome wins over exit code for qwen)", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", result.Outcome)
	}
}

// TestQwenRunner_Run_TimeoutKillsProcess verifies the context-cancel/timeout
// branch (shared by every CLI provider) actually kills a hung process and
// returns *agent.ErrTransient instead of hanging.
func TestQwenRunner_Run_TimeoutKillsProcess(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := qwenHelperInput("")
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

// TestQwenRunner_Run_OversizedLineTruncation mirrors
// TestClaude_OversizedLine_Truncation in claude_test.go — proves the shared
// scanLines helper (scan.go) fixes the silent-truncation bug for qwen too: a
// stdout line exceeding the scan buffer cap must surface a visible LogSystem
// warning and end the run with a non-"completed" status, promptly (not
// wedged until the outer timeout).
func TestQwenRunner_Run_OversizedLineTruncation(t *testing.T) {
	runner := &QwenRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), qwenHelperInput("oversized_line"), logCh)
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
