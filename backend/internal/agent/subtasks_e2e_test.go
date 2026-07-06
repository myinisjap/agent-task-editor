package agent

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
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// fileWritingProvider writes a per-task file into the run's worktree (so the
// pool's safety-net commit produces real content to merge back), then completes
// with success. It's the only fake in the loop; dispatcher/pool/engine/
// coordinator/git all run for real.
type fileWritingProvider struct{}

func (fileWritingProvider) Run(_ context.Context, input RunInput, _ chan<- LogEntry) (Result, error) {
	name := "file-" + short8(input.Task.ID) + ".txt"
	_ = os.WriteFile(filepath.Join(input.RepoPath, name), []byte(input.Task.ID+"\n"), 0644)
	return Result{Status: "completed", Outcome: "success"}, nil
}

// subtaskHarness wires the full loop with the SubtaskCoordinator (mirroring
// cmd/server/main.go's OnTerminal branching).
type subtaskHarness struct {
	q     *gen.Queries
	coord *SubtaskCoordinator
	repo  string
}

func newSubtaskHarness(t *testing.T) *subtaskHarness {
	t.Helper()
	f, err := os.CreateTemp("", "subtask-e2e-*.db")
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
	pub := &recordingPub{}
	engine := workflow.New(db.SQL(), pub)
	coord := NewSubtaskCoordinator(q, engine, pub, "Test", "test@example.com")

	termQ := gen.New(db.SQL())
	engine.OnTerminal = func(ctx context.Context, task gen.Task) {
		repo, err := termQ.GetRepo(ctx, task.RepoID)
		if err != nil {
			return
		}
		if IsSubtask(task) {
			coord.OnChildTerminal(ctx, task, repo.Path)
			return
		}
		if task.WorktreePath != "" {
			_ = RemoveWorktree(ctx, repo.Path, task.WorktreePath)
		}
	}

	pool := NewPool(2, db.SQL(), engine, pub)
	pool.GitName, pool.GitEmail = "Test", "test@example.com"
	pool.Subtasks = coord

	d := NewDispatcher(db.SQL(), pool, engine, func(AgentConfig) Provider { return fileWritingProvider{} })
	d.interval = 15 * time.Millisecond
	d.Subtasks = coord

	ctx, cancel := context.WithCancel(context.Background())
	go pool.Start(ctx)
	go d.Run(ctx)
	t.Cleanup(cancel)

	return &subtaskHarness{q: q, coord: coord, repo: initRepo(t)}
}

// seedSubtaskWorkflow sets up a workflow where children and the parent occupy
// different labels so the auto-advance path is exercised deterministically:
//
//	children: work     --success--> done   (agent, terminal)   [agent config matches "work"]
//	parent:   assemble --success--> verify (agent)             [no agent config matches "assemble"]
//
// Because no agent config matches "assemble", the dispatcher can never pick the
// parent up while it's gated — so the *only* thing that can move it off
// "assemble" is the coordinator's auto-advance (assemble → verify), making the
// subtasks_complete transition deterministic to observe.
func seedSubtaskWorkflow(t *testing.T, q *gen.Queries) string {
	t.Helper()
	ctx := context.Background()
	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "Sub", Description: ""}); err != nil {
		t.Fatalf("create wf: %v", err)
	}
	labels := []struct {
		name         string
		order        int64
		ignore, term int64
	}{
		{"gate", 0, 1, 0},
		{"work", 1, 0, 0},
		{"done", 2, 0, 1},
		{"assemble", 3, 0, 0},
		{"verify", 4, 0, 0},
	}
	for _, l := range labels {
		if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID: uuid.NewString(), WorkflowID: wfID, Name: l.name, Color: "#000", SortOrder: l.order, AgentIgnore: l.ignore, IsTerminal: l.term,
		}); err != nil {
			t.Fatalf("label %s: %v", l.name, err)
		}
	}
	sp := func(s string) *string { return &s }
	trans := []struct{ from, to string }{{"work", "done"}, {"assemble", "verify"}}
	for _, tr := range trans {
		if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
			ID: uuid.NewString(), WorkflowID: wfID, FromLabel: tr.from, ToLabel: tr.to, TriggerType: "agent", Path: sp("success"),
		}); err != nil {
			t.Fatalf("transition %s→%s: %v", tr.from, tr.to, err)
		}
	}
	return wfID
}

func pollT(t *testing.T, q *gen.Queries, id string, cond func(gen.Task) bool, msg string) gen.Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		task, err := q.GetTask(context.Background(), id)
		if err == nil && cond(task) {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := q.GetTask(context.Background(), id)
	t.Fatalf("timed out waiting for %s: %s (label=%q merge=%q)", id, msg, task.Label, task.MergeStatus)
	return gen.Task{}
}

// TestE2E_SubtaskFlow drives the whole Mechanism 2 loop with real git: two
// children branch off the parent's branch, run to terminal, merge back
// concurrently, and the parent auto-advances once both are merged.
func TestE2E_SubtaskFlow(t *testing.T) {
	h := newSubtaskHarness(t)
	ctx := context.Background()
	wfID := seedSubtaskWorkflow(t, h.q)

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: h.repo, WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if _, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake", Provider: "fake", Model: "none",
		Labels: `["work"]`, Env: `{}`, MaxRetries: 1, RetryBackoffSecs: 1,
	}); err != nil {
		t.Fatalf("create config: %v", err)
	}

	// Parent starts gated (agent_ignore "gate") with its worktree provisioned (as
	// a planning run would leave it), so it isn't dispatched while we wire up the
	// children. It reaches "work" only after decomposition — mirroring the real
	// flow, where the parent is never dispatched concurrently with a child's
	// merge-back because its edges gate it until every child is terminal.
	parentID := uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{ID: parentID, Title: "parent", WorkflowID: wfID, RepoID: repoID, Label: "gate", Attachments: "[]"}); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	pWt, pBranch, pBase, err := provisionWorktree(ctx, h.repo, parentID, "parent")
	if err != nil {
		t.Fatalf("provision parent: %v", err)
	}
	_ = h.q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: pBranch, WorktreePath: pWt, BaseRef: pBase, ID: parentID})

	// Two children on "work", each with a parent→child dependency edge.
	var childIDs []string
	for i := 0; i < 2; i++ {
		cid := uuid.NewString()
		if _, err := h.q.CreateSubtask(ctx, gen.CreateSubtaskParams{
			ID: cid, Title: "child", Type: "feature", Label: "work", RepoID: repoID, WorkflowID: wfID, ParentTaskID: &parentID,
		}); err != nil {
			t.Fatalf("create child: %v", err)
		}
		if err := h.q.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{TaskID: parentID, DependsOnTaskID: cid}); err != nil {
			t.Fatalf("create edge: %v", err)
		}
		childIDs = append(childIDs, cid)
	}

	// Now move the parent onto "assemble"; no agent config matches that label, so
	// the dispatcher can't pick it up — only the coordinator's auto-advance can
	// move it, once both children are terminal and merged.
	if _, err := h.q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: "assemble", ID: parentID}); err != nil {
		t.Fatalf("move parent to assemble: %v", err)
	}

	// Both children reach "done" and merge back cleanly.
	for _, cid := range childIDs {
		c := pollT(t, h.q, cid, func(tk gen.Task) bool { return tk.Label == "done" && tk.MergeStatus == "merged" }, "child to reach done + merged")
		if c.MergeStatus != "merged" {
			t.Fatalf("child %s merge_status=%q", cid, c.MergeStatus)
		}
	}

	// Parent auto-advanced off "assemble" once both children merged
	// (assemble → verify via subtasks_complete). No agent config matches
	// "assemble", so only the auto-advance can have moved it.
	pollT(t, h.q, parentID, func(tk gen.Task) bool { return tk.Label == "verify" }, "parent to auto-advance assemble → verify")

	// The auto-advance is recorded with the distinct subtasks_complete trigger.
	if !hasHistoryTrigger(t, h.q, parentID, "subtasks_complete") {
		t.Fatalf("expected a subtasks_complete history entry on the parent")
	}

	// The parent's branch carries both children's files (merge-back landed).
	files := lsTree(t, h.repo, pBranch)
	for _, cid := range childIDs {
		want := "file-" + short8(cid) + ".txt"
		if !sliceHas(files, want) {
			t.Fatalf("parent branch %s missing merged child file %q; has %v", pBranch, want, files)
		}
	}
}

func hasHistoryTrigger(t *testing.T, q *gen.Queries, taskID, trigger string) bool {
	t.Helper()
	// Poll briefly since the auto-advance history write races the assertion.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := q.ListTaskLabelHistory(context.Background(), taskID)
		if err == nil {
			for _, r := range rows {
				if r.Trigger == trigger {
					return true
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func lsTree(t *testing.T, repo, branch string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "ls-tree", "-r", "--name-only", branch).CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-tree %s: %v: %s", branch, err, out)
	}
	return strings.Fields(strings.TrimSpace(string(out)))
}

func sliceHas(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
