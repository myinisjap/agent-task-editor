package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestMatchLessonGlob(t *testing.T) {
	cases := []struct {
		pat, file string
		want      bool
	}{
		{"backend/**", "backend/x.go", true},
		{"backend/**", "backend/a/b/x.go", true},
		{"backend/**", "frontend/x.go", false},
		{"**/x.go", "x.go", true},
		{"**/x.go", "a/b/x.go", true},
		{"backend/**/x.go", "backend/x.go", true},
		{"backend/**/x.go", "backend/a/b/x.go", true},
		{"backend/*.go", "backend/x.go", true},
		{"backend/*.go", "backend/a/x.go", false},
		{"*.sql", "a/b/x.sql", true},
		{"*.sql", "x.sql", true},
		{"*.sql", "x.sqlx", false},
		{"frontend/", "frontend/src/a.ts", true},
		{"frontend/", "frontendx/a.ts", false},
		{"a?.go", "ab.go", true},
		{"a?.go", "a/.go", false},
		{"go.mod", "backend/go.mod", true},
		{"a.b", "axb", false},
	}
	for _, c := range cases {
		if got := matchLessonGlob(c.pat, c.file); got != c.want {
			t.Errorf("matchLessonGlob(%q,%q)=%v want %v", c.pat, c.file, got, c.want)
		}
	}
}

func repoLesson(id, repo, kind, body, globs, stages string, age int) gen.RepoLesson {
	l := gen.RepoLesson{ID: id, Kind: kind, Body: body, PathGlobs: globs, Stages: stages,
		CreatedAt: time.Unix(1_700_000_000+int64(age), 0)}
	if repo != "" {
		l.RepoID = &repo
	}
	return l
}

func ids(ls []gen.RepoLesson) string {
	var s []string
	for _, l := range ls {
		s = append(s, l.ID)
	}
	return strings.Join(s, ",")
}

func TestSelectLessons_StageGlobOrder(t *testing.T) {
	rows := []gen.RepoLesson{
		repoLesson("new", "r", "trap", "b", "", "", 50),
		repoLesson("old", "r", "trap", "b", "", "", 10),
		repoLesson("stage", "r", "trap", "b", "", "work,review", 5),
		repoLesson("other-stage", "r", "trap", "b", "", "review", 60),
		repoLesson("global", "", "trap", "b", "", "", 1),
		repoLesson("glob-hit", "r", "trap", "b", "backend/**", "", 70),
		repoLesson("glob-miss", "r", "trap", "b", "frontend/**", "", 80),
	}
	got := ids(selectLessons(rows, "work", []string{"backend/x.go"}))
	if want := "stage,global,glob-hit,new,old"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	// No changed files: lessons with globs are excluded.
	got = ids(selectLessons(rows, "work", nil))
	if strings.Contains(got, "glob") && strings.Contains(got, "glob-hit") {
		t.Fatalf("glob lesson leaked with no diff: %s", got)
	}
}

func TestSelectLessons_Budget(t *testing.T) {
	var rows []gen.RepoLesson
	for i := 0; i < 20; i++ {
		rows = append(rows, repoLesson(fmt.Sprintf("s%02d", i), "r", "trap", "short", "", "", i))
	}
	if n := len(selectLessons(rows, "work", nil)); n != 15 {
		t.Fatalf("got %d lessons want 15", n)
	}
	rows = nil
	long := strings.Repeat("x", 290)
	for i := 0; i < 10; i++ {
		rows = append(rows, repoLesson(fmt.Sprintf("l%02d", i), "r", "trap", long, "", "", i))
	}
	rows = append(rows, repoLesson("tiny", "r", "trap", "t", "", "", -100))
	sel := selectLessons(rows, "work", nil)
	total := 0
	for _, l := range sel {
		total += lessonLineLen(l)
	}
	if total > maxInjectedLessonChars {
		t.Fatalf("total %d exceeds budget", total)
	}
	if sel[len(sel)-1].ID != "tiny" {
		t.Fatalf("expected shorter later lesson to still fit: %s", ids(sel))
	}
}

func lessonFixture(t *testing.T) (*Dispatcher, *gen.Queries, gen.Task, gen.Task) {
	t.Helper()
	d, q := newRuntimeTestDispatcher(t)
	ctx := context.Background()
	wf := "wf-1"
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wf, Name: "wf", Description: "x"}); err != nil {
		t.Fatal(err)
	}
	var tasks []gen.Task
	for _, r := range []string{"A", "B"} {
		if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: r, Name: r, Path: "/tmp/" + r, WorkflowID: &wf}); err != nil {
			t.Fatal(err)
		}
		tk, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: "t" + r, Title: "t", RepoID: r, WorkflowID: wf, Label: "work"})
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, tk)
	}
	return d, q, tasks[0], tasks[1]
}

func addLesson(t *testing.T, q *gen.Queries, id string, repo *string, globs, stages, status string) {
	t.Helper()
	_, err := q.CreateRepoLesson(context.Background(), gen.CreateRepoLessonParams{
		ID: id, RepoID: repo, Kind: "trap", Body: "body " + id, PathGlobs: globs, Stages: stages,
		Citations: "[]", Source: "human", Status: status,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func idsOf(ls []RepoLesson) string {
	var s []string
	for _, l := range ls {
		s = append(s, l.ID)
	}
	return strings.Join(s, ",")
}

func TestLessonSelector_RepoStageIsolationAndCount(t *testing.T) {
	d, q, ta, tb := lessonFixture(t)
	ctx := context.Background()
	a, b := "A", "B"
	addLesson(t, q, "a-work", &a, "", "work", "active")
	addLesson(t, q, "a-review", &a, "", "review", "active")
	addLesson(t, q, "a-retired", &a, "", "", "retired")
	addLesson(t, q, "b-all", &b, "", "", "active")
	addLesson(t, q, "glob", nil, "", "", "active")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))

	got := d.lessonSelector(ta, log)(ctx, "", "", false)
	if g := idsOf(got); g != "a-work,glob" && g != "glob,a-work" {
		t.Fatalf("repo A got %s", g)
	}
	got = d.lessonSelector(tb, log)(ctx, "", "", false)
	if g := idsOf(got); g != "b-all,glob" && g != "glob,b-all" {
		t.Fatalf("repo B got %s", g)
	}
	count := func(id string) int64 {
		l, err := q.GetRepoLesson(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return l.InjectedCount
	}
	if count("a-work") != 1 || count("glob") != 2 || count("b-all") != 1 || count("a-review") != 0 || count("a-retired") != 0 {
		t.Fatalf("unexpected counts")
	}
	// Resuming: lessons returned but not counted.
	if got = d.lessonSelector(ta, log)(ctx, "", "", true); len(got) != 2 {
		t.Fatalf("resume got %d", len(got))
	}
	if count("a-work") != 1 {
		t.Fatalf("resume must not increment")
	}
}

func TestLessonSelector_GlobFilterWithRealDiff(t *testing.T) {
	d, q, ta, _ := lessonFixture(t)
	ctx := context.Background()
	a := "A"
	addLesson(t, q, "be", &a, "backend/**", "", "active")
	addLesson(t, q, "fe", &a, "frontend/**", "", "active")
	addLesson(t, q, "plain", &a, "", "", "active")
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))

	// No worktree/diff: only the glob-less lesson.
	if g := idsOf(d.lessonSelector(ta, log)(ctx, "", "", false)); g != "plain" {
		t.Fatalf("no diff got %s", g)
	}

	dir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "init")
	run("checkout", "-q", "-b", "task-branch")
	_ = os.MkdirAll(filepath.Join(dir, "backend"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "backend", "x.go"), []byte("package x"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "change")

	files, err := changedFilesAgainstBase(ctx, dir, "main", "task-branch")
	if err != nil || len(files) != 1 || files[0] != "backend/x.go" {
		t.Fatalf("files=%v err=%v", files, err)
	}
	ta.BaseRef = "main"
	g := idsOf(d.lessonSelector(ta, log)(ctx, dir, "task-branch", false))
	if g != "be,plain" && g != "plain,be" {
		t.Fatalf("with diff got %s", g)
	}
}
