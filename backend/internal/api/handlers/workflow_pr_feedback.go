package handlers

import (
	"fmt"
	"strings"
)

type prFeedbackLabel struct {
	Name, Target          string
	AgentIgnore, Terminal bool
}

type prFeedbackTransition struct{ From, To, Trigger string }

// validateLabelPRFeedback validates each label's pr_feedback_label and returns
// the trimmed targets, index-aligned with labels.
func validateLabelPRFeedback(labels []prFeedbackLabel, transitions []prFeedbackTransition) ([]string, error) {
	byName := make(map[string]prFeedbackLabel, len(labels))
	for _, l := range labels {
		byName[l.Name] = l
	}
	out := make([]string, len(labels))
	for i, l := range labels {
		target := strings.TrimSpace(l.Target)
		if target == "" {
			continue
		}
		t, ok := byName[target]
		if !ok {
			return nil, fmt.Errorf("label %q: pr_feedback_label %q is not a label in this workflow", l.Name, target)
		}
		if target == l.Name {
			return nil, fmt.Errorf("label %q: pr_feedback_label must not be the label itself", l.Name)
		}
		if t.Terminal {
			return nil, fmt.Errorf("label %q: pr_feedback_label %q must not be a terminal label", l.Name, target)
		}
		if t.AgentIgnore {
			return nil, fmt.Errorf("label %q: pr_feedback_label %q must not be an agent_ignore label", l.Name, target)
		}
		found := false
		for _, tr := range transitions {
			if tr.From == l.Name && tr.To == target && (tr.Trigger == "human" || tr.Trigger == "both") {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("label %q: pr_feedback_label %q needs a human transition from %q to %q", l.Name, target, l.Name, target)
		}
		out[i] = target
	}
	return out, nil
}
