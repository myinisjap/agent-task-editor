package workflow_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type eventRecorder struct {
	mu     sync.Mutex
	events []map[string]any
	types  []string
}

func (r *eventRecorder) Publish(eventType string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types = append(r.types, eventType)
	r.events = append(r.events, wsevent.ToMap(payload))
}

func (r *eventRecorder) sideEffectEvents() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for i, ty := range r.types {
		if ty == "task.side_effect" {
			out = append(out, r.events[i])
		}
	}
	return out
}

func sideEffectRows(t *testing.T, db *storage.DB, taskID string) []gen.TaskSideEffect {
	t.Helper()
	rows, err := db.SQL().Query(`SELECT id, task_id, kind, status, attempts, next_at, last_error, created_at, updated_at FROM task_side_effects WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []gen.TaskSideEffect
	for rows.Next() {
		var r gen.TaskSideEffect
		if err := rows.Scan(&r.ID, &r.TaskID, &r.Kind, &r.Status, &r.Attempts, &r.NextAt, &r.LastError, &r.CreatedAt, &r.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// createPREngine returns an engine with review marked create_pr, a task on
// agent-review, and a hook that counts calls and returns hookErr.
func createPREngine(t *testing.T, pub workflow.Publisher) (*workflow.Engine, *storage.DB, gen.Task, *int, *error) {
	t.Helper()
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	markCreatePR(t, db, wfID, "review")
	engine := workflow.New(db.SQL(), pub)
	calls := 0
	var hookErr error
	engine.OnCreatePR = func(_ context.Context, _ gen.Task) error {
		calls++
		return hookErr
	}
	task := createTestTask(t, db, "agent-review", wfID)
	return engine, db, task, &calls, &hookErr
}

func TestSideEffects_RowWrittenInTransitionTx(t *testing.T) {
	engine, db, task, calls, _ := createPREngine(t, &noopPublisher{})
	if err := engine.Transition(context.Background(), task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Fatalf("hook must not run inside Transition, ran %d times", *calls)
	}
	rows := sideEffectRows(t, db, task.ID)
	if len(rows) != 1 || rows[0].Kind != workflow.SideEffectCreatePR || rows[0].Status != workflow.SideEffectPending || rows[0].Attempts != 0 {
		t.Fatalf("expected one pending create_pr row, got %+v", rows)
	}
}

func TestSideEffects_NoRowWithoutHook(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	markCreatePR(t, db, wfID, "review")
	engine := workflow.New(db.SQL(), &noopPublisher{}) // no hooks wired
	task := createTestTask(t, db, "agent-review", wfID)
	if err := engine.Transition(context.Background(), task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 0 {
		t.Fatalf("expected no rows, got %+v", rows)
	}
}

func TestSideEffects_FailedTransitionWritesNoRow(t *testing.T) {
	engine, db, task, _, _ := createPREngine(t, &noopPublisher{})
	ctx := context.Background()

	// Undefined edge.
	if err := engine.Transition(ctx, task.ID, "done", workflow.TriggerAgent, "", ""); !errors.Is(err, workflow.ErrNoTransition) {
		t.Fatalf("expected ErrNoTransition, got %v", err)
	}
	// Stale expected-from label.
	if err := engine.TransitionExpecting(ctx, task.ID, "review", workflow.TriggerAgent, "", "", "work"); !errors.Is(err, workflow.ErrStale) {
		t.Fatalf("expected ErrStale, got %v", err)
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 0 {
		t.Fatalf("failed transitions must not enqueue, got %+v", rows)
	}
}

func TestSideEffects_EnqueueIsIdempotentWhilePending(t *testing.T) {
	engine, db, task, calls, _ := createPREngine(t, &noopPublisher{})
	ctx := context.Background()
	q := gen.New(db.SQL())
	for i := 0; i < 3; i++ {
		if err := q.EnqueueTaskSideEffect(ctx, gen.EnqueueTaskSideEffectParams{ID: "id" + string(rune('a'+i)), TaskID: task.ID, Kind: workflow.SideEffectCreatePR}); err != nil {
			t.Fatal(err)
		}
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 1 {
		t.Fatalf("expected deduped to one row, got %d", len(rows))
	}
	// A transition into the label while one is pending adds nothing either.
	if err := engine.Transition(ctx, task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 1 {
		t.Fatalf("expected still one row, got %d", len(rows))
	}
	engine.DrainSideEffects(ctx)
	if *calls != 1 {
		t.Fatalf("expected hook to run once, ran %d", *calls)
	}
}

func TestSideEffects_SuccessClearsRow(t *testing.T) {
	pub := &eventRecorder{}
	engine, db, task, calls, _ := createPREngine(t, pub)
	ctx := context.Background()
	if err := engine.Transition(ctx, task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := engine.DrainSideEffects(ctx); n != 1 {
		t.Fatalf("expected 1 effect run, got %d", n)
	}
	if *calls != 1 {
		t.Fatalf("hook calls = %d", *calls)
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 0 {
		t.Fatalf("expected row cleared on success, got %+v", rows)
	}
	evs := pub.sideEffectEvents()
	if len(evs) != 1 || evs[0]["status"] != "done" || evs[0]["task_id"] != task.ID {
		t.Fatalf("expected a done event, got %v", evs)
	}
}

func TestSideEffects_RetriesWithBackoffAndRecordsError(t *testing.T) {
	pub := &eventRecorder{}
	engine, db, task, calls, hookErr := createPREngine(t, pub)
	ctx := context.Background()
	var gotAttempts []int
	engine.SideEffectBackoff = func(attempts int) time.Duration {
		gotAttempts = append(gotAttempts, attempts)
		return time.Hour
	}
	*hookErr = errors.New("push rejected")
	if err := engine.Transition(ctx, task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}

	engine.DrainSideEffects(ctx)
	rows := sideEffectRows(t, db, task.ID)
	if len(rows) != 1 {
		t.Fatalf("expected row kept, got %+v", rows)
	}
	r := rows[0]
	if r.Status != workflow.SideEffectPending || r.Attempts != 1 || r.LastError == nil || *r.LastError != "push rejected" {
		t.Fatalf("unexpected row after failure: %+v", r)
	}
	if !r.NextAt.After(time.Now().Add(30 * time.Minute)) {
		t.Fatalf("expected next_at about an hour out, got %v", r.NextAt)
	}
	if len(gotAttempts) != 1 || gotAttempts[0] != 1 {
		t.Fatalf("backoff args = %v", gotAttempts)
	}

	// Not due yet: a drain must not call the hook again.
	engine.DrainSideEffects(ctx)
	if *calls != 1 {
		t.Fatalf("hook ran before backoff elapsed: %d calls", *calls)
	}
	evs := pub.sideEffectEvents()
	if len(evs) != 1 || evs[0]["last_error"] != "push rejected" || evs[0]["status"] != "pending" {
		t.Fatalf("expected a retrying event, got %v", evs)
	}

	// Backoff elapsed and the hook now succeeds: row is cleared.
	if _, err := db.SQL().Exec(`UPDATE task_side_effects SET next_at = datetime('now', '-1 minute')`); err != nil {
		t.Fatal(err)
	}
	*hookErr = nil
	engine.DrainSideEffects(ctx)
	if *calls != 2 {
		t.Fatalf("expected second attempt, calls=%d", *calls)
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 0 {
		t.Fatalf("expected row cleared, got %+v", rows)
	}
}

func TestSideEffects_MaxAttemptsParksAsFailedAndManualRetry(t *testing.T) {
	pub := &eventRecorder{}
	engine, db, task, calls, hookErr := createPREngine(t, pub)
	ctx := context.Background()
	engine.SideEffectBackoff = func(int) time.Duration { return 0 }
	*hookErr = errors.New("gh down")
	if err := engine.Transition(ctx, task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	// A single drain call runs each row at most once, so loop.
	for i := 0; i < workflow.MaxSideEffectAttempts+2; i++ {
		engine.DrainSideEffects(ctx)
	}
	if *calls != workflow.MaxSideEffectAttempts {
		t.Fatalf("expected %d attempts, got %d", workflow.MaxSideEffectAttempts, *calls)
	}
	rows := sideEffectRows(t, db, task.ID)
	if len(rows) != 1 || rows[0].Status != workflow.SideEffectFailed || rows[0].Attempts != int64(workflow.MaxSideEffectAttempts) {
		t.Fatalf("expected one failed row, got %+v", rows)
	}
	evs := pub.sideEffectEvents()
	if last := evs[len(evs)-1]; last["status"] != "failed" {
		t.Fatalf("expected final failed event, got %v", last)
	}

	// Manual retry resets the budget and the worker clears it on success.
	*hookErr = nil
	n, err := engine.RetrySideEffects(ctx, task.ID)
	if err != nil || n != 1 {
		t.Fatalf("retry: n=%d err=%v", n, err)
	}
	engine.DrainSideEffects(ctx)
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 0 {
		t.Fatalf("expected cleared after manual retry, got %+v", rows)
	}
	if n, _ := engine.RetrySideEffects(ctx, task.ID); n != 0 {
		t.Fatalf("expected nothing to retry, got %d", n)
	}
}

func TestSideEffects_StaleEffectIsDropped(t *testing.T) {
	engine, db, task, calls, _ := createPREngine(t, &noopPublisher{})
	ctx := context.Background()
	if err := engine.Transition(ctx, task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	// A human moves the task back before the worker gets to it.
	if err := engine.Transition(ctx, task.ID, "work", workflow.TriggerHuman, "h", ""); err != nil {
		t.Fatal(err)
	}
	engine.DrainSideEffects(ctx)
	if *calls != 0 {
		t.Fatalf("hook must not run for a task that left the create_pr label")
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 0 {
		t.Fatalf("expected stale row cleared, got %+v", rows)
	}
}

func TestSideEffects_TerminalAndLeaveNotReadyEnqueue(t *testing.T) {
	db := setupTestDB(t)
	wfID := defaultWorkflowID(t, db)
	engine := workflow.New(db.SQL(), &noopPublisher{})
	var terminal, leave int
	engine.OnTerminal = func(context.Context, gen.Task) error { terminal++; return nil }
	engine.OnLeaveNotReady = func(context.Context, gen.Task) error { leave++; return nil }
	ctx := context.Background()

	task := createTestTask(t, db, "not_ready", wfID)
	if err := engine.Transition(ctx, task.ID, "plan", workflow.TriggerHuman, "h", ""); err != nil {
		t.Fatal(err)
	}
	rows := sideEffectRows(t, db, task.ID)
	if len(rows) != 1 || rows[0].Kind != workflow.SideEffectLeaveNotReady {
		t.Fatalf("expected leave_not_ready row, got %+v", rows)
	}

	// Force into a terminal label.
	var terminalLabel string
	if err := db.SQL().QueryRow(`SELECT name FROM workflow_labels WHERE workflow_id = ? AND is_terminal = 1 LIMIT 1`, wfID).Scan(&terminalLabel); err != nil {
		t.Fatal(err)
	}
	if err := engine.ForceTransition(ctx, task.ID, terminalLabel, "h", ""); err != nil {
		t.Fatal(err)
	}
	engine.DrainSideEffects(ctx)
	if terminal != 1 || leave != 1 {
		t.Fatalf("terminal=%d leave=%d", terminal, leave)
	}
}

func TestRunSideEffects_DrainsOnWakeAndStopsOnCancel(t *testing.T) {
	engine, db, task, _, _ := createPREngine(t, &noopPublisher{})
	done := make(chan struct{})
	engine.OnCreatePR = func(context.Context, gen.Task) error { close(done); return nil }

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { defer close(stopped); engine.RunSideEffects(ctx) }()

	if err := engine.Transition(context.Background(), task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not run the effect")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop on cancel")
	}
	_ = db
}

func TestRunSideEffects_RecoversRunningRowsOnStart(t *testing.T) {
	engine, db, task, calls, _ := createPREngine(t, &noopPublisher{})
	if err := engine.Transition(context.Background(), task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`UPDATE task_side_effects SET status = 'running'`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { defer close(stopped); engine.RunSideEffects(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(sideEffectRows(t, db, task.ID)) != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-stopped
	if len(sideEffectRows(t, db, task.ID)) != 0 {
		t.Fatal("interrupted running row was not recovered and run")
	}
	_ = calls
}

func TestSideEffects_MergedTaskPushAndPRAreNoOps(t *testing.T) {
	engine, db, task, calls, hookErr := createPREngine(t, &noopPublisher{})
	ctx := context.Background()
	*hookErr = errors.New("src refspec does not match any")
	if err := engine.Transition(ctx, task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO task_git (task_id, git_state) VALUES (?, 'pr_merged') ON CONFLICT(task_id) DO UPDATE SET git_state = 'pr_merged'`, task.ID); err != nil {
		t.Fatal(err)
	}
	engine.DrainSideEffects(ctx)
	if *calls != 0 {
		t.Fatalf("hook must not run for a merged task, ran %d times", *calls)
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 0 {
		t.Fatalf("expected row deleted, got %+v", rows)
	}
}

func TestSideEffects_DismissDeletesOnlyFailed(t *testing.T) {
	engine, db, task, _, hookErr := createPREngine(t, &noopPublisher{})
	ctx := context.Background()
	engine.SideEffectBackoff = func(int) time.Duration { return 0 }
	*hookErr = errors.New("down")
	if err := engine.Transition(ctx, task.ID, "review", workflow.TriggerAgent, "", ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := engine.DismissSideEffects(ctx, task.ID); n != 0 {
		t.Fatalf("pending row must not be dismissed, got %d", n)
	}
	for i := 0; i < workflow.MaxSideEffectAttempts+1; i++ {
		engine.DrainSideEffects(ctx)
	}
	if n, err := engine.DismissSideEffects(ctx, task.ID); err != nil || n != 1 {
		t.Fatalf("dismiss: n=%d err=%v", n, err)
	}
	if rows := sideEffectRows(t, db, task.ID); len(rows) != 0 {
		t.Fatalf("expected none, got %+v", rows)
	}
}
