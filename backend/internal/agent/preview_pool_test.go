package agent_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// 1x1 PNG.
var tinyPNG = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89}

type fakePreviewer struct {
	err   error
	notPN bool

	mu    sync.Mutex
	calls []agent.PreviewRequest
}

func (f *fakePreviewer) Capture(_ context.Context, req agent.PreviewRequest) agent.PreviewResult {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	if f.err != nil {
		return agent.PreviewResult{Err: f.err}
	}
	var res agent.PreviewResult
	for i, r := range req.Routes {
		for _, vp := range req.Viewports {
			name := filepath.Join(req.OutDir, string(rune('a'+i))+"-"+vp.Name+".png")
			data := tinyPNG
			if f.notPN {
				data = []byte("not a png")
			}
			_ = os.WriteFile(name, data, 0o644)
			res.Shots = append(res.Shots, agent.PreviewShot{Route: r, Width: vp.Width, Height: vp.Height, File: filepath.Base(name)})
		}
	}
	return res
}

func (f *fakePreviewer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

type previewFixture struct {
	*verifyFixture
	prev      *fakePreviewer
	uploadDir string
}

func newPreviewFixture(t *testing.T, previewCmd, changedFile string, verify agent.Verifier, verifyCmd string) *previewFixture {
	t.Helper()
	f := newVerifyFixture(t, "agent-review", verifyCmd, verify)
	dir := f.repo
	gitIn(t, dir, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, changedFile)), 0o755)
	_ = os.WriteFile(filepath.Join(dir, changedFile), []byte("y"), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "change")
	if err := f.q.SetTaskWorktree(context.Background(), gen.SetTaskWorktreeParams{ID: f.taskID, Branch: "feature", WorktreePath: dir, BaseRef: "main"}); err != nil {
		t.Fatal(err)
	}
	if previewCmd != "" {
		if _, err := f.db.Exec("UPDATE repos SET preview_command=?, preview_routes='/\n/tasks' WHERE id=(SELECT repo_id FROM tasks WHERE id=?)", previewCmd, f.taskID); err != nil {
			t.Fatal(err)
		}
	}
	pf := &previewFixture{verifyFixture: f, prev: &fakePreviewer{}, uploadDir: t.TempDir()}
	f.pool.Previewer = pf.prev
	f.pool.UploadDir = pf.uploadDir
	return pf
}

func (f *previewFixture) waitCapture(want string) gen.TaskPreviewCapture {
	f.t.Helper()
	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		c, err := f.q.GetLatestPreviewCapture(context.Background(), f.taskID)
		if err == nil && c.Status == want {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, _ := f.q.GetLatestPreviewCapture(context.Background(), f.taskID)
	f.t.Fatalf("capture never reached %q; have %+v", want, c)
	return c
}

func TestPreview_UnconfiguredIsNoop(t *testing.T) {
	f := newPreviewFixture(t, "", "frontend/App.tsx", nil, "")
	f.run("agent-review", "success")
	f.waitSettled(func(task gen.Task) bool { return task.Label == "review" })
	time.Sleep(100 * time.Millisecond)
	if f.prev.callCount() != 0 {
		t.Error("previewer called without preview_command")
	}
	if _, err := f.q.GetLatestPreviewCapture(context.Background(), f.taskID); err == nil {
		t.Error("capture row created without preview_command")
	}
}

func TestPreview_NoUIChangeIsNoop(t *testing.T) {
	f := newPreviewFixture(t, "npm run dev", "backend/main.go", nil, "")
	f.run("agent-review", "success")
	f.waitSettled(func(task gen.Task) bool { return task.Label == "review" })
	time.Sleep(100 * time.Millisecond)
	if f.prev.callCount() != 0 {
		t.Error("previewer called for a non-UI diff")
	}
}

func TestPreview_CapturesAndStoresImages(t *testing.T) {
	f := newPreviewFixture(t, "npm run dev", "frontend/App.tsx", nil, "")
	f.run("agent-review", "success")
	c := f.waitCapture("ok")
	f.waitSettled(func(task gen.Task) bool { return task.Label == "review" })
	if f.prev.callCount() != 1 {
		t.Fatalf("calls = %d", f.prev.callCount())
	}
	req := f.prev.calls[0]
	if req.Command != "npm run dev" || req.Dir != f.repo || len(req.Routes) != 2 || len(req.Viewports) != 2 {
		t.Errorf("request = %+v", req)
	}
	imgs, _ := f.q.ListPreviewImagesByCapture(context.Background(), c.ID)
	if len(imgs) != 4 {
		t.Fatalf("images = %d, want 4", len(imgs))
	}
	for _, im := range imgs {
		if _, err := os.Stat(filepath.Join(f.uploadDir, im.Path)); err != nil {
			t.Errorf("image file missing: %v", err)
		}
	}
	if _, err := os.Stat(req.OutDir); !os.IsNotExist(err) {
		t.Errorf("temp out dir not cleaned up: %v", err)
	}
}

func TestPreview_FailureNeverBlocksTask(t *testing.T) {
	f := newPreviewFixture(t, "npm run dev", "frontend/App.tsx", nil, "")
	f.prev.err = errors.New("port 1 not listening after 120s")
	f.run("agent-review", "success")
	c := f.waitCapture("failed")
	if c.Error != "port 1 not listening after 120s" {
		t.Errorf("error = %q", c.Error)
	}
	task := f.waitSettled(func(task gen.Task) bool { return task.Label == "review" })
	if task.ActiveAgentRunID != nil {
		t.Errorf("task still locked: %+v", task.ActiveAgentRunID)
	}
}

func TestPreview_RejectsNonPNG(t *testing.T) {
	f := newPreviewFixture(t, "npm run dev", "frontend/App.tsx", nil, "")
	f.prev.notPN = true
	f.run("agent-review", "success")
	f.waitCapture("failed")
}

func TestPreview_NotCalledWhenVerifyFails(t *testing.T) {
	v := &fakeVerifier{res: agent.VerifyResult{Status: agent.VerifyFail, ExitCode: intPtr(1), Output: "boom"}}
	f := newPreviewFixture(t, "npm run dev", "frontend/App.tsx", v, "make test")
	f.run("agent-review", "success")
	f.waitSettled(func(task gen.Task) bool { return task.Label == "work" })
	time.Sleep(100 * time.Millisecond)
	if f.prev.callCount() != 0 {
		t.Error("previewer called after failed verification")
	}
}

func TestPreview_NotCalledOnFailedRun(t *testing.T) {
	f := newPreviewFixture(t, "npm run dev", "frontend/App.tsx", nil, "")
	f.run("agent-review", "failure")
	f.waitSettled(func(task gen.Task) bool { return task.Label == "work" })
	time.Sleep(100 * time.Millisecond)
	if f.prev.callCount() != 0 {
		t.Error("previewer called for a failed run")
	}
}
