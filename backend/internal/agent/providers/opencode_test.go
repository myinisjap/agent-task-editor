package providers

import (
	"context"
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
