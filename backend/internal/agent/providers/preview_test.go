//go:build linux

package providers

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

func TestStopProcessGroup_KillsChildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	cmd := newCLICommand(context.Background(), "sh", "-c", "sleep 300 & echo $! > "+pidFile+"; sleep 300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()

	var child int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(b)) != "" {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("child pid never written")
	}

	stopProcessGroup(cmd, 200*time.Millisecond)
	<-done

	deadline = time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if processGone(child) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(child) + "/stat")
	t.Fatalf("child %d survived stopProcessGroup: %s", child, b)
}

func fakeScript(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "capture.mjs")
	if err := os.WriteFile(p, []byte("// stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPreviewCapture_ScriptMissing(t *testing.T) {
	res := PreviewCapturer{ScriptPath: "/nonexistent/capture.mjs"}.Capture(context.Background(), agent.PreviewRequest{Command: "true"})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "browser not installed") {
		t.Errorf("err = %v", res.Err)
	}
}

func TestPreviewCapture_CommandExits(t *testing.T) {
	c := PreviewCapturer{ScriptPath: fakeScript(t), pollInterval: 20 * time.Millisecond}
	res := c.Capture(context.Background(), agent.PreviewRequest{Command: "echo boom >&2; exit 3", Dir: t.TempDir(), StartTimeout: 10 * time.Second})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "exited (code 3)") || !strings.Contains(res.Err.Error(), "boom") {
		t.Errorf("err = %v", res.Err)
	}
}

func TestPreviewCapture_NeverListens(t *testing.T) {
	c := PreviewCapturer{ScriptPath: fakeScript(t), pollInterval: 20 * time.Millisecond}
	start := time.Now()
	res := c.Capture(context.Background(), agent.PreviewRequest{Command: "sleep 300", Dir: t.TempDir(), StartTimeout: 300 * time.Millisecond})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "not listening") {
		t.Errorf("err = %v", res.Err)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

// A listening server whose capture step fails must still be torn down.
func TestPreviewCapture_ServerStoppedAfterCapture(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	// Bind $PORT with a tiny shell-less listener: use bash's /dev/tcp is
	// unavailable for listening, so fall back to python3 / nc when present.
	cmd := "echo $$ > " + pidFile + "; exec python3 -m http.server $PORT --bind 127.0.0.1"
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("python3 not available")
	}
	c := PreviewCapturer{ScriptPath: fakeScript(t), pollInterval: 20 * time.Millisecond}
	res := c.Capture(context.Background(), agent.PreviewRequest{
		Command: cmd, Dir: dir, StartTimeout: 15 * time.Second, Routes: []string{"/"},
		Viewports: []agent.Viewport{{Width: 100, Height: 100, Name: "x"}}, OutDir: dir,
	})
	// The stub script prints nothing, so capture fails; the point is cleanup.
	if res.Err == nil {
		t.Fatal("expected capture error from stub script")
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if processGone(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("preview server pid %d still running", pid)
}

// processGone reports whether pid no longer runs. A killed orphan can linger
// as a zombie until init reaps it (no init in containers), which still
// answers signal 0, so treat state Z as gone.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i >= 0 && i+2 < len(s) && s[i+2] == 'Z'
}
