package policy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/escape"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func (e *env) addRule(t *testing.T, label *string, globs string, typ *string, min *int64) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := e.q.CreateAskRule(context.Background(), gen.CreateAskRuleParams{
		ID: id, WorkflowID: e.wfID, Label: label, PathGlobs: globs, TaskType: typ, MinChangedLines: min, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAskRuleForcesAskAndCountsHit(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "auto")
	typ := "bug"
	other := "feature"
	hit := e.addRule(t, nil, `[]`, &typ, nil)
	miss := e.addRule(t, nil, `[]`, &other, nil)
	task := e.task(t, "review")
	if _, err := e.db.Exec("UPDATE tasks SET type = 'bug' WHERE id = ?", task.ID); err != nil {
		t.Fatal(err)
	}
	e.rec.Handle(context.Background(), task.ID, "review")
	d, ok := e.decision(t, task, "review")
	if !ok || d.Decision != "ask" || !strings.Contains(d.Reasons, `"rule_id":"`+hit+`"`) || strings.Contains(d.Reasons, miss) {
		t.Fatalf("decision = %+v", d)
	}
	h, _ := e.q.GetAskRule(context.Background(), hit)
	m, _ := e.q.GetAskRule(context.Background(), miss)
	if h.HitCount != 1 || h.LastHitAt == nil || m.HitCount != 0 {
		t.Fatalf("hits: %+v %+v", h, m)
	}
}

func TestPathRuleWithoutDiffDoesNotMatch(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review", "auto")
	e.addRule(t, nil, `["src/**"]`, nil, nil)
	task := e.task(t, "review")
	e.rec.Handle(context.Background(), task.ID, "review")
	if d, _ := e.decision(t, task, "review"); d.Decision != "approve" {
		t.Fatalf("decision = %s", d.Decision)
	}
}

// approvedAt seeds a policy-approved decision at a gate and returns its id.
func (e *env) approvedAt(t *testing.T, task gen.Task, label string, ago time.Duration) string {
	t.Helper()
	id := uuid.NewString()
	out := "approved"
	at := time.Now().UTC().Add(-ago)
	if err := e.q.CreateAutonomyDecision(context.Background(), gen.CreateAutonomyDecisionParams{
		ID: id, TaskID: task.ID, WorkflowID: e.wfID, Label: label, Mode: "auto", Decision: "approve", Reasons: "[]", Outcome: &out}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec("UPDATE autonomy_decisions SET outcome_at = ?, outcome_actor='policy' WHERE id = ?", storage.FormatTime(at), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) humanMove(t *testing.T, task gen.Task, from, to, actor string) {
	t.Helper()
	if _, err := e.db.Exec("UPDATE tasks SET label = ? WHERE id = ?", to, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger, actor_id) VALUES (?, ?, ?, ?, 'human', ?)`,
		uuid.NewString(), task.ID, from, to, actor); err != nil {
		t.Fatal(err)
	}
}

func (e *env) escapes(t *testing.T, task gen.Task) []gen.AutonomyEscape {
	es, err := e.q.ListAutonomyEscapesForTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestRejectedLaterRecordsEscapeOnce(t *testing.T) {
	e := setup(t)
	e.rec.SetEscapeRecorder(escape.New(e.q, nil))
	ctx := context.Background()
	task := e.task(t, "review")
	did := e.approvedAt(t, task, "review-plan", time.Hour)
	e.humanMove(t, task, "review", "work", "alice")
	e.rec.Handle(ctx, task.ID, "work")
	es := e.escapes(t, task)
	if len(es) != 1 || es[0].Source != autonomy.EscapeSourceRejected || es[0].DecisionID == nil || *es[0].DecisionID != did || es[0].Label != "review-plan" || es[0].Actor == nil || *es[0].Actor != "alice" {
		t.Fatalf("escapes = %+v", es)
	}
	// Rejected again: no duplicate.
	e.humanMove(t, task, "review", "work", "alice")
	e.rec.Handle(ctx, task.ID, "work")
	if n := len(e.escapes(t, task)); n != 1 {
		t.Fatalf("duplicate escape: %d", n)
	}
}

func TestRejectedAfterWindowIgnored(t *testing.T) {
	e := setup(t)
	e.rec.SetEscapeRecorder(escape.New(e.q, nil))
	task := e.task(t, "review")
	e.approvedAt(t, task, "review-plan", 15*24*time.Hour)
	e.humanMove(t, task, "review", "work", "alice")
	e.rec.Handle(context.Background(), task.ID, "work")
	if n := len(e.escapes(t, task)); n != 0 {
		t.Fatalf("escapes = %d", n)
	}
}

func TestReopenedRecordsEscape(t *testing.T) {
	e := setup(t)
	e.rec.SetEscapeRecorder(escape.New(e.q, nil))
	task := e.task(t, "done")
	e.approvedAt(t, task, "review", time.Hour)
	e.humanMove(t, task, "done", "work", "alice")
	e.rec.Handle(context.Background(), task.ID, "work")
	es := e.escapes(t, task)
	if len(es) != 1 || es[0].Source != autonomy.EscapeSourceReopened {
		t.Fatalf("escapes = %+v", es)
	}
}

func TestPolicyActorAndUndoMovesIgnored(t *testing.T) {
	e := setup(t)
	e.rec.SetEscapeRecorder(escape.New(e.q, nil))
	task := e.task(t, "review")
	e.approvedAt(t, task, "review-plan", time.Hour)
	e.humanMove(t, task, "review", "work", "policy")
	e.rec.Handle(context.Background(), task.ID, "work")
	// An approved move out of a non-failure transition is not a reject either.
	e.humanMove(t, task, "review", "done", "alice")
	e.rec.Handle(context.Background(), task.ID, "done")
	if _, err := e.db.Exec(`INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger, actor_id, note) VALUES (?, ?, 'done', 'review', 'human', 'alice', ?)`,
		uuid.NewString(), task.ID, autonomy.UndoNote); err != nil {
		t.Fatal(err)
	}
	e.rec.Handle(context.Background(), task.ID, "review")
	if n := len(e.escapes(t, task)); n != 0 {
		t.Fatalf("escapes = %d", n)
	}
}

func TestTwoEscapesStepDown(t *testing.T) {
	e := setup(t)
	e.setMode(t, "review-plan", "auto")
	e.rec.SetEscapeRecorder(escape.New(e.q, nil))
	for i := 0; i < 2; i++ {
		task := e.task(t, "review")
		e.approvedAt(t, task, "review-plan", time.Hour)
		e.humanMove(t, task, "review", "work", "alice")
		e.rec.Handle(context.Background(), task.ID, "work")
	}
	ls, _ := e.q.ListWorkflowLabels(context.Background(), e.wfID)
	for _, l := range ls {
		if l.Name == "review-plan" && l.AutonomyMode != "on_flags" {
			t.Fatalf("mode = %s", l.AutonomyMode)
		}
	}
}

func TestPRFeedbackSyncRejectionRecordsNoEscape(t *testing.T) {
	e := setup(t)
	e.rec.SetEscapeRecorder(escape.New(e.q, nil))
	task := e.task(t, "review")
	e.approvedAt(t, task, "review-plan", time.Hour)
	e.humanMove(t, task, "review", "work", workflow.ActorPRFeedbackSync)
	e.rec.Handle(context.Background(), task.ID, "work")
	if n := len(e.escapes(t, task)); n != 0 {
		t.Fatalf("escapes = %d", n)
	}
}
