package tasksource

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// createRule is a small test helper around gen.CreateIntakeRule with
// sensible zero-value defaults for fields a given test doesn't care about.
func createRule(t *testing.T, q *gen.Queries, mods ...func(*gen.CreateIntakeRuleParams)) gen.IntakeRule {
	t.Helper()
	p := gen.CreateIntakeRuleParams{
		ID:               uuid.NewString(),
		Name:             "test rule",
		Enabled:          true,
		SortOrder:        0,
		MatchSource:      "issue",
		MatchLabels:      "[]",
		MatchAuthorAssoc: "[]",
	}
	for _, m := range mods {
		m(&p)
	}
	rule, err := q.CreateIntakeRule(context.Background(), p)
	if err != nil {
		t.Fatalf("create intake rule: %v", err)
	}
	return rule
}

func TestImporter_RuleAppliesLabelPriorityCost(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repo := seedRepo(t, q, 1, true)

	// Add a second, agent-triggerable label the rule will target, wired with
	// an agent transition from the gate so it's a legitimate landing spot.
	workLabelID := uuid.NewString()
	if _, err := q.CreateWorkflowLabel(context.Background(), gen.CreateWorkflowLabelParams{
		ID: workLabelID, WorkflowID: *repo.WorkflowID, Name: "work", Color: "#111", SortOrder: 1, AgentIgnore: 0,
	}); err != nil {
		t.Fatalf("create work label: %v", err)
	}

	cost := 12.5
	prio := int64(1)
	createRule(t, q, func(p *gen.CreateIntakeRuleParams) {
		p.MatchLabels = `["bug"]`
		p.MatchAuthorAssoc = `["OWNER"]`
		p.ApplyTargetLabel = "work"
		p.ApplyPriority = &prio
		p.ApplyMaxCostUsd = &cost
	})

	src := fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Fix crash", Body: "boom", Labels: []string{"bug"}, AuthorAssoc: "OWNER"},
	}}
	pub := &recordingPub{}
	im := New(db.SQL(), pub, time.Minute, src)
	im.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Label != "work" {
		t.Errorf("label = %q, want work (rule's apply_target_label)", task.Label)
	}
	if task.Priority != 1 {
		t.Errorf("priority = %d, want 1", task.Priority)
	}
	if task.MaxCostUsd != 12.5 {
		t.Errorf("max_cost_usd = %v, want 12.5", task.MaxCostUsd)
	}
	if task.MatchedRuleID == nil {
		t.Fatal("expected matched_rule_id to be set")
	}
}

func TestImporter_AutoStartDeniedWithoutTrustedAuthor(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repo := seedRepo(t, q, 1, true)

	if _, err := q.CreateWorkflowLabel(context.Background(), gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: *repo.WorkflowID, Name: "work", Color: "#111", SortOrder: 1, AgentIgnore: 0,
	}); err != nil {
		t.Fatalf("create work label: %v", err)
	}

	// A rule that would auto-start (non-gate target label) but declares NO
	// author-association constraint — the CRUD handler should reject this at
	// write time, but the importer must also defensively refuse to honour it
	// (belt and braces: never let untrusted issue content bypass the human
	// gate just because a bad rule made it into the DB).
	createRule(t, q, func(p *gen.CreateIntakeRuleParams) {
		p.MatchLabels = `["bug"]`
		p.ApplyTargetLabel = "work"
	})

	src := fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Fix crash", Labels: []string{"bug"}, AuthorAssoc: "NONE"},
	}}
	pub := &recordingPub{}
	im := New(db.SQL(), pub, time.Minute, src)
	im.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Label != "triage" {
		t.Errorf("label = %q, want triage (fallback to gate — auto-start must be denied)", tasks[0].Label)
	}
}

func TestImporter_InvalidTargetLabelFallsBackToGate(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepo(t, q, 1, true)

	createRule(t, q, func(p *gen.CreateIntakeRuleParams) {
		p.MatchLabels = `["bug"]`
		p.MatchAuthorAssoc = `["OWNER"]`
		p.ApplyTargetLabel = "does-not-exist"
	})

	src := fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Fix crash", Labels: []string{"bug"}, AuthorAssoc: "OWNER"},
	}}
	pub := &recordingPub{}
	im := New(db.SQL(), pub, time.Minute, src)
	im.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Label != "triage" {
		t.Errorf("label = %q, want triage (fallback to gate for an invalid target label)", tasks[0].Label)
	}
}

// TestImporter_DoubleSweepNoDriftWithTemplate is the regression test for the
// update-thrash hazard: a rule that applies a template shapes the task's
// type/description at creation, and a second sweep over the same
// (unchanged) issue must not treat that shaping as drift and "correct" it
// back — which would otherwise flip-flop every sweep and spam task.updated.
func TestImporter_DoubleSweepNoDriftWithTemplate(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepo(t, q, 1, true)

	tmpl, err := q.CreateTaskTemplate(context.Background(), gen.CreateTaskTemplateParams{
		ID:          uuid.NewString(),
		Name:        "triage template",
		Title:       "",
		Description: "Please triage per SOP.",
		Type:        "bug",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	createRule(t, q, func(p *gen.CreateIntakeRuleParams) {
		p.MatchLabels = `["bug"]`
		p.ApplyTemplateID = &tmpl.ID
	})

	src := fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Fix crash", Body: "It crashes.", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug"}},
	}}
	pub := &recordingPub{}
	im := New(db.SQL(), pub, time.Minute, src)

	ctx := context.Background()
	im.Sweep(ctx)

	tasksAfterFirst, err := q.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasksAfterFirst) != 1 {
		t.Fatalf("expected 1 task after first sweep, got %d", len(tasksAfterFirst))
	}
	first := tasksAfterFirst[0]
	if first.Type != "bug" {
		t.Fatalf("expected template type 'bug' applied at creation, got %q", first.Type)
	}

	// Second sweep over the exact same (unchanged) issue must not update
	// the task — no drift, no event.
	pub.events = nil
	im.Sweep(ctx)

	tasksAfterSecond, err := q.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasksAfterSecond) != 1 {
		t.Fatalf("expected still 1 task after second sweep, got %d", len(tasksAfterSecond))
	}
	second := tasksAfterSecond[0]
	if second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("task was updated on the second sweep of an unchanged issue (drift thrash): first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
	for _, e := range pub.events {
		if e == "task.updated" {
			t.Errorf("unexpected task.updated event on second sweep of unchanged issue: %v", pub.events)
		}
	}
}
