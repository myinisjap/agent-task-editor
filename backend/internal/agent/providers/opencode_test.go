package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// --- Subprocess lifecycle tests (generalized from claude_test.go's
// CLAUDE_TEST_HELPER re-exec harness — see TestMain in claude_test.go).
// opencode previously had 0% coverage of Run/binary — only classifyOpencodeJSON
// (parse_opencode_test.go) was tested directly. ---

func opencodeHelperInput(mode string) agent.RunInput {
	return agent.RunInput{
		RunID:       "opencode-test-run",
		Task:        agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{Env: map[string]string{"OPENCODE_TEST_HELPER": mode}, TimeoutSecs: 10},
		RepoPath:    os.TempDir(),
	}
}

func TestOpencodeRunner_Binary_DefaultsAndOverrides(t *testing.T) {
	var r OpencodeRunner
	if got := r.binary(); got != "opencode" {
		t.Errorf("binary() = %q, want default %q", got, "opencode")
	}
	r.BinaryPath = "/opt/opencode"
	if got := r.binary(); got != "/opt/opencode" {
		t.Errorf("binary() = %q, want override %q", got, "/opt/opencode")
	}
}

func TestOpencodeRunner_Run_Exit0Success(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), opencodeHelperInput("exit0_success"), logCh)
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
	if result.SessionID != "oc-1" {
		t.Errorf("SessionID = %q, want oc-1", result.SessionID)
	}
}

// TestOpencodeRunner_Run_UsageSumsAllStepFinish verifies the per-step-delta
// accumulation fix: when multiple step_finish events are emitted (one per
// step), the final Result's usage/cost must be the *sum* across every
// step_finish, not just the last one — see classifyOpencodeJSON's doc
// comment on why each step_finish's cost/tokens is a per-step delta.
func TestOpencodeRunner_Run_UsageSumsAllStepFinish(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), opencodeHelperInput("exit0_success_with_usage"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.InputTokens != 110 {
		t.Errorf("InputTokens = %d, want 110 (10 + 100, summed across both step_finish events)", result.InputTokens)
	}
	if result.OutputTokens != 220 {
		t.Errorf("OutputTokens = %d, want 220 (20 + 200, summed across both step_finish events)", result.OutputTokens)
	}
	if diff := result.CostUSD - 0.06; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("CostUSD = %v, want ~0.06 (0.01 + 0.05, summed across both step_finish events)", result.CostUSD)
	}
}

// TestOpencodeRunner_Run_SystemPromptReachesConfigContentEnv verifies the
// fix for the system prompt never being sent to opencode: Run must deliver
// buildSystemPrompt(input) — which carries the OUTCOME: success/failure
// instruction, opencode's only way to report an outcome since it has no
// MCP sidecar — to the CLI via OPENCODE_CONFIG_CONTENT. The fake binary
// (OPENCODE_TEST_HELPER=assert_outcome_in_config) fails unless it finds
// that instruction in its own OPENCODE_CONFIG_CONTENT env var.
func TestOpencodeRunner_Run_SystemPromptReachesConfigContentEnv(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	logs := make([]agent.LogEntry, 0)
	done := make(chan struct{})
	go func() {
		for e := range logCh {
			logs = append(logs, e)
		}
		close(done)
	}()
	result, err := runner.Run(context.Background(), opencodeHelperInput("assert_outcome_in_config"), logCh)
	close(logCh)
	<-done
	if err != nil {
		t.Fatalf("Run: unexpected error: %v (logs: %v)", err, logs)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", result.Outcome)
	}
}

// TestOpencodeConfigContent_CarriesSystemPromptOnBuildAgent verifies the
// OPENCODE_CONFIG_CONTENT JSON shape directly: the system prompt must land
// on the "build" agent's "prompt" field (the agent opencode run uses by
// default), since opencode run has no --system-prompt flag of its own.
func TestOpencodeConfigContent_CarriesSystemPromptOnBuildAgent(t *testing.T) {
	content, err := opencodeConfigContent("be careful.\n\nOUTCOME: success")
	if err != nil {
		t.Fatalf("opencodeConfigContent: unexpected error: %v", err)
	}
	var parsed struct {
		Agent map[string]struct {
			Prompt string `json:"prompt"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not valid JSON: %v\ncontent: %s", err, content)
	}
	build, ok := parsed.Agent["build"]
	if !ok {
		t.Fatalf("want an agent.build entry, got agents: %v", parsed.Agent)
	}
	if build.Prompt != "be careful.\n\nOUTCOME: success" {
		t.Errorf("agent.build.prompt = %q, want the full system prompt", build.Prompt)
	}
}

func TestOpencodeRunner_Run_Exit1NoOutputIsFailed(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), opencodeHelperInput("exit1"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestOpencodeRunner_Run_TimeoutKillsProcess verifies the context-cancel/
// timeout branch (shared by every CLI provider) actually kills a hung
// process and returns *agent.ErrTransient instead of hanging.
func TestOpencodeRunner_Run_TimeoutKillsProcess(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := opencodeHelperInput("")
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

// TestOpencodeRunner_Run_ModelAndResumeFlags exercises the args-building
// portion of Run indirectly is not possible (opencode.go inlines arg
// construction rather than exposing a buildOpencodeArgs helper like the
// other providers), so this smoke-tests that a Model/ResumeSessionID-bearing
// input doesn't panic or otherwise break the fake-binary run — the resulting
// args aren't independently inspectable without capturing the exec.Cmd, but
// the successful run + expected outcome is still a meaningful regression
// guard for this previously fully-untested provider.
func TestOpencodeRunner_Run_ModelAndResumeFlags(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := opencodeHelperInput("exit0_success")
	input.AgentConfig.Model = "some-model"
	input.ResumeSessionID = "sess-99"
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
}

// TestOpencodeRunner_Run_OversizedLineTruncation mirrors
// TestClaude_OversizedLine_Truncation in claude_test.go — proves the shared
// scanLines helper (scan.go) fixes the silent-truncation bug for opencode
// too: a stdout line exceeding the scan buffer cap must surface a visible
// LogSystem warning and end the run with a non-"completed" status, promptly
// (not wedged until the outer timeout).
func TestOpencodeRunner_Run_OversizedLineTruncation(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), opencodeHelperInput("oversized_line"), logCh)
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
