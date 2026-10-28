package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

type env struct {
	db     *sql.DB
	q      *gen.Queries
	rec    *Service
	wfID   string
	repoID string
	labels map[string]gen.WorkflowLabel
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := storage.Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := storage.SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatal(err)
	}
	q := gen.New(db.SQL())
	wfs, _ := q.ListWorkflows(ctx)
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfs[0].ID}); err != nil {
		t.Fatal(err)
	}
	ls, _ := q.ListWorkflowLabels(ctx, wfs[0].ID)
	m := map[string]gen.WorkflowLabel{}
	for _, l := range ls {
		m[l.Name] = l
	}
	return &env{db: db.SQL(), q: q, rec: New(q), wfID: wfs[0].ID, repoID: repoID, labels: m}
}

func (e *env) setMode(t *testing.T, label, mode string) {
	t.Helper()
	l := e.labels[label]
	if err := e.q.SetWorkflowLabelAutonomy(context.Background(), gen.SetWorkflowLabelAutonomyParams{
		AutonomyMode: mode, AutonomyAskOn: `["migration"]`, AutonomyHoldSeconds: 600, ID: l.ID,
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) task(t *testing.T, label string) gen.Task {
	t.Helper()
	task, err := e.q.CreateTask(context.Background(), gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: e.wfID, RepoID: e.repoID, Label: label})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (e *env) decision(t *testing.T, task gen.Task, label string) (gen.AutonomyDecision, bool) {
	t.Helper()
	d, err := e.q.GetLatestAutonomyDecision(context.Background(), gen.GetLatestAutonomyDecisionParams{TaskID: task.ID, Label: label})
	if errors.Is(err, sql.ErrNoRows) {
		return d, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return d, true
}

func decisionCodes(t *testing.T, d gen.AutonomyDecision) []string {
	var rs []autonomy.Reason
	if err := json.Unmarshal([]byte(d.Reasons), &rs); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}

func TestAutoCleanApproves(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "auto")
	task := e.task(t, "review")
	e.rec.Handle(context.Background(), task.ID, "review")
	d, ok := e.decision(t, task, "review")
	if !ok || d.Decision != "approve" || d.Mode != "auto" {
		t.Fatalf("decision = %+v ok=%v", d, ok)
	}
	after, _ := e.q.GetTask(context.Background(), task.ID)
	if after.Label != "review" {
		t.Fatalf("label moved to %s", after.Label)
	}
}

func TestAlwaysAskAndVerify(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "auto")
	ctx := context.Background()

	task := e.task(t, "review")
	if n, err := e.q.SetTaskAlwaysAsk(ctx, gen.SetTaskAlwaysAskParams{AlwaysAsk: 1, ID: task.ID}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	e.rec.Handle(ctx, task.ID, "review")
	if d, ok := e.decision(t, task, "review"); !ok || d.Decision != "ask" || decisionCodes(t, d)[0] != autonomy.ReasonAlwaysAsk {
		t.Fatalf("always_ask: %+v", d)
	}

	task2 := e.task(t, "review")
	runID := uuid.NewString()
	if _, err := e.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: task2.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec("UPDATE tasks SET current_agent_run_id = ? WHERE id = ?", runID, task2.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.q.UpsertRunVerification(ctx, gen.UpsertRunVerificationParams{RunID: runID, TaskID: task2.ID, Command: "c", Status: "fail"}); err != nil {
		t.Fatal(err)
	}
	e.rec.Handle(ctx, task2.ID, "review")
	d, ok := e.decision(t, task2, "review")
	if !ok || d.Decision != "ask" || decisionCodes(t, d)[0] != autonomy.ReasonVerifyFailed || d.RunID == nil {
		t.Fatalf("verify: %+v", d)
	}
}

func TestOnFlagsNoBranch(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "on_flags")
	task := e.task(t, "review")
	e.rec.Handle(context.Background(), task.ID, "review")
	d, ok := e.decision(t, task, "review")
	if !ok || d.Decision != "ask" || decisionCodes(t, d)[0] != autonomy.ReasonNoDiff {
		t.Fatalf("decision = %+v", d)
	}
}

func TestNoRecord(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// gate in ask mode
	a := e.task(t, "review")
	e.rec.Handle(ctx, a.ID, "review")
	if _, ok := e.decision(t, a, "review"); ok {
		t.Fatal("ask-mode gate recorded")
	}
	// non-gate label with a mode set directly
	e.setMode(t, "work", "auto")
	b := e.task(t, "work")
	e.rec.Handle(ctx, b.ID, "work")
	if _, ok := e.decision(t, b, "work"); ok {
		t.Fatal("non-gate recorded")
	}
	// task moved on
	e.setMode(t, "review", "auto")
	c := e.task(t, "work")
	e.rec.Handle(ctx, c.ID, "review")
	if _, ok := e.decision(t, c, "review"); ok {
		t.Fatal("moved task recorded")
	}
	// unknown task: must not panic
	e.rec.Handle(ctx, "nope", "review")
}

func TestPublishFullQueueDoesNotBlock(t *testing.T) {
	r := New(nil)
	done := make(chan struct{})
	go func() {
		for i := 0; i < queueCap+10; i++ {
			r.Publish(wsevent.TypeTaskLabelChanged, map[string]any{"task_id": "t", "to": "review"})
		}
		r.Publish("other.event", nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked")
	}
	if len(r.queue) != queueCap {
		t.Fatalf("queue len = %d", len(r.queue))
	}
}
