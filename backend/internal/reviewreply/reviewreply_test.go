package reviewreply

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type fakeForge struct {
	forge.Forge // unused methods panic if called
	name        string
	head        forge.PRHead
	replies     []string // "<commentID>: <body>"
	resolved    []string
	resolveErr  error
	replyErr    error
}

func (f *fakeForge) Name() string { return f.name }
func (f *fakeForge) PRHead(context.Context, string, string) (forge.PRHead, error) {
	return f.head, nil
}
func (f *fakeForge) ReplyToReviewComment(_ context.Context, _ string, _ int, id, body string) (string, error) {
	if f.replyErr != nil {
		return "", f.replyErr
	}
	f.replies = append(f.replies, id+": "+body)
	return "reply-" + id, nil
}
func (f *fakeForge) ResolveReviewThread(_ context.Context, _ string, _ int, id string) error {
	f.resolved = append(f.resolved, id)
	return f.resolveErr
}

type env struct {
	q    *gen.Queries
	repo gen.Repo
	task gen.Task
	run  string
	f    *fakeForge
	r    *Replier
}

func setup(t *testing.T, enabled, resolve int64, localHead string) *env {
	t.Helper()
	ctx := context.Background()
	file, err := os.CreateTemp("", "reviewreply-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	t.Cleanup(func() { _ = os.Remove(file.Name()) })
	db, err := storage.Open(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := gen.New(db.SQL())

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: uuid.NewString(), Name: "wf"})
	if err != nil {
		t.Fatal(err)
	}
	remote := "https://forge.test/acme/widgets"
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: uuid.NewString(), Name: "w", Path: t.TempDir(), RemoteUrl: &remote, WorkflowID: &wf.ID})
	if err != nil {
		t.Fatal(err)
	}
	repo, err = q.SetRepoReviewReplySettings(ctx, gen.SetRepoReviewReplySettingsParams{ReviewReplyEnabled: enabled, ReviewReplyResolveEnabled: resolve, ID: repo.ID})
	if err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", Type: "feature", Label: "not_ready", RepoID: repo.ID, WorkflowID: wf.ID, Attachments: "[]"}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: "feat", BaseRef: "main", ID: taskID}); err != nil {
		t.Fatal(err)
	}
	task, err := q.GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: run, TaskID: taskID}); err != nil {
		t.Fatal(err)
	}
	f := &fakeForge{name: "fake", head: forge.PRHead{Number: 4, HeadSHA: "abcdef1234567890", State: "pr_open"}}
	r := New(q)
	r.localHead = func(context.Context, string, string) (string, error) { return localHead, nil }
	r.forgeFor = func(string) (forge.Forge, string, bool) { return f, "acme/widgets", true }
	return &env{q: q, repo: repo, task: task, run: run, f: f, r: r}
}

// comment adds a comment; agentResolved marks it resolved by the env's run.
func (e *env) comment(t *testing.T, source, externalID string, agentResolved bool, note string) gen.TaskReviewComment {
	t.Helper()
	ctx := context.Background()
	var c gen.TaskReviewComment
	var err error
	if source == "local" {
		c, err = e.q.CreateTaskReviewComment(ctx, gen.CreateTaskReviewCommentParams{ID: uuid.NewString(), TaskID: e.task.ID, FilePath: "a.go", Side: "new", StartLine: 1, EndLine: 1, Body: "fix"})
	} else {
		c, err = e.q.CreateForgeTaskReviewComment(ctx, gen.CreateForgeTaskReviewCommentParams{ID: uuid.NewString(), TaskID: e.task.ID, FilePath: "a.go", Side: "new", StartLine: 1, EndLine: 1, Body: "fix", ExternalID: &externalID, Source: source})
	}
	if err != nil {
		t.Fatal(err)
	}
	params := gen.ResolveTaskReviewCommentParams{ResolutionNote: &note, ID: c.ID, TaskID: e.task.ID}
	if agentResolved {
		params.ResolvedByRunID = &e.run
	}
	if _, err := e.q.ResolveTaskReviewComment(ctx, params); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRun_RepliesOnlyForForgeSourcedAgentResolvedComments(t *testing.T) {
	e := setup(t, 1, 0, "abcdef1234567890")
	e.comment(t, "local", "", true, "local note")
	e.comment(t, "fake", "11", true, "fixed the nil check")
	e.comment(t, "fake", "12", false, "human resolved")
	e.comment(t, "other-forge", "13", true, "wrong forge")

	if err := e.r.Run(context.Background(), e.task); err != nil {
		t.Fatal(err)
	}
	if len(e.f.replies) != 1 || !strings.HasPrefix(e.f.replies[0], "11: "+Prefix+"fixed the nil check") {
		t.Fatalf("replies = %v", e.f.replies)
	}
	if !strings.Contains(e.f.replies[0], "`abcdef1`") || !strings.Contains(e.f.replies[0], Marker) {
		t.Errorf("reply lacks short sha or marker: %q", e.f.replies[0])
	}
	if len(e.f.resolved) != 0 {
		t.Errorf("thread resolved with the switch off: %v", e.f.resolved)
	}
}

func TestRun_NotPushedPostsNothingThenSucceedsAfterPush(t *testing.T) {
	e := setup(t, 1, 0, "localsha")
	e.comment(t, "fake", "11", true, "done")
	err := e.r.Run(context.Background(), e.task)
	if !errors.Is(err, ErrNotPushed) {
		t.Fatalf("err = %v, want ErrNotPushed", err)
	}
	if len(e.f.replies) != 0 {
		t.Fatalf("replied before push: %v", e.f.replies)
	}
	e.r.localHead = func(context.Context, string, string) (string, error) { return e.f.head.HeadSHA, nil }
	if err := e.r.Run(context.Background(), e.task); err != nil || len(e.f.replies) != 1 {
		t.Fatalf("after push: err=%v replies=%v", err, e.f.replies)
	}
}

func TestRun_Idempotent(t *testing.T) {
	e := setup(t, 1, 0, "abcdef1234567890")
	e.comment(t, "fake", "11", true, "done")
	for i := 0; i < 3; i++ {
		if err := e.r.Run(context.Background(), e.task); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.f.replies) != 1 {
		t.Fatalf("replies = %v, want exactly one", e.f.replies)
	}
}

func TestRun_DisabledDoesNothing(t *testing.T) {
	e := setup(t, 0, 0, "abcdef1234567890")
	e.comment(t, "fake", "11", true, "done")
	if err := e.r.Run(context.Background(), e.task); err != nil || len(e.f.replies) != 0 {
		t.Fatalf("err=%v replies=%v", err, e.f.replies)
	}
}

func TestRun_ResolveSwitchAndUnsupportedFallback(t *testing.T) {
	e := setup(t, 1, 1, "abcdef1234567890")
	e.comment(t, "fake", "11", true, "done")
	if err := e.r.Run(context.Background(), e.task); err != nil {
		t.Fatal(err)
	}
	if len(e.f.resolved) != 1 || e.f.resolved[0] != "11" {
		t.Fatalf("resolved = %v", e.f.resolved)
	}

	// A forge that cannot resolve threads (Gitea) just replies, without error.
	e2 := setup(t, 1, 1, "abcdef1234567890")
	e2.f.resolveErr = forge.ErrThreadResolveUnsupported
	e2.comment(t, "fake", "21", true, "done")
	if err := e2.r.Run(context.Background(), e2.task); err != nil || len(e2.f.replies) != 1 {
		t.Fatalf("err=%v replies=%v", err, e2.f.replies)
	}
}

func TestRun_ReplyFailureIsRetriedWithoutDoublePosting(t *testing.T) {
	e := setup(t, 1, 0, "abcdef1234567890")
	e.comment(t, "fake", "11", true, "a")
	e.comment(t, "fake", "12", true, "b")
	e.f.replyErr = errors.New("boom")
	if err := e.r.Run(context.Background(), e.task); err == nil {
		t.Fatal("expected error")
	}
	e.f.replyErr = nil
	if err := e.r.Run(context.Background(), e.task); err != nil || len(e.f.replies) != 2 {
		t.Fatalf("err=%v replies=%v", err, e.f.replies)
	}
}

func TestBody_CapsLength(t *testing.T) {
	b := Body(strings.Repeat("x", 5000), "abcdef1234")
	if strings.Count(b, "x") != MaxNoteRunes {
		t.Errorf("note not capped: %d x's", strings.Count(b, "x"))
	}
	if !strings.HasPrefix(b, Prefix) {
		t.Error("missing prefix")
	}
}

func TestIsOwnReply(t *testing.T) {
	e := setup(t, 1, 0, "abcdef1234567890")
	c := e.comment(t, "fake", "11", true, "done")
	if err := e.r.Run(context.Background(), e.task); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if !IsOwnReply(ctx, e.q, e.task.ID, forge.PRReviewComment{ID: "reply-11", Body: "x"}) {
		t.Error("recorded reply id not recognised")
	}
	if !IsOwnReply(ctx, e.q, e.task.ID, forge.PRReviewComment{ID: "zzz", Body: "hi " + Marker}) {
		t.Error("marker not recognised")
	}
	if IsOwnReply(ctx, e.q, e.task.ID, forge.PRReviewComment{ID: "11", Body: "human"}) {
		t.Error("human comment misclassified")
	}
	_ = c
}
