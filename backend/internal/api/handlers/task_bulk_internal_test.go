package handlers

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// TestBulkErrorMessage covers each of bulkErrorMessage's known-error
// translations (used by Bulk to report a per-task reason without leaking
// raw internal error text for the common/expected cases) plus its
// fallback to err.Error() for anything unrecognized.
func TestBulkErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"sql no rows", sql.ErrNoRows, "task not found"},
		{"workflow task not found", workflow.ErrTaskNotFound, "task not found"},
		{"no transition", workflow.ErrNoTransition, "no transition defined between these labels"},
		{"gate required", workflow.ErrGateRequired, "transition requires human approval"},
		{"agent ignored", workflow.ErrAgentIgnored, "destination label is ignored by agents"},
		{"stale", workflow.ErrStale, "task label changed concurrently; refresh and retry"},
		{"run live", errRunLive, errRunLive.Error()},
		{"unrecognized error falls back to Error()", errors.New("boom"), "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bulkErrorMessage(tc.err); got != tc.want {
				t.Errorf("bulkErrorMessage(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
