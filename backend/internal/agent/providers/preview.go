package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/agentuser"
)

// PreviewCapturer implements agent.PreviewCapturer. The repo's preview command
// is untrusted repo code and is spawned exactly like the verify command: via
// newCLICommand (own process group, run as the agent user) with the provider
// allow-list environment (no server secrets). Chromium is driven by the
// bundled Playwright script at ScriptPath, also as the agent user.
type PreviewCapturer struct {
	ScriptPath string
	// pollInterval overrides the readiness poll period (tests).
	pollInterval time.Duration
}

const (
	previewServerStopGrace = 5 * time.Second
	previewPerShotTimeout  = 30 * time.Second
	previewCaptureCap      = 2 * time.Minute
)

// Capture implements agent.PreviewCapturer.
func (c PreviewCapturer) Capture(ctx context.Context, req agent.PreviewRequest) agent.PreviewResult {
	if _, err := os.Stat(c.ScriptPath); c.ScriptPath == "" || err != nil {
		return agent.PreviewResult{Err: errors.New("preview browser not installed (see docs/runtime.md#preview-screenshots)")}
	}
	port, err := freePort()
	if err != nil {
		return agent.PreviewResult{Err: fmt.Errorf("no free port: %w", err)}
	}

	env := providerBaseEnv(commonBaseEnvKeys)
	env = append(env, "PORT="+strconv.Itoa(port), "HOST=127.0.0.1", "BROWSER=none", "CI=true")
	bin, args, env, err := applyRuntime(req.Runtime, "sh", []string{"-c", sanitizeNUL(req.Command)}, env)
	if err != nil {
		return agent.PreviewResult{Err: err}
	}
	out := &tailBuffer{max: 4096}
	srv := newCLICommand(ctx, bin, args...)
	srv.Dir = req.Dir
	srv.Env = agentuser.Env(env)
	srv.Stdout, srv.Stderr = out, out
	if err := srv.Start(); err != nil {
		return agent.PreviewResult{Err: fmt.Errorf("preview command could not start: %w", err)}
	}
	exited := make(chan struct{})
	var waitErr error
	go func() { waitErr = srv.Wait(); close(exited) }()
	defer func() {
		stopProcessGroup(srv, previewServerStopGrace)
		select {
		case <-exited:
		case <-time.After(previewServerStopGrace + 6*time.Second):
		}
	}()

	timeout := req.StartTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	poll := c.pollInterval
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	if err := waitForPort(ctx, port, timeout, poll, exited, &waitErr, out); err != nil {
		return agent.PreviewResult{Err: err}
	}

	return c.runScript(ctx, req, port)
}

func (c PreviewCapturer) runScript(ctx context.Context, req agent.PreviewRequest, port int) agent.PreviewResult {
	routesJSON, _ := json.Marshal(req.Routes)
	vpJSON, _ := json.Marshal(req.Viewports)
	budget := time.Duration(len(req.Routes)*len(req.Viewports)) * previewPerShotTimeout
	if budget > previewCaptureCap || budget <= 0 {
		budget = previewCaptureCap
	}
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	env := providerBaseEnv(commonBaseEnvKeys)
	if v := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); v != "" {
		env = append(env, "PLAYWRIGHT_BROWSERS_PATH="+v)
	}
	cmd := newCLICommand(cctx, "node", c.ScriptPath,
		"--base", "http://127.0.0.1:"+strconv.Itoa(port),
		"--out", req.OutDir,
		"--routes-json", string(routesJSON),
		"--viewports-json", string(vpJSON))
	cmd.Env = agentuser.Env(env)
	var stdout, stderr tailBuffer
	stdout.max, stderr.max = 1<<20, 2048
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	raw, _ := stdout.result()

	var manifest []struct {
		Route  string `json:"route"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		File   string `json:"file"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		msg, _ := stderr.result()
		msg = lastLine(msg)
		if runErr != nil {
			if errors.Is(cctx.Err(), context.DeadlineExceeded) {
				msg = "capture timed out"
			} else if msg == "" {
				msg = runErr.Error()
			}
		}
		if msg == "" {
			msg = "capture script produced no output"
		}
		return agent.PreviewResult{Err: errors.New(msg)}
	}
	var res agent.PreviewResult
	for _, m := range manifest {
		res.Shots = append(res.Shots, agent.PreviewShot{Route: m.Route, Width: m.Width, Height: m.Height, File: m.File, Err: m.Error})
	}
	return res
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// waitForPort polls until something listens on port, the process exits, the
// timeout passes or ctx ends.
func waitForPort(ctx context.Context, port int, timeout, poll time.Duration, exited <-chan struct{}, waitErr *error, out *tailBuffer) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for {
		if conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-exited:
			err := *waitErr
			code := -1
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else if err == nil {
				code = 0
			}
			tail, _ := out.result()
			msg := fmt.Sprintf("preview command exited (code %d)", code)
			if l := lastLine(tail); l != "" {
				msg += ": " + l
			}
			return errors.New(msg)
		case <-deadline.C:
			return fmt.Errorf("port %d not listening after %s", port, timeout.Round(time.Second))
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// stopProcessGroup asks cmd's process group to terminate (SIGTERM), waits up
// to grace for the leader to be gone, then SIGKILLs the whole group. Servers
// that daemonize into a new session escape the group; that is documented.
func stopProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !groupAlive(pgid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// groupAlive reports whether any process remains in group pgid. A zombie
// leader (not yet reaped) still counts, so callers that Wait elsewhere should
// expect a short overlap; SIGKILL after the grace is harmless then.
func groupAlive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}
