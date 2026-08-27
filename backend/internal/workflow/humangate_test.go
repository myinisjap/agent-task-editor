package workflow_test

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func transition(from, to, trigger string) gen.WorkflowTransition {
	return gen.WorkflowTransition{FromLabel: from, ToLabel: to, TriggerType: trigger}
}

func gateLabel(name string, isTerminal, agentIgnore int64) gen.WorkflowLabel {
	return gen.WorkflowLabel{Name: name, IsTerminal: isTerminal, AgentIgnore: agentIgnore}
}

func TestIsHumanGateLabel(t *testing.T) {
	cases := []struct {
		name        string
		labels      []gen.WorkflowLabel
		transitions []gen.WorkflowTransition
		labelName   string
		want        bool
	}{
		{
			name:      "unknown label",
			labels:    []gen.WorkflowLabel{gateLabel("todo", 0, 0)},
			labelName: "missing",
			want:      false,
		},
		{
			name:   "terminal label is never a gate",
			labels: []gen.WorkflowLabel{gateLabel("done", 1, 0)},
			transitions: []gen.WorkflowTransition{
				transition("done", "archived", "human"),
			},
			labelName: "done",
			want:      false,
		},
		{
			name:      "agent_ignore is always a gate, even with no transitions",
			labels:    []gen.WorkflowLabel{gateLabel("not_ready", 0, 1)},
			labelName: "not_ready",
			want:      true,
		},
		{
			name:   "all outgoing transitions human is a gate",
			labels: []gen.WorkflowLabel{gateLabel("review", 0, 0)},
			transitions: []gen.WorkflowTransition{
				transition("review", "approved", "human"),
				transition("review", "rejected", "human"),
			},
			labelName: "review",
			want:      true,
		},
		{
			name:   "mixed human/agent outgoing is not a gate",
			labels: []gen.WorkflowLabel{gateLabel("review", 0, 0)},
			transitions: []gen.WorkflowTransition{
				transition("review", "approved", "human"),
				transition("review", "retry", "agent"),
			},
			labelName: "review",
			want:      false,
		},
		{
			name:   "all agent outgoing is not a gate",
			labels: []gen.WorkflowLabel{gateLabel("in_progress", 0, 0)},
			transitions: []gen.WorkflowTransition{
				transition("in_progress", "done", "agent"),
			},
			labelName: "in_progress",
			want:      false,
		},
		{
			name:      "zero outgoing transitions is not a gate",
			labels:    []gen.WorkflowLabel{gateLabel("dead_end", 0, 0)},
			labelName: "dead_end",
			want:      false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := workflow.IsHumanGateLabel(c.labels, c.transitions, c.labelName)
			if got != c.want {
				t.Errorf("IsHumanGateLabel(%q) = %v, want %v", c.labelName, got, c.want)
			}
		})
	}
}
