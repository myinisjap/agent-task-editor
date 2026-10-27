package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// addWorkAgent registers an enabled agent config for the "work" label so the
// parent counts as agent-dispatchable.
func addWorkAgent(t *testing.T, q *gen.Queries) {
	t.Helper()
	ctx := context.Background()
	pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: uuid.NewString(), Name: "p", Provider: "fake", Model: "none", Env: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "w", ProviderConfigID: pc.ID, Labels: `["work"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	}); err != nil {
		t.Fatal(err)
	}
}

func commitChildFile(t *testing.T, child gen.Task, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(child.WorktreePath, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child.WorktreePath, name), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, child.WorktreePath, "add", "-A")
	gitIn(t, child.WorktreePath, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "child work")
}

func TestCoordinator_IntegrationPendingInsteadOfAutoAdvance(t *testing.T) {
	coord, q, repo, parent, child := setupCoordEnv(t)
	addWorkAgent(t, q)
	ctx := context.Background()
	if err := q.UpsertTaskFileScope(ctx, gen.UpsertTaskFileScopeParams{TaskID: child.ID, FileScope: "docs/**\nweb/**"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateTaskNotes(ctx, gen.UpdateTaskNotesParams{AgentNotes: strings.Repeat("n", 2000), ID: child.ID}); err != nil {
		t.Fatal(err)
	}
	commitChildFile(t, child, "docs/child.txt")
	child, _ = q.GetTask(ctx, child.ID)

	if got := coord.BuildIntegrationContext(ctx, parent.ID, "make test"); got != nil {
		t.Fatalf("integration context before pending should be nil")
	}
	coord.OnChildTerminal(ctx, child, repo)

	p, _ := q.GetTask(ctx, parent.ID)
	if p.Label != "work" {
		t.Fatalf("parent must stay on work, got %q", p.Label)
	}
	if s := coord.integrationStatus(ctx, parent.ID); s != "pending" {
		t.Fatalf("integration_status = %q, want pending", s)
	}
	rows, _ := q.ListTaskLabelHistory(ctx, parent.ID)
	for _, r := range rows {
		if r.Trigger == "subtasks_complete" {
			t.Fatalf("unexpected subtasks_complete transition")
		}
	}

	ic := coord.BuildIntegrationContext(ctx, parent.ID, "make test")
	if ic == nil {
		t.Fatal("expected integration context")
	}
	for _, want := range []string{"child", child.Branch, "docs/**, web/**", "…(truncated)", "`make test`", "docs/child.txt", "merged and deleted"} {
		if !strings.Contains(*ic, want) {
			t.Errorf("integration context missing %q:\n%s", want, *ic)
		}
	}

	// A failed integration run leaves it pending; a successful one completes it.
	coord.AfterParentRun(ctx, parent.ID, repo, true, false)
	if s := coord.integrationStatus(ctx, parent.ID); s != "pending" {
		t.Fatalf("after failed outcome status = %q, want pending", s)
	}
	coord.AfterParentRun(ctx, parent.ID, repo, true, true)
	if s := coord.integrationStatus(ctx, parent.ID); s != "done" {
		t.Fatalf("after success status = %q, want done", s)
	}
	if coord.BuildIntegrationContext(ctx, parent.ID, "") != nil {
		t.Fatalf("no context once done")
	}
}

func TestCoordinator_IntegrationIncludesConflicts(t *testing.T) {
	coord, q, repo, parent, child := setupCoordEnv(t)
	addWorkAgent(t, q)
	ctx := context.Background()
	for _, wt := range []string{parent.WorktreePath, child.WorktreePath} {
		if err := os.WriteFile(filepath.Join(wt, "clash.txt"), []byte(wt+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, wt, "add", "-A")
		gitIn(t, wt, "-c", "user.name=test", "-c", "user.email=t@example.com", "commit", "-m", "x")
	}
	coord.OnChildTerminal(ctx, child, repo)
	if s := coord.integrationStatus(ctx, parent.ID); s != "pending" {
		t.Fatalf("conflicts should still trigger integration pending, got %q", s)
	}
	ic := coord.BuildIntegrationContext(ctx, parent.ID, "")
	if ic == nil || !strings.Contains(*ic, "CONFLICT") || !strings.Contains(*ic, "git merge "+child.Branch) || !strings.Contains(*ic, "test/verify commands") {
		t.Fatalf("bad context: %v", ic)
	}
	// Outcome success while the conflict is unresolved must not complete it.
	// (The loop resolves conflicts on a successful run, so use a failed run.)
	coord.AfterParentRun(ctx, parent.ID, repo, false, false)
	if s := coord.integrationStatus(ctx, parent.ID); s != "pending" {
		t.Fatalf("status = %q", s)
	}
}

func TestCoordinator_EmptyChildBranchMarkedMerged(t *testing.T) {
	coord, q, repo, parent, child := setupCoordEnv(t)
	ctx := context.Background()
	if err := q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{ID: child.ID}); err != nil {
		t.Fatal(err)
	}
	child, _ = q.GetTask(ctx, child.ID)
	if child.Branch != "" {
		t.Fatalf("branch should be cleared")
	}
	parent, _ = q.GetTask(ctx, parent.ID)
	coord.mergeChild(ctx, repo, parent, child)
	got, _ := q.GetTask(ctx, child.ID)
	if got.MergeStatus != "merged" {
		t.Fatalf("merge_status = %q, want merged", got.MergeStatus)
	}
}

func TestPool_CheckFileScopeWarns(t *testing.T) {
	_, q, _, _, child := setupCoordEnv(t)
	ctx := context.Background()
	commitChildFile(t, child, "a/x")
	commitChildFile(t, child, "b/y")
	p := &Pool{q: q}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	job := Job{Input: RunInput{RepoPath: child.WorktreePath, Task: Task{ID: child.ID, ParentID: derefStr(child.ParentTaskID), FileScope: []string{"a/**"}}}}
	p.checkFileScope(ctx, job, "completed", log)
	got, _ := q.GetTask(ctx, child.ID)
	if !strings.Contains(got.AgentNotes, "FILE SCOPE WARNING") || !strings.Contains(got.AgentNotes, "- b/y") || strings.Contains(got.AgentNotes, "- a/x") {
		t.Fatalf("notes = %q", got.AgentNotes)
	}
	// No scope / not completed: no-op.
	before := got.AgentNotes
	p.checkFileScope(ctx, job, "failed", log)
	got, _ = q.GetTask(ctx, child.ID)
	if got.AgentNotes != before {
		t.Fatal("failed run should not append")
	}
}
