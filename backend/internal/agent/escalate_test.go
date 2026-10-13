package agent

import (
	"context"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// assertEscalationReason fails unless the run is waiting_human with want
// recorded as its escalation_reason.
func assertEscalationReason(t *testing.T, q *gen.Queries, runID string, want EscalationReason) {
	t.Helper()
	run, err := q.GetAgentRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run %s: %v", runID, err)
	}
	if run.Status != "waiting_human" {
		t.Errorf("run %s status = %q, want waiting_human", runID, run.Status)
	}
	if run.EscalationReason == nil || *run.EscalationReason != string(want) {
		t.Errorf("run %s escalation_reason = %v, want %q", runID, run.EscalationReason, want)
	}
}
