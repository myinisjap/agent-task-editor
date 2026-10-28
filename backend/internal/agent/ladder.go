package agent

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// LadderRung is one escalation rung. Rung 0 is implicit (the agent config's
// own provider/effort); escalation_ladder[i] is rung i+1.
type LadderRung struct {
	ProviderConfigID          string   `json:"provider_config_id"`
	Effort                    string   `json:"effort,omitempty"`
	FallbackProviderConfigIDs []string `json:"fallback_provider_config_ids,omitempty"`
}

// ParseLadder decodes agent_configs.escalation_ladder; empty or malformed
// input yields nil (no ladder).
func ParseLadder(raw string) []LadderRung {
	if raw == "" {
		return nil
	}
	var l []LadderRung
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return nil
	}
	return l
}

// selectRung maps a failed-attempt count to a rung index, capped at the last.
func selectRung(failed, ladderLen int) int {
	if failed < 0 {
		failed = 0
	}
	if failed > ladderLen {
		return ladderLen
	}
	return failed
}

// reworkBounces counts how many times the task bounced back into label via a
// failure-path transition, walking newest to oldest. A human-triggered row or
// a fresh (non-failure) arrival into label resets the count.
func reworkBounces(hist []gen.TaskLabelHistory, label string, isFailureEdge func(from, to string) bool) int {
	n := 0
	for i := len(hist) - 1; i >= 0; i-- {
		h := hist[i]
		if h.Trigger == string(workflow.TriggerHuman) {
			break
		}
		if h.ToLabel != label {
			continue
		}
		if h.FromLabel != nil && isFailureEdge(*h.FromLabel, label) {
			n++
			continue
		}
		break
	}
	return n
}

// rungProviderIDs lists the providers to try for a rung, in order. Rung 0 is
// exactly agentProviderIDs(cfg); a higher rung tries its own provider and
// fallbacks first, then the agent's own list.
func rungProviderIDs(cfg *gen.AgentConfig, ladder []LadderRung, rung int) []string {
	base := agentProviderIDs(cfg)
	if rung <= 0 || rung > len(ladder) {
		return base
	}
	r := ladder[rung-1]
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(r.ProviderConfigID)
	for _, id := range r.FallbackProviderConfigIDs {
		add(id)
	}
	for _, id := range base {
		add(id)
	}
	return out
}

// rungEffort returns the effort for a rung (inherits the agent's effort).
func rungEffort(cfg gen.AgentConfig, ladder []LadderRung, rung int) string {
	if rung <= 0 || rung > len(ladder) || ladder[rung-1].Effort == "" {
		return cfg.Effort
	}
	return ladder[rung-1].Effort
}

// failedAttempts is the number of failed attempts on the task's current stage:
// the consecutive-failure breaker counter plus rework bounce-backs into the
// stage. Best-effort: lookup errors degrade to the breaker counter alone.
func (d *Dispatcher) failedAttempts(ctx context.Context, t gen.Task) int {
	n := int(t.ConsecutiveFailureCount)
	hist, err := d.q.ListTaskLabelHistory(ctx, t.ID)
	if err != nil {
		slog.Warn("dispatcher: ladder: list label history", "component", "dispatcher", "task_id", t.ID, "err", err)
		return n
	}
	trs, err := d.q.ListWorkflowTransitions(ctx, t.WorkflowID)
	if err != nil {
		slog.Warn("dispatcher: ladder: list transitions", "component", "dispatcher", "task_id", t.ID, "err", err)
		return n
	}
	failure := map[[2]string]bool{}
	for _, tr := range trs {
		if tr.Path != nil && *tr.Path == "failure" {
			failure[[2]string{tr.FromLabel, tr.ToLabel}] = true
		}
	}
	return n + reworkBounces(hist, t.Label, func(from, to string) bool { return failure[[2]string{from, to}] })
}
