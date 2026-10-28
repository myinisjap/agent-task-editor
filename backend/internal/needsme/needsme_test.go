package needsme

import (
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func sp(s string) *string { return &s }

func wfDef() WorkflowDef {
	return WorkflowDef{
		Labels: []gen.WorkflowLabel{
			{Name: "backlog", AgentIgnore: 1},
			{Name: "work"},
			{Name: "review"},
			{Name: "dead"},
			{Name: "done", IsTerminal: 1},
		},
		Transitions: []gen.WorkflowTransition{
			{FromLabel: "backlog", ToLabel: "work", TriggerType: "human"},
			{FromLabel: "work", ToLabel: "review", TriggerType: "agent"},
			{FromLabel: "review", ToLabel: "done", TriggerType: "human"},
			{FromLabel: "review", ToLabel: "work", TriggerType: "human"},
		},
	}
}

func TestClassify(t *testing.T) {
	now := time.Now()
	wait := &gen.AgentRun{ID: "r1", Status: "waiting_human", Feedback: sp("which db?"), CreatedAt: now}
	running := &gen.AgentRun{ID: "r2", Status: "running"}
	done := &gen.AgentRun{ID: "r3", Status: "completed"}
	cases := []struct {
		name   string
		task   gen.Task
		run    *gen.AgentRun
		want   Reason
		wantOK bool
		msg    string
	}{
		{"waiting_human", gen.Task{Label: "work"}, wait, ReasonWaitingHuman, true, "which db?"},
		{"waiting_human notes fallback", gen.Task{Label: "work"}, &gen.AgentRun{ID: "r", Status: "waiting_human", Notes: sp("max turns")}, ReasonWaitingHuman, true, "max turns"},
		{"human gate", gen.Task{Label: "review"}, nil, ReasonHumanGate, true, ""},
		{"human gate with finished run", gen.Task{Label: "review"}, done, ReasonHumanGate, true, ""},
		{"human gate live run excluded", gen.Task{Label: "review"}, running, "", false, ""},
		{"agent label not a gate", gen.Task{Label: "work"}, nil, "", false, ""},
		{"backlog excluded", gen.Task{Label: "backlog"}, nil, "", false, ""},
		{"dead end excluded", gen.Task{Label: "dead"}, nil, "", false, ""},
		{"terminal excluded", gen.Task{Label: "done"}, nil, "", false, ""},
		{"archived excluded", gen.Task{Label: "review", Archived: 1}, nil, "", false, ""},
		{"archived waiting excluded", gen.Task{Label: "work", Archived: 1}, wait, "", false, ""},
		{"paused gate excluded", gen.Task{Label: "review", Paused: 1}, nil, "", false, ""},
		{"pr conflict", gen.Task{Label: "work", GitState: "pr_open", PrMergeable: "conflicting"}, nil, ReasonPRConflict, true, ""},
		{"pr merged not conflict", gen.Task{Label: "work", GitState: "pr_merged", PrMergeable: "conflicting"}, nil, "", false, ""},
		{"pr conflict beats gate", gen.Task{Label: "review", GitState: "pr_open", PrMergeable: "conflicting"}, nil, ReasonPRConflict, true, ""},
		{"waiting beats conflict", gen.Task{Label: "review", GitState: "pr_open", PrMergeable: "conflicting"}, wait, ReasonWaitingHuman, true, "which db?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it, ok := Classify(c.task, c.run, wfDef(), nil)
			if ok != c.wantOK || it.Reason != c.want || it.Message != c.msg {
				t.Fatalf("got (%+v, %v), want reason=%q ok=%v msg=%q", it, ok, c.want, c.wantOK, c.msg)
			}
		})
	}
}

func TestClassify_EscalationReason(t *testing.T) {
	run := &gen.AgentRun{ID: "r", Status: "waiting_human", EscalationReason: sp("max_turns")}
	it, ok := Classify(gen.Task{Label: "work"}, run, wfDef(), nil)
	if !ok || it.EscalationReason != "max_turns" {
		t.Fatalf("got %+v ok=%v, want escalation reason max_turns", it, ok)
	}
	legacy := &gen.AgentRun{ID: "r", Status: "waiting_human"}
	it, _ = Classify(gen.Task{Label: "work"}, legacy, wfDef(), nil)
	if it.EscalationReason != "" {
		t.Errorf("legacy run: got %q, want empty", it.EscalationReason)
	}
}

func TestClassify_AutoApproving(t *testing.T) {
	due := time.Now().Add(time.Minute)
	pend := &gen.AutonomyDecision{ID: "d1", Label: "review", DueAt: &due}
	it, ok := Classify(gen.Task{Label: "review"}, nil, wfDef(), pend)
	if !ok || it.Reason != ReasonAutoApproving || it.DecisionID != "d1" || it.DueAt == nil {
		t.Fatalf("pending: %+v ok=%v", it, ok)
	}
	// Pending for a different label does not apply.
	other := &gen.AutonomyDecision{ID: "d2", Label: "work", DueAt: &due}
	if it, _ := Classify(gen.Task{Label: "review"}, nil, wfDef(), other); it.Reason != ReasonHumanGate {
		t.Fatalf("other label: %+v", it)
	}
	// waiting_human still wins.
	run := &gen.AgentRun{ID: "r", Status: "waiting_human"}
	if it, _ := Classify(gen.Task{Label: "review"}, run, wfDef(), pend); it.Reason != ReasonWaitingHuman {
		t.Fatalf("waiting: %+v", it)
	}
}

func TestCanUndo(t *testing.T) {
	hist := []gen.TaskLabelHistory{{FromLabel: sp("review"), ToLabel: "done", ActorID: sp("policy")}}
	gate, ok, _ := CanUndo(gen.Task{Label: "done"}, hist, wfDef())
	if !ok || gate != "review" {
		t.Fatalf("gate=%q ok=%v", gate, ok)
	}
	if _, ok, _ := CanUndo(gen.Task{Label: "done", MergeStatus: "merged"}, hist, wfDef()); ok {
		t.Fatal("merged terminal must not be undoable")
	}
	hist[0].ActorID = sp("alice")
	if _, ok, _ := CanUndo(gen.Task{Label: "done"}, hist, wfDef()); ok {
		t.Fatal("human exit must not be undoable")
	}
}
