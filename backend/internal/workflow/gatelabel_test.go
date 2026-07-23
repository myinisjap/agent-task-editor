package workflow_test

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func label(name string, sortOrder int64, agentIgnore int64) gen.WorkflowLabel {
	return gen.WorkflowLabel{Name: name, SortOrder: sortOrder, AgentIgnore: agentIgnore}
}

func TestGateLabel(t *testing.T) {
	cases := []struct {
		name      string
		labels    []gen.WorkflowLabel
		wantGate  string
		wantFirst string
	}{
		{
			name:      "empty",
			labels:    nil,
			wantGate:  "",
			wantFirst: "",
		},
		{
			// The custom-named gate case this change exists to support: a
			// workflow whose human-gate label is NOT "not_ready".
			name:      "custom-named agent_ignore gate wins",
			labels:    []gen.WorkflowLabel{label("triage", 0, 1), label("work", 1, 0), label("done", 2, 0)},
			wantGate:  "triage",
			wantFirst: "triage",
		},
		{
			// Lowest sort_order agent_ignore label wins even when a higher
			// sort_order label is also agent_ignore.
			name:      "lowest sort_order agent_ignore wins over higher",
			labels:    []gen.WorkflowLabel{label("work", 2, 0), label("backlog", 0, 1), label("hold", 1, 1)},
			wantGate:  "backlog",
			wantFirst: "backlog",
		},
		{
			// No agent_ignore label anywhere: gate is empty, first is the
			// lowest sort_order label (the callers' fallback landing spot).
			name:      "no agent_ignore falls back to first",
			labels:    []gen.WorkflowLabel{label("b", 1, 0), label("a", 0, 0), label("c", 2, 0)},
			wantGate:  "",
			wantFirst: "a",
		},
		{
			// first is the lowest sort_order label overall, independent of
			// where the gate sits.
			name:      "first is lowest sort_order overall",
			labels:    []gen.WorkflowLabel{label("start", 0, 0), label("gate", 5, 1)},
			wantGate:  "gate",
			wantFirst: "start",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gate, first := workflow.GateLabel(c.labels)
			if gate != c.wantGate {
				t.Errorf("gate = %q, want %q", gate, c.wantGate)
			}
			if first != c.wantFirst {
				t.Errorf("first = %q, want %q", first, c.wantFirst)
			}
		})
	}
}
