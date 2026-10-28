package policy

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

type recPub struct {
	mu     sync.Mutex
	events []string
}

func (p *recPub) Publish(t string, _ any) {
	p.mu.Lock()
	p.events = append(p.events, t)
	p.mu.Unlock()
}

func (p *recPub) has(t string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.events {
		if e == t {
			return true
		}
	}
	return false
}

// wire attaches a real engine and a recording publisher to e.rec.
func (e *env) wire(t *testing.T) (*workflow.Engine, *recPub) {
	t.Helper()
	pub := &recPub{}
	eng := workflow.New(e.db, pub)
	e.rec.SetApprover(eng)
	e.rec.SetPublisher(pub)
	return eng, pub
}

func (e *env) setHold(t *testing.T, label string, hold int64) {
	t.Helper()
	l := e.labels[label]
	if err := e.q.SetWorkflowLabelAutonomy(context.Background(), gen.SetWorkflowLabelAutonomyParams{
		AutonomyMode: "auto", AutonomyAskOn: `[]`, AutonomyHoldSeconds: hold, ID: l.ID,
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) label(t *testing.T, id string) string {
	t.Helper()
	task, err := e.q.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task.Label
}

func TestHoldWindowCreatesPending(t *testing.T) {
	e := setup(t)
	_, pub := e.wire(t)
	e.setHold(t, "review", 600)
	task := e.task(t, "review")
	e.rec.Handle(context.Background(), task.ID, "review")
	d, ok := e.decision(t, task, "review")
	if !ok || d.Outcome == nil || *d.Outcome != autonomy.OutcomePending || d.DueAt == nil || d.HoldSeconds == nil || *d.HoldSeconds != 600 {
		t.Fatalf("decision = %+v", d)
	}
	if got := time.Until(*d.DueAt); got < 590*time.Second || got > 601*time.Second {
		t.Fatalf("due_at off: %v", got)
	}
	if !pub.has(wsevent.TypeTaskAutonomyPending) {
		t.Fatal("no pending event")
	}
	if e.label(t, task.ID) != "review" {
		t.Fatal("task moved during hold")
	}
}

func TestHoldZeroApprovesImmediately(t *testing.T) {
	e := setup(t)
	_, pub := e.wire(t)
	e.setHold(t, "review", 0)
	task := e.task(t, "review")
	e.rec.Handle(context.Background(), task.ID, "review")
	if e.label(t, task.ID) == "review" {
		t.Fatal("not approved")
	}
	d, _ := e.decision(t, task, "review")
	if d.Outcome == nil || *d.Outcome != autonomy.OutcomeApproved {
		t.Fatalf("outcome = %v", d.Outcome)
	}
	hist, _ := e.q.ListTaskLabelHistory(context.Background(), task.ID)
	last := hist[len(hist)-1]
	if last.ActorID == nil || *last.ActorID != "policy" || last.Trigger != "human" || last.Note == nil || *last.Note != *d.Note {
		t.Fatalf("history = %+v", last)
	}
	if !pub.has(wsevent.TypeTaskAutonomyApproved) {
		t.Fatal("no approved event")
	}
}

func pendingTask(t *testing.T, e *env) (gen.Task, gen.AutonomyDecision) {
	t.Helper()
	e.setHold(t, "review", 600)
	task := e.task(t, "review")
	e.rec.Handle(context.Background(), task.ID, "review")
	d, ok := e.decision(t, task, "review")
	if !ok || d.Outcome == nil || *d.Outcome != autonomy.OutcomePending {
		t.Fatalf("no pending: %+v", d)
	}
	return task, d
}

func (e *env) makeDue(t *testing.T, id string) {
	t.Helper()
	if _, err := e.db.Exec("UPDATE autonomy_decisions SET due_at = ? WHERE id = ?", storage.FormatTime(time.Now().Add(-time.Minute)), id); err != nil {
		t.Fatal(err)
	}
}

func outcome(t *testing.T, e *env, id string) string {
	d, err := e.q.GetAutonomyDecision(context.Background(), id)
	if err != nil || d.Outcome == nil {
		t.Fatalf("outcome: %v %v", d, err)
	}
	return *d.Outcome
}

func TestSweepApprovesDueOnly(t *testing.T) {
	e := setup(t)
	e.wire(t)
	ctx := context.Background()
	due, dd := pendingTask(t, e)
	notDue, nd := pendingTask(t, e)
	e.makeDue(t, dd.ID)
	e.rec.Sweep(ctx)
	if e.label(t, due.ID) == "review" || outcome(t, e, dd.ID) != autonomy.OutcomeApproved {
		t.Fatal("due task not approved")
	}
	if e.label(t, notDue.ID) != "review" || outcome(t, e, nd.ID) != autonomy.OutcomePending {
		t.Fatal("not-due task touched")
	}
}

func TestSweepSupersedesMovedTask(t *testing.T) {
	e := setup(t)
	e.wire(t)
	task, d := pendingTask(t, e)
	if _, err := e.db.Exec("UPDATE tasks SET label = 'work' WHERE id = ?", task.ID); err != nil {
		t.Fatal(err)
	}
	e.makeDue(t, d.ID)
	e.rec.Sweep(context.Background())
	if outcome(t, e, d.ID) != autonomy.OutcomeSuperseded || e.label(t, task.ID) != "work" {
		t.Fatal("not superseded")
	}
}

func TestSweepSkipsHeld(t *testing.T) {
	e := setup(t)
	e.wire(t)
	ctx := context.Background()
	task, d := pendingTask(t, e)
	actor := "me"
	now := time.Now().UTC()
	n, err := e.q.HoldPendingAutonomy(ctx, gen.HoldPendingAutonomyParams{OutcomeAt: &now, OutcomeActor: &actor, TaskID: task.ID, Label: "review"})
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	e.makeDue(t, d.ID) // held rows are not listed as due
	e.rec.Sweep(ctx)
	if e.label(t, task.ID) != "review" || outcome(t, e, d.ID) != autonomy.OutcomeHeld {
		t.Fatal("held task approved")
	}
	// Claim after a hold affects nothing.
	n, _ = e.q.ClaimAutonomyApproval(ctx, gen.ClaimAutonomyApprovalParams{OutcomeAt: &now, ID: d.ID})
	if n != 0 {
		t.Fatal("claim succeeded after hold")
	}
}

func TestSweepSkipsLiveRun(t *testing.T) {
	e := setup(t)
	_, pub := e.wire(t)
	ctx := context.Background()
	task, d := pendingTask(t, e)
	runID := uuid.NewString()
	if _, err := e.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec("UPDATE agent_runs SET status = 'running' WHERE id = ?", runID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec("UPDATE tasks SET active_agent_run_id = ? WHERE id = ?", runID, task.ID); err != nil {
		t.Fatal(err)
	}
	e.makeDue(t, d.ID)
	e.rec.Sweep(ctx)
	if e.label(t, task.ID) != "review" || outcome(t, e, d.ID) != autonomy.OutcomeSkipped || !pub.has(wsevent.TypeTaskAutonomyAsked) {
		t.Fatal("live-run task not skipped")
	}
}

func TestPendingSurvivesRestart(t *testing.T) {
	e := setup(t)
	task, d := pendingTask(t, e)
	e.makeDue(t, d.ID)
	// A brand-new service on the same DB (a restarted server) picks it up.
	fresh := New(e.q)
	fresh.SweepInterval = time.Hour
	fresh.SetApprover(workflow.New(e.db, &recPub{}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { fresh.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for e.label(t, task.ID) == "review" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if e.label(t, task.ID) == "review" || outcome(t, e, d.ID) != autonomy.OutcomeApproved {
		t.Fatal("pending approval not processed after restart")
	}
}

func TestLandingAfterUndoAsks(t *testing.T) {
	e := setup(t)
	_, pub := e.wire(t)
	e.setHold(t, "review", 0)
	task := e.task(t, "review")
	ctx := context.Background()
	note := autonomy.UndoNote
	actor := "me"
	prev := "work"
	if _, err := e.db.Exec(
		"INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger, actor_id, note) VALUES (?, ?, ?, 'review', 'human', ?, ?)",
		uuid.NewString(), task.ID, prev, actor, note); err != nil {
		t.Fatal(err)
	}
	e.rec.Handle(ctx, task.ID, "review")
	d, ok := e.decision(t, task, "review")
	if !ok || d.Decision != "ask" || d.Outcome != nil || decisionCodes(t, d)[0] != autonomy.ReasonUndone {
		t.Fatalf("decision = %+v", d)
	}
	if e.label(t, task.ID) != "review" || !pub.has(wsevent.TypeTaskAutonomyAsked) {
		t.Fatal("undone landing must stay and notify")
	}
}

func TestAskDecisionPublishesAsked(t *testing.T) {
	e := setup(t)
	_, pub := e.wire(t)
	e.setHold(t, "review", 600)
	task := e.task(t, "review")
	if _, err := e.q.SetTaskAlwaysAsk(context.Background(), gen.SetTaskAlwaysAskParams{AlwaysAsk: 1, ID: task.ID}); err != nil {
		t.Fatal(err)
	}
	e.rec.Handle(context.Background(), task.ID, "review")
	if !pub.has(wsevent.TypeTaskAutonomyAsked) {
		t.Fatal("no asked event")
	}
}
