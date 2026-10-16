package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// TestOpencodeRunner_Run_ModelAndResumeFlags is a subprocess smoke test for
// a Model/ResumeSessionID-bearing input: the actual arg construction is now
// covered directly (and much more precisely) by the buildOpencodeArgs unit
// tests below, which don't need a subprocess at all. This test remains as a
// regression guard that such an input doesn't panic or otherwise break the
// fake-binary run end-to-end.
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

// --- buildOpencodeArgs unit tests (no subprocess needed) ---

// TestBuildOpencodeArgs_BaseFlagsAndSeparator verifies the always-present
// `run --format json` flags, that "--" is present, and that the prompt is
// the very last argument — the guard against -f (a variadic/array-type
// yargs option) swallowing the prompt as an extra file.
func TestBuildOpencodeArgs_BaseFlagsAndSeparator(t *testing.T) {
	input := agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}
	args := buildOpencodeArgs(input)
	if len(args) < 3 || args[0] != "run" || args[1] != "--format" || args[2] != "json" {
		t.Fatalf("expected leading `run --format json`, got %v", args)
	}
	sepIdx := slices.Index(args, "--")
	if sepIdx < 0 {
		t.Fatalf("expected a -- separator before the prompt, args=%v", args)
	}
	if args[len(args)-1] != buildPrompt(input) {
		t.Fatalf("expected the prompt as the final argument, got %v", args)
	}
	if sepIdx != len(args)-2 {
		t.Fatalf("expected -- to immediately precede the prompt, args=%v", args)
	}
}

// TestBuildOpencodeArgs_ModelAndResume verifies -m/--session are present
// when set and absent when unset.
func TestBuildOpencodeArgs_ModelAndResume(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		args := buildOpencodeArgs(agent.RunInput{
			Task:            agent.Task{Title: "t"},
			AgentConfig:     agent.AgentConfig{Model: "some-model"},
			ResumeSessionID: "sess-99",
		})
		if got := findFlagValue(args, "-m"); got != "some-model" {
			t.Fatalf("expected -m some-model, got %q (args=%v)", got, args)
		}
		if got := findFlagValue(args, "--session"); got != "sess-99" {
			t.Fatalf("expected --session sess-99, got %q (args=%v)", got, args)
		}
	})
	t.Run("unset", func(t *testing.T) {
		args := buildOpencodeArgs(agent.RunInput{
			Task:        agent.Task{Title: "t"},
			AgentConfig: agent.AgentConfig{},
		})
		if containsArg(args, "-m") {
			t.Fatalf("did not expect -m flag when Model is unset, args=%v", args)
		}
		if containsArg(args, "--session") {
			t.Fatalf("did not expect --session flag when ResumeSessionID is unset, args=%v", args)
		}
	})
}

// TestBuildOpencodeArgs_Variant verifies AgentConfig.Effort maps to
// --variant unchanged (pass-through, no clamping — see effort.go), and is
// omitted when unset or unrecognized.
func TestBuildOpencodeArgs_Variant(t *testing.T) {
	t.Run("unset omits --variant", func(t *testing.T) {
		args := buildOpencodeArgs(agent.RunInput{
			Task:        agent.Task{Title: "t"},
			AgentConfig: agent.AgentConfig{},
		})
		if containsArg(args, "--variant") {
			t.Fatalf("did not expect --variant flag when Effort is unset, args=%v", args)
		}
	})

	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		t.Run(level, func(t *testing.T) {
			args := buildOpencodeArgs(agent.RunInput{
				Task:        agent.Task{Title: "t"},
				AgentConfig: agent.AgentConfig{Effort: level},
			})
			if got := findFlagValue(args, "--variant"); got != level {
				t.Fatalf("expected --variant %s, got %q (args=%v)", level, got, args)
			}
		})
	}

	t.Run("unrecognized value omits --variant", func(t *testing.T) {
		args := buildOpencodeArgs(agent.RunInput{
			Task:        agent.Task{Title: "t"},
			AgentConfig: agent.AgentConfig{Effort: "bogus"},
		})
		if containsArg(args, "--variant") {
			t.Fatalf("did not expect --variant flag for an unrecognized Effort value, args=%v", args)
		}
	})
}

// TestBuildOpencodeArgs_Files verifies task attachments copied into the
// worktree's .task_attachments/ are passed as one -f <path> per file,
// de-duplicated, missing copies skipped, and that the prompt stays the
// final argument (not swallowed as a file by the variadic -f).
func TestBuildOpencodeArgs_Files(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".task_attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.png", "b.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, ".task_attachments", f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	input := agent.RunInput{
		Task: agent.Task{
			Title:       "t",
			Attachments: []string{"task1/a.png", "task1/missing.png", "task1/b.jpg", "task1/a.png"},
		},
		AgentConfig: agent.AgentConfig{},
		RepoPath:    dir,
	}
	args := buildOpencodeArgs(input)

	var files []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-f" && i+1 < len(args) {
			files = append(files, args[i+1])
		}
	}
	want := []string{".task_attachments/a.png", ".task_attachments/b.jpg"}
	if !slices.Equal(files, want) {
		t.Fatalf("file args = %v, want %v (all args %v)", files, want, args)
	}
	if args[len(args)-1] != buildPrompt(input) {
		t.Fatalf("expected prompt as final arg (not swallowed by -f), args=%v", args)
	}
	if strings.HasPrefix(args[len(args)-1], ".task_attachments") {
		t.Fatalf("expected prompt as final arg, args=%v", args)
	}
}

// TestBuildOpencodeArgs_NoAttachmentsNoFileFlag verifies no -f flag is
// added when there are no attachments.
func TestBuildOpencodeArgs_NoAttachmentsNoFileFlag(t *testing.T) {
	args := buildOpencodeArgs(agent.RunInput{Task: agent.Task{Title: "t"}, RepoPath: t.TempDir()})
	if containsArg(args, "-f") {
		t.Fatalf("did not expect a -f arg without attachments, args=%v", args)
	}
}
