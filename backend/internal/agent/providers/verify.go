package providers

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
)

// CommandVerifier implements agent.Verifier. The repo's verify command is
// untrusted repo code, so it is spawned exactly like an agent CLI: through
// newCLICommand (own process group killed on timeout, run as the unprivileged
// agent user via agentuser.Apply), with the allow-listed provider environment
// (no server secrets) and the repo's mise/uv runtime wrapper (applyRuntime).
type CommandVerifier struct{}

// Verify runs req.Command under `sh -c` in req.Dir and classifies the result.
func (CommandVerifier) Verify(ctx context.Context, req agent.VerifyRequest) agent.VerifyResult {
	start := time.Now()
	res := agent.VerifyResult{Status: agent.VerifyError}
	finish := func() agent.VerifyResult {
		res.Duration = time.Since(start)
		return res
	}

	maxOut := req.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = agent.VerifyMaxOutputBytes
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = agent.DefaultVerifyTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	env := providerBaseEnv(commonBaseEnvKeys)
	bin, args, env, err := applyRuntime(req.Runtime, "sh", []string{"-c", sanitizeNUL(req.Command)}, env)
	if err != nil {
		res.Output = err.Error()
		return finish()
	}
	cmd := newCLICommand(runCtx, bin, args...)
	cmd.Dir = req.Dir
	cmd.Env = agentuser.Env(env)
	out := &tailBuffer{max: maxOut}
	cmd.Stdout = out
	cmd.Stderr = out

	runErr := cmd.Run()
	res.Output, res.Truncated = out.result()
	switch {
	case runErr == nil:
		code := 0
		res.ExitCode = &code
		res.Status = agent.VerifyPass
	case errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		res.Status = agent.VerifyTimeout
	default:
		var ee *exec.ExitError
		if errors.As(runErr, &ee) && ee.ExitCode() >= 0 {
			code := ee.ExitCode()
			res.ExitCode = &code
			res.Status = agent.VerifyFail
		} else {
			// Could not start (or killed by a signal we did not send).
			if res.Output == "" {
				res.Output = runErr.Error()
			}
		}
	}
	return finish()
}

func sanitizeNUL(s string) string { return strings.ReplaceAll(s, "\x00", "") }

// tailBuffer is an io.Writer that keeps only the last max bytes written, so a
// chatty command cannot grow memory without bound.
type tailBuffer struct {
	mu      sync.Mutex
	max     int
	buf     []byte
	dropped bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
		t.dropped = true
	}
	return len(p), nil
}

// result returns the final tail as valid, NUL-free UTF-8 and whether earlier
// output was dropped.
func (t *tailBuffer) result() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	b, dropped := t.buf, t.dropped
	if len(b) > t.max {
		b = b[len(b)-t.max:]
		dropped = true
	}
	s := strings.ToValidUTF8(string(b), "")
	return strings.ReplaceAll(s, "\x00", ""), dropped
}
