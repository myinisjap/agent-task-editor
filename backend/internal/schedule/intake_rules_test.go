package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func createScheduleRule(t *testing.T, q *gen.Queries, mods ...func(*gen.CreateIntakeRuleParams)) gen.IntakeRule {
	t.Helper()
	p := gen.CreateIntakeRuleParams{
		ID:               uuid.NewString(),
		Name:             "schedule rule",
		Enabled:          true,
		SortOrder:        0,
		MatchSource:      "schedule",
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

func TestScheduler_RuleAppliesPriorityAndCost(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wf := seedWorkflow(t, q)
	repo := seedRepo(t, q, &wf.ID)
	tmpl := seedTemplate(t, q)
	// Leave target_label empty so the schedule uses the workflow gate.
	sched := seedSchedule(t, q, tmpl.ID, repo.ID, "* * * * *", "", true)
	backdateLastRun(t, q, sched.ID)

	cost := 3.0
	prio := int64(2)
	createScheduleRule(t, q, func(p *gen.CreateIntakeRuleParams) {
		p.ApplyPriority = &prio
		p.ApplyMaxCostUsd = &cost
	})

	pub := &recordingPub{}
	s := New(db.SQL(), pub, time.Minute)
	s.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Priority != 2 {
		t.Errorf("priority = %d, want 2", task.Priority)
	}
	if task.MaxCostUsd != 3.0 {
		t.Errorf("max_cost_usd = %v, want 3.0", task.MaxCostUsd)
	}
	if task.MatchedRuleID == nil {
		t.Fatal("expected matched_rule_id to be set")
	}
}

func TestScheduler_ExplicitTargetLabelBeatsRule(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wf := seedWorkflow(t, q)
	repo := seedRepo(t, q, &wf.ID)
	tmpl := seedTemplate(t, q)
	// Schedule explicitly targets "done" (a real, if unusual, choice for
	// this test) — the rule's target_label must NOT override it.
	sched := seedSchedule(t, q, tmpl.ID, repo.ID, "* * * * *", "done", true)
	backdateLastRun(t, q, sched.ID)

	createScheduleRule(t, q, func(p *gen.CreateIntakeRuleParams) {
		p.ApplyTargetLabel = "not_ready"
	})

	pub := &recordingPub{}
	s := New(db.SQL(), pub, time.Minute)
	s.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Label != "done" {
		t.Errorf("label = %q, want done (schedule's explicit target_label must win over the rule)", tasks[0].Label)
	}
}

func TestScheduler_RuleSuppliesTargetLabelWhenScheduleLeavesItEmpty(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wf := seedWorkflow(t, q)
	repo := seedRepo(t, q, &wf.ID)
	tmpl := seedTemplate(t, q)
	sched := seedSchedule(t, q, tmpl.ID, repo.ID, "* * * * *", "", true)
	backdateLastRun(t, q, sched.ID)

	createScheduleRule(t, q, func(p *gen.CreateIntakeRuleParams) {
		p.ApplyTargetLabel = "done"
	})

	pub := &recordingPub{}
	s := New(db.SQL(), pub, time.Minute)
	s.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Label != "done" {
		t.Errorf("label = %q, want done (rule's target_label used since the schedule left its own empty)", tasks[0].Label)
	}
}
