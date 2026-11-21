package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
)

func newWakeDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	f, err := os.CreateTemp("", "dispatcher-wake-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	d := NewDispatcher(db.SQL(), &Pool{maxWorkers: 1}, nil, nil)
	d.interval = time.Hour // only wakes can trigger a sweep
	return d
}

func runDispatcher(t *testing.T, d *Dispatcher) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestDispatcher_WakeSweepsBeforeTick(t *testing.T) {
	d := newWakeDispatcher(t)
	runDispatcher(t, d)
	d.Wake()
	deadline := time.Now().Add(2 * time.Second)
	for d.SweepCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if d.SweepCount() < 1 {
		t.Fatal("Wake did not trigger a sweep within 2s on a 1h interval")
	}
}

func TestDispatcher_WakesCoalesceAndNeverBlock(t *testing.T) {
	d := newWakeDispatcher(t)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			d.Wake() // Run has not started: must not block
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wake blocked before Run started")
	}
	runDispatcher(t, d)
	deadline := time.Now().Add(2 * time.Second)
	for d.SweepCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if n := d.SweepCount(); n != 1 {
		t.Fatalf("100 coalesced wakes ran %d sweeps, want 1", n)
	}
}

func TestDispatcher_WakeOnZeroValueDoesNotBlock(t *testing.T) {
	d := &Dispatcher{}
	done := make(chan struct{})
	go func() { d.Wake(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wake blocked on a zero-value Dispatcher")
	}
}

func TestReusableVerification_DirtyTreeNeverReuses(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if out, err := gitCmd(dir, args...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(dir+"/a.txt", []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "i")
	if err := os.WriteFile(dir+"/a.txt", []byte("2"), 0o644); err != nil { // dirty tracked
		t.Fatal(err)
	}
	p := &Pool{Verifier: nopVerifier{}} // q is nil: a dirty tree must short-circuit before any query
	var job Job
	job.Input.RepoPath = dir
	sha, err := worktreeHead(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.reusableVerification(context.Background(), job, dir, sha, "cmd"); ok {
		t.Fatal("dirty tree must not reuse")
	}
}

type nopVerifier struct{}

func (nopVerifier) Verify(context.Context, VerifyRequest) VerifyResult { return VerifyResult{} }

func gitCmd(dir string, args ...string) ([]byte, error) {
	return gitcmd.Output(context.Background(), dir, args...)
}
