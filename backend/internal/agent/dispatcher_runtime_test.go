package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// errFakePrep stands in for a real runtime.Prep failure (e.g. mise install
// exiting non-zero) so escalation-path tests don't depend on mise/uv being
// installed in the test environment.
var errFakePrep = errors.New("fake prep failure: mise exited 1")

// newRuntimeTestDispatcher opens a fresh sqlite DB and returns a Dispatcher
// wired against it, plus the raw *gen.Queries for seeding — same pattern as
// TestDispatcher_ResolveAgentConfig_ResumeByProvider in
// dispatcher_internal_test.go.
func newRuntimeTestDispatcher(t *testing.T) (*Dispatcher, *gen.Queries) {
	t.Helper()
	f, err := os.CreateTemp("", "dispatcher-runtime-*.db")
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
	d := NewDispatcher(db.SQL(), &Pool{maxWorkers: 1}, nil, nil)
	return d, q
}

// TestPrepareRuntime_EmptyColumnIsPassthrough is the §4.1 byte-identical
// regression guard: a repo whose runtime_languages column is empty (the
// default, and the overwhelmingly common case today) must get back a nil
// RuntimeSpec and no error — prepareRuntime must never shell out to mise/uv
// in this case.
func TestPrepareRuntime_EmptyColumnIsPassthrough(t *testing.T) {
	d, _ := newRuntimeTestDispatcher(t)
	repo := gen.Repo{ID: "repo-1", RuntimeLanguages: ""}

	spec, err := d.prepareRuntime(context.Background(), repo, "/tmp/worktree")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec != nil {
		t.Fatalf("expected nil RuntimeSpec for empty runtime_languages, got %+v", spec)
	}
}

// TestPrepareRuntime_InvalidStoredConfigErrors verifies a corrupt/invalid
// stored runtime_languages value (should never happen via the validated API
// write path, but defends against a bad direct DB edit) is reported as an
// error rather than silently ignored or causing a panic — and, critically,
// never reaches runtime.Prep (no mise/uv exec attempted).
func TestPrepareRuntime_InvalidStoredConfigErrors(t *testing.T) {
	d, _ := newRuntimeTestDispatcher(t)
	repo := gen.Repo{ID: "repo-1", RuntimeLanguages: `[{"id":"php","version":"8.3"}]`}

	spec, err := d.prepareRuntime(context.Background(), repo, "/tmp/worktree")
	if err == nil {
		t.Fatal("expected error for invalid stored runtime_languages, got nil")
	}
	if spec != nil {
		t.Fatalf("expected nil RuntimeSpec on error, got %+v", spec)
	}
}

// TestPrepareRuntime_PrepFailurePropagates verifies a valid pin whose
// toolchain prep fails (here: no `mise` binary on PATH in the test
// environment, the same failure shape as a broken container image) returns
// an error rather than a usable RuntimeSpec.
func TestPrepareRuntime_PrepFailurePropagates(t *testing.T) {
	d, _ := newRuntimeTestDispatcher(t)
	repo := gen.Repo{ID: "repo-1", RuntimeLanguages: `[{"id":"go","version":"1.21"}]`}

	spec, err := d.prepareRuntime(context.Background(), repo, t.TempDir())
	if err == nil {
		t.Fatal("expected error when mise is unavailable, got nil")
	}
	if spec != nil {
		t.Fatalf("expected nil RuntimeSpec on prep failure, got %+v", spec)
	}
}

// TestEscalateRuntimePrepFailure_LocksTaskAsWaitingHuman verifies the
// fail-closed contract (PRD §4.6): a runtime prep failure marks the phantom
// run waiting_human, keeps active_agent_run_id locked on that run (so the
// dispatcher never re-picks the task with a plain, unprepared spawn), and
// restores current_agent_run_id to the task's prior real run rather than
// pointing it at the logless phantom run.
func TestEscalateRuntimePrepFailure_LocksTaskAsWaitingHuman(t *testing.T) {
	d, q := newRuntimeTestDispatcher(t)
	ctx := context.Background()

	wfID := "wf-1"
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "wf", Description: "test"}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	repoID := "repo-1"
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: "/tmp/repo", WorkflowID: &wfID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	task, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: "task-1", Title: "t", RepoID: repoID, WorkflowID: wfID, Label: "work",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Seed a prior real run so we can verify current_agent_run_id is
	// restored to it (not left pointing at the phantom escalation run).
	priorRunID := "run-prior"
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: priorRunID, TaskID: task.ID}); err != nil {
		t.Fatalf("create prior run: %v", err)
	}
	task.CurrentAgentRunID = &priorRunID

	phantomRunID := "run-phantom"
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: phantomRunID, TaskID: task.ID}); err != nil {
		t.Fatalf("create phantom run: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &phantomRunID,
		ActiveAgentRunID:  &phantomRunID,
		ID:                task.ID,
	}); err != nil {
		t.Fatalf("set active run: %v", err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1})) // discard
	err = d.escalateRuntimePrepFailure(ctx, task, phantomRunID, errFakePrep, log)
	if err == nil {
		t.Fatal("expected escalateRuntimePrepFailure to return an error")
	}
	if !strings.Contains(err.Error(), "fake prep failure") {
		t.Errorf("error = %v, want it to wrap the prep error", err)
	}

	run, err := q.GetAgentRun(ctx, phantomRunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "waiting_human" {
		t.Errorf("run status = %q, want waiting_human", run.Status)
	}
	if run.Notes == nil || !strings.Contains(*run.Notes, "fake prep failure") {
		t.Errorf("run notes = %v, want them to mention the prep error", run.Notes)
	}

	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.ActiveAgentRunID == nil || *updated.ActiveAgentRunID != phantomRunID {
		t.Errorf("active_agent_run_id = %v, want locked on phantom run %q", updated.ActiveAgentRunID, phantomRunID)
	}
	if updated.CurrentAgentRunID == nil || *updated.CurrentAgentRunID != priorRunID {
		t.Errorf("current_agent_run_id = %v, want restored to prior run %q", updated.CurrentAgentRunID, priorRunID)
	}
}
