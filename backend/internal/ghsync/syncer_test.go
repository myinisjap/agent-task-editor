package ghsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
)

// fakeHub records every Publish call for assertions.
type fakeHub struct {
	calls []publishCall
}

type publishCall struct {
	eventType string
	payload   map[string]any
}

func (f *fakeHub) Publish(eventType string, payload map[string]any) {
	f.calls = append(f.calls, publishCall{eventType: eventType, payload: payload})
}

// initRepo creates a minimal git repo with one commit, mirroring
// internal/agent/worktree_test.go's helper of the same name (duplicated
// here to avoid depending on that package's unexported surface).
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "init")
	return dir
}

// gitWorktreeAdd creates a new branch + worktree off of the repo at
// repoPath, at wtPath.
func gitWorktreeAdd(t *testing.T, repoPath, branch, wtPath string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "worktree", "add", "-b", branch, wtPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
}

// branchExists reports whether the given local branch still exists in the
// repo at repoPath.
func branchExists(t *testing.T, repoPath, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "branch", "--list", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out)) != ""
}

// newTestSyncer sets up a Syncer backed by a temp sqlite DB and the given
// fake getPR function, bypassing New (which wires the real ghclient call).
// fakeWriteback records calls made through the writeback seam, for tests
// that want to assert ghsync fires the write-back hooks at the right times
// without shelling out to a real (or even faked) `gh` binary.
type fakeWriteback struct {
	labelCalls   []string
	commentCalls []string
	closeCalls   []string
}

func newTestSyncer(t *testing.T, getPR func(ctx context.Context, repoName, branch string) (string, string, int, error)) (*Syncer, *gen.Queries, *fakeHub) {
	t.Helper()
	s, q, hub, _ := newTestSyncerWithWriteback(t, getPR)
	return s, q, hub
}

// newTestSyncerWithWriteback is like newTestSyncer but also wires a
// writeback.Writeback backed by fake gh-calling functions, and returns the
// fake so tests can assert on what write-back actions fired.
func newTestSyncerWithWriteback(t *testing.T, getPR func(ctx context.Context, repoName, branch string) (string, string, int, error)) (*Syncer, *gen.Queries, *fakeHub, *fakeWriteback) {
	t.Helper()
	f, err := os.CreateTemp("", "ghsync-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	q := gen.New(db.SQL())
	hub := &fakeHub{}
	fwb := &fakeWriteback{}
	wb := writeback.NewWithClient(q,
		func(ctx context.Context, repoName string, issueNumber int, label string) error {
			fwb.labelCalls = append(fwb.labelCalls, label)
			return nil
		},
		func(ctx context.Context, repoName string, issueNumber int, body string) error {
			fwb.commentCalls = append(fwb.commentCalls, body)
			return nil
		},
		func(ctx context.Context, repoName string, issueNumber int, body string) error {
			fwb.closeCalls = append(fwb.closeCalls, body)
			return nil
		},
	)
	s := &Syncer{
		q:        q,
		hub:      hub,
		interval: time.Hour,
		wb:       wb,
		getPR:    getPR,
	}
	return s, q, hub, fwb
}

// newTestWorkflow seeds a minimal workflow with a single non-terminal label
// and returns (workflowID, labelName).
func newTestWorkflow(t *testing.T, q *gen.Queries) (string, string) {
	t.Helper()
	ctx := context.Background()
	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "Sync", Description: ""}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wfID, Name: "work", Color: "#000", SortOrder: 0, AgentIgnore: 0, IsTerminal: 0,
	}); err != nil {
		t.Fatalf("create label: %v", err)
	}
	return wfID, "work"
}

// newTestRepo seeds a repo row pointing at the given local path with the
// given remote URL (may be nil for a non-GitHub / no-remote repo).
func newTestRepo(t *testing.T, q *gen.Queries, wfID, path string, remoteURL *string) string {
	t.Helper()
	return newTestRepoWithWriteback(t, q, wfID, path, remoteURL, false)
}

// newTestRepoWithWriteback is like newTestRepo but lets the test opt the repo
// into issue write-back.
func newTestRepoWithWriteback(t *testing.T, q *gen.Queries, wfID, path string, remoteURL *string, writebackEnabled bool) string {
	t.Helper()
	ctx := context.Background()
	repoID := uuid.NewString()
	wb := int64(0)
	if writebackEnabled {
		wb = 1
	}
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:                    repoID,
		Name:                  "widgets",
		Path:                  path,
		RemoteUrl:             remoteURL,
		WorkflowID:            &wfID,
		IssueSyncEnabled:      0,
		IssueSyncLabel:        "",
		IssueWritebackEnabled: wb,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return repoID
}

// newTestTask seeds a task with the given branch/worktree/git state/pr url.
func newTestTask(t *testing.T, q *gen.Queries, repoID, wfID, label, branch, worktreePath, gitState, prURL string) gen.Task {
	t.Helper()
	ctx := context.Background()
	taskID := uuid.NewString()
	_, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID:          taskID,
		Title:       "Test task",
		Description: "",
		Type:        "feature",
		Label:       label,
		RepoID:      repoID,
		WorkflowID:  wfID,
		Attachments: "[]",
		Priority:    0,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{
		Branch:       branch,
		WorktreePath: worktreePath,
		BaseRef:      "main",
		ID:           taskID,
	}); err != nil {
		t.Fatalf("set task worktree: %v", err)
	}
	if gitState != "" || prURL != "" {
		if _, err := q.SetTaskPR(ctx, gen.SetTaskPRParams{
			GitState: gitState,
			PrUrl:    prURL,
			ID:       taskID,
		}); err != nil {
			t.Fatalf("set task pr: %v", err)
		}
	}
	task, err := q.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task
}

// newSourcedTestTask is like newTestTask but creates the task as if imported
// from GitHub (source="github", source_ref set), so write-back hooks apply.
func newSourcedTestTask(t *testing.T, q *gen.Queries, repoID, wfID, label, branch, worktreePath, gitState, prURL, sourceRef string) gen.Task {
	t.Helper()
	ctx := context.Background()
	taskID := uuid.NewString()
	_, err := q.CreateSourcedTask(ctx, gen.CreateSourcedTaskParams{
		ID:          taskID,
		Title:       "Test task",
		Description: "",
		Type:        "feature",
		Label:       label,
		RepoID:      repoID,
		WorkflowID:  wfID,
		Attachments: "[]",
		Source:      "github",
		SourceRef:   sourceRef,
	})
	if err != nil {
		t.Fatalf("create sourced task: %v", err)
	}
	if err := q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{
		Branch:       branch,
		WorktreePath: worktreePath,
		BaseRef:      "main",
		ID:           taskID,
	}); err != nil {
		t.Fatalf("set task worktree: %v", err)
	}
	if gitState != "" || prURL != "" {
		if _, err := q.SetTaskPR(ctx, gen.SetTaskPRParams{
			GitState: gitState,
			PrUrl:    prURL,
			ID:       taskID,
		}); err != nil {
			t.Fatalf("set task pr: %v", err)
		}
	}
	task, err := q.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task
}

func ghURL() *string {
	u := "https://github.com/acme/widgets"
	return &u
}

func TestSyncTask_MergedTriggersCleanup(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_merged", "https://github.com/acme/widgets/pull/1", 1, nil
	}
	s, q, hub := newTestSyncer(t, getPR)
	wfID, label := newTestWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, repoPath, ghURL())
	task := newTestTask(t, q, repoID, wfID, label, branch, wtPath, "pr_open", "")

	s.syncTask(ctx, task, repoInfo{ghName: "acme/widgets", path: repoPath})

	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.GitState != "pr_merged" {
		t.Errorf("git_state = %q, want pr_merged", updated.GitState)
	}
	if updated.PrUrl != "https://github.com/acme/widgets/pull/1" {
		t.Errorf("pr_url = %q, want the merged PR url", updated.PrUrl)
	}

	if len(hub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(hub.calls))
	}
	if hub.calls[0].eventType != "task.git_state_changed" {
		t.Errorf("event type = %q, want task.git_state_changed", hub.calls[0].eventType)
	}
	if hub.calls[0].payload["git_state"] != "pr_merged" {
		t.Errorf("payload git_state = %v, want pr_merged", hub.calls[0].payload["git_state"])
	}

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("expected worktree dir to be removed, stat err = %v", err)
	}
	if branchExists(t, repoPath, branch) {
		t.Errorf("expected local branch %q to be deleted", branch)
	}
}

func TestSyncTask_ClosedWithoutMerge_NoCleanup(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_closed", "https://github.com/acme/widgets/pull/2", 2, nil
	}
	s, q, hub := newTestSyncer(t, getPR)
	wfID, label := newTestWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, repoPath, ghURL())
	task := newTestTask(t, q, repoID, wfID, label, branch, wtPath, "pr_open", "")

	s.syncTask(ctx, task, repoInfo{ghName: "acme/widgets", path: repoPath})

	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.GitState != "pr_closed" {
		t.Errorf("git_state = %q, want pr_closed", updated.GitState)
	}
	if len(hub.calls) != 1 {
		t.Fatalf("expected 1 publish call, got %d", len(hub.calls))
	}

	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("expected worktree dir to still exist, stat err = %v", err)
	}
	if !branchExists(t, repoPath, branch) {
		t.Errorf("expected local branch %q to still exist", branch)
	}
}

func TestSyncTask_NoStateChange_NoOp(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/3", 3, nil
	}
	s, q, hub := newTestSyncer(t, getPR)
	wfID, label := newTestWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, repoPath, ghURL())
	task := newTestTask(t, q, repoID, wfID, label, branch, wtPath, "pr_open", "https://github.com/acme/widgets/pull/3")

	s.syncTask(ctx, task, repoInfo{ghName: "acme/widgets", path: repoPath})

	if len(hub.calls) != 0 {
		t.Fatalf("expected no publish calls on no-op sync, got %d", len(hub.calls))
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("expected worktree dir to still exist, stat err = %v", err)
	}
	if !branchExists(t, repoPath, branch) {
		t.Errorf("expected local branch %q to still exist", branch)
	}
}

func TestSyncTask_PreservesExistingPRURLOnRegression(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pushed", "", 0, nil
	}
	s, q, _ := newTestSyncer(t, getPR)
	wfID, label := newTestWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, repoPath, ghURL())
	task := newTestTask(t, q, repoID, wfID, label, branch, wtPath, "pr_open", "https://github.com/acme/widgets/pull/4")

	s.syncTask(ctx, task, repoInfo{ghName: "acme/widgets", path: repoPath})

	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.GitState != "pushed" {
		t.Errorf("git_state = %q, want pushed", updated.GitState)
	}
	if updated.PrUrl != "https://github.com/acme/widgets/pull/4" {
		t.Errorf("pr_url = %q, want the previously-stored url to be preserved", updated.PrUrl)
	}
}

func TestCleanupMergedBranch_NoWorktreePath(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)
	// Simulate the worktree already having been removed by the workflow
	// engine (e.g. via git worktree remove), leaving only the branch.
	if out, err := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", wtPath).CombinedOutput(); err != nil {
		t.Fatalf("pre-remove worktree: %v: %s", err, out)
	}

	s, q, _ := newTestSyncer(t, nil)
	wfID, label := newTestWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, repoPath, ghURL())
	task := newTestTask(t, q, repoID, wfID, label, branch, "", "pr_open", "")

	s.cleanupMergedBranch(ctx, task, repoPath)

	if branchExists(t, repoPath, branch) {
		t.Errorf("expected local branch %q to be deleted", branch)
	}
}

func TestSweep_SkipsNonGitHubRepo(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		t.Fatalf("getPR should not be called for a non-GitHub repo")
		return "", "", 0, nil
	}
	s, q, hub := newTestSyncer(t, getPR)
	wfID, label := newTestWorkflow(t, q)
	// No remote URL at all -> not a GitHub repo.
	repoID := newTestRepo(t, q, wfID, repoPath, nil)
	newTestTask(t, q, repoID, wfID, label, branch, wtPath, "", "")

	s.sweep(ctx)

	if len(hub.calls) != 0 {
		t.Fatalf("expected no publish calls, got %d", len(hub.calls))
	}
}

func TestSyncTask_Writeback_PROpened(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/9", 9, nil
	}
	s, q, _, fwb := newTestSyncerWithWriteback(t, getPR)
	wfID, label := newTestWorkflow(t, q)
	repoID := newTestRepoWithWriteback(t, q, wfID, repoPath, ghURL(), true)
	task := newSourcedTestTask(t, q, repoID, wfID, label, branch, wtPath, "pushed", "", "acme/widgets#9")
	repo := s.resolveRepoInfo(ctx, repoID)

	s.syncTask(ctx, task, repo)

	if len(fwb.commentCalls) != 1 {
		t.Fatalf("expected 1 PR-opened comment call, got %d: %v", len(fwb.commentCalls), fwb.commentCalls)
	}
	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackPrCommented == 0 {
		t.Error("expected writeback_pr_commented to be set")
	}
}

func TestSyncTask_Writeback_PRMerged(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_merged", "https://github.com/acme/widgets/pull/9", 9, nil
	}
	s, q, _, fwb := newTestSyncerWithWriteback(t, getPR)
	wfID, label := newTestWorkflow(t, q)
	repoID := newTestRepoWithWriteback(t, q, wfID, repoPath, ghURL(), true)
	task := newSourcedTestTask(t, q, repoID, wfID, label, branch, wtPath, "pr_open", "https://github.com/acme/widgets/pull/9", "acme/widgets#9")
	repo := s.resolveRepoInfo(ctx, repoID)

	s.syncTask(ctx, task, repo)

	if len(fwb.closeCalls) != 1 {
		t.Fatalf("expected 1 close-with-comment call, got %d: %v", len(fwb.closeCalls), fwb.closeCalls)
	}
	// The PR-opened comment flag should also already be set at this point
	// since the task had a PR URL before merging (it would have been marked on
	// an earlier sweep); OnPROpened is a no-op here because PrUrl was already
	// present when this sourced task was created, without going through
	// OnPROpened — so double check the important flag, writeback_closed.
	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.WritebackClosed == 0 {
		t.Error("expected writeback_closed to be set")
	}
}

func TestSyncTask_Writeback_DisabledRepo_NoOp(t *testing.T) {
	ctx := context.Background()
	repoPath := initRepo(t)
	branch := "feature-branch"
	wtPath := filepath.Join(t.TempDir(), "wt")
	gitWorktreeAdd(t, repoPath, branch, wtPath)

	getPR := func(ctx context.Context, repoName, br string) (string, string, int, error) {
		return "pr_open", "https://github.com/acme/widgets/pull/9", 9, nil
	}
	s, q, _, fwb := newTestSyncerWithWriteback(t, getPR)
	wfID, label := newTestWorkflow(t, q)
	// Write-back NOT enabled on this repo.
	repoID := newTestRepoWithWriteback(t, q, wfID, repoPath, ghURL(), false)
	task := newSourcedTestTask(t, q, repoID, wfID, label, branch, wtPath, "pushed", "", "acme/widgets#9")
	repo := s.resolveRepoInfo(ctx, repoID)

	s.syncTask(ctx, task, repo)

	if len(fwb.commentCalls) != 0 {
		t.Fatalf("expected no writeback calls when repo has writeback disabled, got %v", fwb.commentCalls)
	}
}
