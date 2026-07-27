package workflow_test

import (
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func trans(from, to, trigger string, path *string) gen.WorkflowTransition {
	return gen.WorkflowTransition{FromLabel: from, ToLabel: to, TriggerType: trigger, Path: path}
}

func strp(s string) *string { return &s }

func TestSuccessTarget(t *testing.T) {
	cases := []struct {
		name        string
		transitions []gen.WorkflowTransition
		from        string
		wantTarget  string
		wantOK      bool
	}{
		{
			name: "nil path matches success",
			transitions: []gen.WorkflowTransition{
				trans("work", "review", "agent", nil),
			},
			from:       "work",
			wantTarget: "review",
			wantOK:     true,
		},
		{
			name: "explicit success path matches",
			transitions: []gen.WorkflowTransition{
				trans("work", "review", "agent", strp("success")),
				trans("work", "not_ready", "agent", strp("failure")),
			},
			from:       "work",
			wantTarget: "review",
			wantOK:     true,
		},
		{
			name: "either path matches",
			transitions: []gen.WorkflowTransition{
				trans("work", "review", "agent", strp("either")),
			},
			from:       "work",
			wantTarget: "review",
			wantOK:     true,
		},
		{
			name: "human transitions are ignored",
			transitions: []gen.WorkflowTransition{
				trans("review", "done", "human", nil),
			},
			from:       "review",
			wantTarget: "",
			wantOK:     false,
		},
		{
			name: "ambiguous success transitions -> not ok",
			transitions: []gen.WorkflowTransition{
				trans("work", "review", "agent", nil),
				trans("work", "agent-review", "agent", nil),
			},
			from:       "work",
			wantTarget: "",
			wantOK:     false,
		},
		{
			name:        "no matching transitions",
			transitions: []gen.WorkflowTransition{trans("other", "review", "agent", nil)},
			from:        "work",
			wantTarget:  "",
			wantOK:      false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target, ok := workflow.SuccessTarget(c.transitions, c.from)
			if ok != c.wantOK || target != c.wantTarget {
				t.Errorf("SuccessTarget() = (%q, %v), want (%q, %v)", target, ok, c.wantTarget, c.wantOK)
			}
		})
	}
}
