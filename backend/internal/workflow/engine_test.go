package workflow_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/google/uuid"
)

// noopPublisher satisfies the Publisher interface without doing anything.
type noopPublisher struct {
	events []string
}

func (p *noopPublisher) Publish(eventType string, _ map[string]any) {
	p.events = append(p.events, eventType)
}

func setupTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "test-*.db")
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

	ctx := context.Background()
	if err := storage.SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func defaultWorkflowID(t *testing.T, db *storage.DB) string {
	t.Helper()
	q := gen.New(db.SQL())
	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatal("no workflow found after seed")
	}
	return wfs[0].ID
}

func createTestTask(t *testing.T, db *storage.DB, label, workflowID string) gen.Task {
	t.Helper()
	// Need a repo first
	q := gen.New(db.SQL())
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:         repoID,
		Name:       "test-repo",
		Path:       "/tmp/test-repo-" + repoID,
		WorkflowID: &workflowID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID:         uuid.NewString(),
		Title:      "Test Task",
		WorkflowID: workflowID,
		RepoID:     repoID,
		Label:      label,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

func TestTransition_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	pub := &noopPublisher{}
	engine := workflow.New(db.SQL(), pub)

	task := createTestTask(t, db, "work", wfID)

	err := engine.Transition(context.Background(), task.ID, "testing", workflow.TriggerAgent, "", "")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}

	if len(pub.events) != 1 || pub.events[0] != "task.label_changed" {
		t.Errorf("expected label_changed event, got: %v", pub.events)
	}

	// Verify label was updated in DB
	q := gen.New(db.SQL())
	updated, _ := q.GetTask(context.Background(), task.ID)
	if updated.Label != "testing" {
		t.Errorf("expected label testing, got %s", updated.Label)
	}
}

// TestTransition_ConcurrentSameLabel_OnlyOneSucceeds fires many transitions from
// the same source label at once and asserts the compare-and-swap lets exactly one
// win. Before the CAS, both would validate against the same from_label and both
// commit, recording two history rows from the same source. Now the losers fail
// cleanly (ErrStale if they lost the race after reading the old label, or
// ErrNoTransition if they read the already-moved label) and history records the
// move exactly once.
func TestTransition_ConcurrentSameLabel_OnlyOneSucceeds(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	task := createTestTask(t, db, "work", wfID)

	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release all at once to maximise contention
			errs[i] = engine.Transition(context.Background(), task.ID, "testing", workflow.TriggerAgent, "", "")
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, workflow.ErrStale), errors.Is(err, workflow.ErrNoTransition):
			// expected outcome for a loser
		default:
			t.Errorf("losing transition failed unexpectedly: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful transition, got %d", successes)
	}

	q := gen.New(db.SQL())
	updated, _ := q.GetTask(context.Background(), task.ID)
	if updated.Label != "testing" {
		t.Errorf("expected final label testing, got %s", updated.Label)
	}

	// The move must be recorded exactly once — the whole point of the CAS.
	hist, err := q.ListTaskLabelHistory(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	moves := 0
	for _, h := range hist {
		if h.ToLabel == "testing" {
			moves++
		}
	}
	if moves != 1 {
		t.Errorf("expected exactly one history entry for the move, got %d", moves)
	}
}

// TestTransition_HumanMoveWhileRunLive_DoesNotOrphanLock reproduces the issue
// #244 sequence directly against the engine + DB:
//  1. Run R1 is set as the task's active run while the task sits on "work".
//  2. A human moves the label (engine.Transition with TriggerHuman). Per
//     current engine behaviour this clears the lock as part of its CAS.
//  3. A new dispatch picks the task up and sets R2 as the active run.
//  4. R1's (now-stale) cleanup calls the owner-scoped
//     ClearActiveAgentRunIfOwner with its own run id. Because the lock no
//     longer points at R1, this must be a no-op: RowsAffected must be 0, and
//     the task's active_agent_run_id must still be R2 afterwards.
//
// Before the fix (plain ClearActiveAgentRun keyed only on task id), R1's
// cleanup would have wiped R2's lock, orphaning it and enabling a concurrent
// third dispatch. This test only exercises the new scoped clear (Change 1);
// the human-transition-vs-live-run 409 guard itself (Change 6) is exercised
// in the handlers package where MoveLabel lives.
func TestTransition_HumanMoveWhileRunLive_DoesNotOrphanLock(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})
	q := gen.New(db.SQL())
	ctx := context.Background()

	task := createTestTask(t, db, "work", wfID)

	// Step 1: R1 is dispatched and becomes the task's active run.
	run1, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:     uuid.NewString(),
		TaskID: task.ID,
	})
	if err != nil {
		t.Fatalf("create run1: %v", err)
	}
	if _, err := q.SetAgentRunStarted(ctx, run1.ID); err != nil {
		t.Fatalf("start run1: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &run1.ID,
		ActiveAgentRunID:  &run1.ID,
		ID:                task.ID,
	}); err != nil {
		t.Fatalf("set active run1: %v", err)
	}

	// Step 2: a human moves the label while R1 is still "running". This
	// clears the lock as part of the transition's CAS (current behaviour;
	// see the comment in engine.go on why the CAS clear stays unconditional).
	if err := engine.Transition(ctx, task.ID, "testing", workflow.TriggerHuman, "human-actor", ""); err != nil {
		t.Fatalf("human transition: %v", err)
	}

	// Step 3: a new dispatch (R2) picks the task up on its new label and
	// claims the lock.
	run2, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:     uuid.NewString(),
		TaskID: task.ID,
	})
	if err != nil {
		t.Fatalf("create run2: %v", err)
	}
	if _, err := q.SetAgentRunStarted(ctx, run2.ID); err != nil {
		t.Fatalf("start run2: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{
		CurrentAgentRunID: &run2.ID,
		ActiveAgentRunID:  &run2.ID,
		ID:                task.ID,
	}); err != nil {
		t.Fatalf("set active run2: %v", err)
	}

	// Step 4: R1's stale cleanup releases its lock via the owner-scoped
	// clear. It must be a no-op: the lock is R2's, not R1's.
	rows, err := q.ClearActiveAgentRunIfOwner(ctx, gen.ClearActiveAgentRunIfOwnerParams{
		ID:               task.ID,
		ActiveAgentRunID: &run1.ID,
	})
	if err != nil {
		t.Fatalf("clear active run if owner (R1): %v", err)
	}
	if rows != 0 {
		t.Fatalf("expected R1's stale release to affect 0 rows, affected %d", rows)
	}

	updated, err := q.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated.ActiveAgentRunID == nil || *updated.ActiveAgentRunID != run2.ID {
		t.Fatalf("expected active_agent_run_id to still be R2 (%s), got %v", run2.ID, updated.ActiveAgentRunID)
	}

	// Sanity: R2 can release its own lock.
	rows, err = q.ClearActiveAgentRunIfOwner(ctx, gen.ClearActiveAgentRunIfOwnerParams{
		ID:               task.ID,
		ActiveAgentRunID: &run2.ID,
	})
	if err != nil {
		t.Fatalf("clear active run if owner (R2): %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected R2's own release to affect 1 row, affected %d", rows)
	}
}

func TestTransition_NoTransitionDefined(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	task := createTestTask(t, db, "work", wfID)

	err := engine.Transition(context.Background(), task.ID, "done", workflow.TriggerAgent, "", "")
	if err != workflow.ErrNoTransition {
		t.Errorf("expected ErrNoTransition, got: %v", err)
	}
}

func TestTransition_GateRequired(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	// review→done is human-only
	task := createTestTask(t, db, "review", wfID)

	err := engine.Transition(context.Background(), task.ID, "done", workflow.TriggerAgent, "", "")
	if err != workflow.ErrGateRequired {
		t.Errorf("expected ErrGateRequired, got: %v", err)
	}
}

func TestTransition_HumanCanBypassGate(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	task := createTestTask(t, db, "review-plan", wfID)

	err := engine.Transition(context.Background(), task.ID, "work", workflow.TriggerHuman, "user-1", "approved")
	if err != nil {
		t.Fatalf("human should be able to trigger human-only transition, got: %v", err)
	}
}

func TestTransition_AgentCannotMoveToAgentIgnoredLabel(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	// not_ready has agent_ignore=true and no transition targets it in the
	// default workflow, so an agent attempting to move a task there always
	// fails (with ErrNoTransition, since no from→not_ready transition exists).
	task := createTestTask(t, db, "plan", wfID)

	err := engine.Transition(context.Background(), task.ID, "not_ready", workflow.TriggerAgent, "", "")
	if err == nil {
		t.Error("expected error moving agent to not_ready, got nil")
	}
}

func TestAvailableTransitions_Agent(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	task := createTestTask(t, db, "work", wfID)

	transitions, err := engine.AvailableTransitions(context.Background(), task.ID, workflow.TriggerAgent)
	if err != nil {
		t.Fatal(err)
	}

	// work→testing (agent) is the only transition from work
	if len(transitions) != 1 || transitions[0] != "testing" {
		t.Errorf("expected [testing], got %v", transitions)
	}
}

func TestAvailableTransitions_Human(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	task := createTestTask(t, db, "review", wfID)

	transitions, err := engine.AvailableTransitions(context.Background(), task.ID, workflow.TriggerHuman)
	if err != nil {
		t.Fatal(err)
	}

	// review→done (human), review→work (human)
	if len(transitions) < 2 {
		t.Errorf("expected multiple human transitions from review, got %v", transitions)
	}
}

func TestAgentPickupLabels(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	labels, err := engine.AgentPickupLabels(context.Background(), wfID)
	if err != nil {
		t.Fatal(err)
	}

	// not_ready must NOT be in the list (agent_ignore=true)
	for _, l := range labels {
		if l == "not_ready" {
			t.Error("not_ready should not be in agent pickup labels")
		}
	}

	// plan, work, testing, agent-review must be present (labels with an
	// agent-triggerable outgoing transition)
	expected := map[string]bool{"plan": true, "work": true, "testing": true, "agent-review": true}
	for _, l := range labels {
		delete(expected, l)
	}
	if len(expected) > 0 {
		t.Errorf("missing agent pickup labels: %v", expected)
	}
}

func TestFeedbackLoop_ReviewToWork(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	task := createTestTask(t, db, "review", wfID)

	err := engine.Transition(context.Background(), task.ID, "work", workflow.TriggerHuman, "reviewer-1", "needs more tests")
	if err != nil {
		t.Fatalf("feedback loop transition failed: %v", err)
	}

	q := gen.New(db.SQL())
	updated, _ := q.GetTask(context.Background(), task.ID)
	if updated.Label != "work" {
		t.Errorf("expected work after rejection, got %s", updated.Label)
	}
}

func TestTransition_OnLeaveNotReady_FiresOnceWhenLeavingNotReady(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	var calls []string
	engine.OnLeaveNotReady = func(_ context.Context, task gen.Task) {
		calls = append(calls, task.ID)
	}

	task := createTestTask(t, db, "not_ready", wfID)

	if err := engine.Transition(context.Background(), task.ID, "plan", workflow.TriggerHuman, "human-1", ""); err != nil {
		t.Fatalf("transition failed: %v", err)
	}

	if len(calls) != 1 || calls[0] != task.ID {
		t.Fatalf("expected OnLeaveNotReady to fire once for task %s, got %v", task.ID, calls)
	}
}

func TestTransition_OnLeaveNotReady_DoesNotFireOnOtherTransitions(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})

	var calls []string
	engine.OnLeaveNotReady = func(_ context.Context, task gen.Task) {
		calls = append(calls, task.ID)
	}

	// review -> work is a feedback-loop transition unrelated to not_ready.
	task := createTestTask(t, db, "review", wfID)
	if err := engine.Transition(context.Background(), task.ID, "work", workflow.TriggerHuman, "reviewer-1", ""); err != nil {
		t.Fatalf("transition failed: %v", err)
	}

	if len(calls) != 0 {
		t.Fatalf("expected OnLeaveNotReady not to fire for review->work, got %v", calls)
	}
}

func TestTransition_OnLeaveNotReady_NilCallbackIsSafe(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})
	// engine.OnLeaveNotReady left nil — Transition must not panic.

	task := createTestTask(t, db, "not_ready", wfID)
	if err := engine.Transition(context.Background(), task.ID, "plan", workflow.TriggerHuman, "human-1", ""); err != nil {
		t.Fatalf("transition failed: %v", err)
	}
}

// markCreatePR flips the create_pr flag on a seeded workflow label.
func markCreatePR(t *testing.T, db *storage.DB, wfID, label string) {
	t.Helper()
	if _, err := db.SQL().Exec(
		`UPDATE workflow_labels SET create_pr = 1 WHERE workflow_id = ? AND name = ?`,
		wfID, label,
	); err != nil {
		t.Fatalf("mark create_pr: %v", err)
	}
}

func TestTransition_OnCreatePR_FiresWhenEnteringCreatePRLabel(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	markCreatePR(t, db, wfID, "review")
	engine := workflow.New(db.SQL(), &noopPublisher{})

	var calls []string
	engine.OnCreatePR = func(_ context.Context, task gen.Task) {
		calls = append(calls, task.Label) // label should already be the destination
	}

	task := createTestTask(t, db, "agent-review", wfID)
	if err := engine.Transition(context.Background(), task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatalf("transition failed: %v", err)
	}

	if len(calls) != 1 || calls[0] != "review" {
		t.Fatalf("expected OnCreatePR to fire once with label review, got %v", calls)
	}
}

func TestTransition_OnCreatePR_DoesNotFireForNonCreatePRLabel(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	// No label marked create_pr.
	engine := workflow.New(db.SQL(), &noopPublisher{})

	var calls []string
	engine.OnCreatePR = func(_ context.Context, task gen.Task) {
		calls = append(calls, task.ID)
	}

	task := createTestTask(t, db, "work", wfID)
	if err := engine.Transition(context.Background(), task.ID, "testing", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatalf("transition failed: %v", err)
	}

	if len(calls) != 0 {
		t.Fatalf("expected OnCreatePR not to fire, got %v", calls)
	}
}

func TestTransition_OnCreatePR_NilCallbackIsSafe(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	markCreatePR(t, db, wfID, "review")
	engine := workflow.New(db.SQL(), &noopPublisher{})
	// engine.OnCreatePR left nil — Transition must not panic.

	task := createTestTask(t, db, "agent-review", wfID)
	if err := engine.Transition(context.Background(), task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatalf("transition failed: %v", err)
	}
}
