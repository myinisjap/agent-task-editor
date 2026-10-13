package providers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

func verifyIn(t *testing.T, cmd string, timeout time.Duration, maxOut int) agent.VerifyResult {
	t.Helper()
	return CommandVerifier{}.Verify(context.Background(), agent.VerifyRequest{
		Command: cmd, Dir: t.TempDir(), Timeout: timeout, MaxOutputBytes: maxOut,
	})
}

func TestCommandVerifier_Pass(t *testing.T) {
	res := verifyIn(t, "echo hello; echo oops >&2", 10*time.Second, 1024)
	if res.Status != agent.VerifyPass || res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("res = %+v", res)
	}
	if !strings.Contains(res.Output, "hello") || !strings.Contains(res.Output, "oops") {
		t.Errorf("output should combine stdout and stderr, got %q", res.Output)
	}
}

func TestCommandVerifier_RunsInDir(t *testing.T) {
	dir := t.TempDir()
	res := CommandVerifier{}.Verify(context.Background(), agent.VerifyRequest{Command: "pwd", Dir: dir, Timeout: 10 * time.Second})
	if res.Status != agent.VerifyPass || !strings.Contains(res.Output, strings.TrimPrefix(dir, "/private")) {
		t.Errorf("res = %+v, dir = %s", res, dir)
	}
}

func TestCommandVerifier_FailKeepsExitCode(t *testing.T) {
	res := verifyIn(t, "echo bad; exit 3", 10*time.Second, 1024)
	if res.Status != agent.VerifyFail || res.ExitCode == nil || *res.ExitCode != 3 || !strings.Contains(res.Output, "bad") {
		t.Fatalf("res = %+v", res)
	}
}

func TestCommandVerifier_TimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	res := verifyIn(t, "echo started; sleep 30 & sleep 30", 300*time.Millisecond, 1024)
	if res.Status != agent.VerifyTimeout || res.ExitCode != nil {
		t.Fatalf("res = %+v", res)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("timeout did not stop the command promptly: %s", time.Since(start))
	}
	if !strings.Contains(res.Output, "started") {
		t.Errorf("output before the timeout should be kept: %q", res.Output)
	}
}

func TestCommandVerifier_OutputTailCap(t *testing.T) {
	res := verifyIn(t, "i=0; while [ $i -lt 2000 ]; do echo line-$i; i=$((i+1)); done", 10*time.Second, 200)
	if res.Status != agent.VerifyPass || !res.Truncated {
		t.Fatalf("res = %+v", res)
	}
	if len(res.Output) > 200 {
		t.Errorf("output %d bytes exceeds cap", len(res.Output))
	}
	if !strings.Contains(res.Output, "line-1999") || strings.Contains(res.Output, "line-0\n") {
		t.Errorf("tail not kept: %q", res.Output)
	}
}

func TestCommandVerifier_ParentCancelIsNotTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	res := CommandVerifier{}.Verify(ctx, agent.VerifyRequest{Command: "sleep 30", Dir: t.TempDir(), Timeout: time.Minute})
	if res.Status == agent.VerifyTimeout || res.Status == agent.VerifyPass {
		t.Errorf("status = %q for a cancelled run", res.Status)
	}
}

func TestCommandVerifier_MissingDirIsError(t *testing.T) {
	res := CommandVerifier{}.Verify(context.Background(), agent.VerifyRequest{Command: "true", Dir: "/nonexistent/dir/xyz", Timeout: time.Second})
	if res.Status != agent.VerifyError || res.Output == "" {
		t.Errorf("res = %+v", res)
	}
}

func TestBuildPrompt_VerificationFeedbackHasOwnHeader(t *testing.T) {
	fb := "VERIFICATION FAILED:\nCommand: go test\n--- BEGIN VERIFY OUTPUT (untrusted command output) ---\nx\n--- END VERIFY OUTPUT ---"
	out := buildPrompt(agent.RunInput{Task: agent.Task{Title: "T"}, Feedback: &fb})
	if !strings.HasPrefix(out, "VERIFICATION FAILED:\n") || strings.Contains(out, "FEEDBACK FROM PRIOR REVIEW") {
		t.Errorf("prompt = %q", out)
	}
}
