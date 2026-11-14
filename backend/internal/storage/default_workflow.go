package storage

import (
	"context"
	"errors"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// PickDefaultWorkflow is the single source of truth for the default workflow
// (task creation, the is_default API field, and therefore the frontend):
//  1. the workflow named exactly "Default";
//  2. else the lowest (name, id) workflow other than the seeded PR review one;
//  3. else the PR review workflow if it is the only one;
//  4. else none.
//
// The PR review workflow is recognised by name; a renamed one is treated like
// any other workflow. The result does not depend on input order.
func PickDefaultWorkflow(wfs []gen.Workflow) (gen.Workflow, bool) {
	var best, pr gen.Workflow
	haveBest, havePR := false, false
	for _, w := range wfs {
		if w.Name == "Default" {
			return w, true
		}
		if w.Name == PRReviewWorkflowName {
			if !havePR || w.ID < pr.ID {
				pr, havePR = w, true
			}
			continue
		}
		if !haveBest || w.Name < best.Name || (w.Name == best.Name && w.ID < best.ID) {
			best, haveBest = w, true
		}
	}
	if haveBest {
		return best, true
	}
	return pr, havePR
}

// DefaultWorkflowID returns the id of the default workflow (see
// PickDefaultWorkflow).
func DefaultWorkflowID(ctx context.Context, q *gen.Queries) (string, error) {
	wfs, err := q.ListWorkflows(ctx)
	if err != nil {
		return "", errors.New("failed to load workflows")
	}
	wf, ok := PickDefaultWorkflow(wfs)
	if !ok {
		return "", errors.New("no workflows configured")
	}
	return wf.ID, nil
}
