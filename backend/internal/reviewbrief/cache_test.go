package reviewbrief

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestCache_Diff(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "T")
	must(t, os.WriteFile(dir+"/base.txt", []byte("base\n"), 0o644))
	git("add", ".")
	git("commit", "-m", "base")
	git("checkout", "-b", "feat")
	must(t, os.MkdirAll(dir+"/db/migrations", 0o755))
	must(t, os.WriteFile(dir+"/db/migrations/1.sql", []byte("create table x(a int);\n"), 0o644))
	git("add", ".")
	git("commit", "-m", "mig")

	now := time.Unix(1000, 0)
	c := NewCache()
	c.now = func() time.Time { return now }
	task := gen.Task{ID: "t1", Branch: "feat", BaseRef: "main", WorktreePath: dir}

	r, err := c.Diff(context.Background(), nil, task)
	if err != nil || !r.Available || r.Changed.Files != 1 || len(r.Flags) != 1 || r.Flags[0].Code != FlagMigration {
		t.Fatalf("first = %+v err=%v", r, err)
	}
	// New commit within the fresh window is not seen (no git call at all).
	must(t, os.WriteFile(dir+"/a_test.go", []byte("package a\n"), 0o644))
	git("add", ".")
	git("commit", "-m", "more")
	r2, _ := c.Diff(context.Background(), nil, task)
	if r2.Changed.Files != 1 {
		t.Fatalf("fresh window should reuse cache, files=%d", r2.Changed.Files)
	}
	// After the window, the head changed so it recomputes.
	now = now.Add(time.Minute)
	r3, _ := c.Diff(context.Background(), nil, task)
	if r3.Changed.Files != 2 {
		t.Fatalf("expected recompute, files=%d", r3.Changed.Files)
	}
	// Unchanged heads after the window: same result, still cached.
	now = now.Add(time.Minute)
	r4, _ := c.Diff(context.Background(), nil, task)
	if r4.Changed.Files != 2 {
		t.Fatalf("files=%d", r4.Changed.Files)
	}
}

func TestCache_Diff_Unavailable(t *testing.T) {
	c := NewCache()
	r, err := c.Diff(context.Background(), nil, gen.Task{ID: "x"})
	if err != nil || r.Available || r.Flags == nil {
		t.Fatalf("%+v %v", r, err)
	}
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Fatal(err)
	}

	r, err = c.Diff(context.Background(), nil, gen.Task{ID: "y", Branch: "nope", BaseRef: "main", WorktreePath: dir})
	if err != nil || r.Available {
		t.Fatalf("%+v %v", r, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
