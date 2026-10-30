package propose

import (
	"context"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Approval is a clean policy approval in the window: one nobody corrected.
type Approval struct {
	TaskID  string
	RepoID  string
	Facts   autonomy.Facts
	FactsOK bool
}

const escapeScanLimit = 500

// gatherEvidence collects the gate's escapes and human rejections in the
// window, one entry per task (an escape wins over a rejection), newest first,
// capped at MaxEvidence. It also returns every task with an escape at the
// gate in the window, which never counts as a clean approval.
func (s *Service) gatherEvidence(ctx context.Context, wfID, label string, since time.Time) ([]Evidence, map[string]bool, error) {
	escapes, err := s.Q.ListGateEscapes(ctx, gen.ListGateEscapesParams{WorkflowID: wfID, Label: label, Limit: escapeScanLimit})
	if err != nil {
		return nil, nil, err
	}
	transitions, err := s.Q.ListWorkflowTransitions(ctx, wfID)
	if err != nil {
		return nil, nil, err
	}
	failure := map[string]bool{}
	for _, t := range transitions {
		if t.FromLabel == label && t.TriggerType == "human" && t.Path != nil && *t.Path == "failure" {
			failure[t.ToLabel] = true
		}
	}
	lbl := label
	exits, err := s.Q.ListGateRejections(ctx, gen.ListGateRejectionsParams{
		WorkflowID: wfID, Label: &lbl, Since: storage.FormatTime(since), MaxRows: MaxCleanApprovals,
	})
	if err != nil {
		return nil, nil, err
	}

	escaped := map[string]bool{}
	seen := map[string]bool{}
	repos := map[string]string{}
	var out []Evidence
	add := func(taskID, kind, source, note string, at time.Time) {
		if seen[taskID] || len(out) >= MaxEvidence {
			return
		}
		task, err := s.Q.GetTask(ctx, taskID)
		if err != nil {
			return
		}
		seen[taskID] = true
		facts, ok := s.facts(ctx, task, at)
		facts.TaskType = task.Type
		out = append(out, Evidence{
			TaskID: task.ID, Title: task.Title, Kind: kind, Source: source, Note: note,
			RepoID: task.RepoID, RepoName: s.repoName(ctx, repos, task.RepoID), Facts: facts, FactsOK: ok,
		})
	}
	for _, e := range escapes {
		if e.CreatedAt.Before(since) {
			continue
		}
		escaped[e.TaskID] = true
		at := e.CreatedAt
		// The rule would have fired when the policy approved, so replay the
		// facts as of that approval.
		if e.DecisionID != nil {
			if d, err := s.Q.GetAutonomyDecision(ctx, *e.DecisionID); err == nil && d.OutcomeAt != nil {
				at = *d.OutcomeAt
			}
		}
		note := ""
		if e.Note != nil {
			note = *e.Note
		}
		add(e.TaskID, KindEscape, e.Source, note, at)
	}
	for _, x := range exits {
		if !failure[x.ToLabel] {
			continue
		}
		add(x.TaskID, KindRejected, "", x.Note, x.CreatedAt)
	}
	return out, escaped, nil
}

func (s *Service) repoName(ctx context.Context, cache map[string]string, id string) string {
	if n, ok := cache[id]; ok {
		return n
	}
	n := ""
	if r, err := s.Q.GetRepo(ctx, id); err == nil {
		n = r.Name
	}
	cache[id] = n
	return n
}

// cleanApprovals are the gate's policy approvals in the window whose task
// never escaped: what a new rule would cost in extra asking.
func (s *Service) cleanApprovals(ctx context.Context, wfID, label string, since time.Time, escaped map[string]bool) ([]Approval, error) {
	rows, err := s.Q.ListPolicyApprovalsAtGate(ctx, gen.ListPolicyApprovalsAtGateParams{
		WorkflowID: wfID, Label: label, OutcomeAt: &since, Limit: MaxCleanApprovals,
	})
	if err != nil {
		return nil, err
	}
	out := []Approval{}
	for _, d := range rows {
		if escaped[d.TaskID] || d.Outcome == nil || *d.Outcome != "approved" {
			continue
		}
		task, err := s.Q.GetTask(ctx, d.TaskID)
		if err != nil {
			continue
		}
		at := s.clock()
		if d.OutcomeAt != nil {
			at = *d.OutcomeAt
		}
		facts, ok := s.facts(ctx, task, at)
		facts.TaskType = task.Type
		out = append(out, Approval{TaskID: task.ID, RepoID: task.RepoID, Facts: facts, FactsOK: ok})
	}
	return out, nil
}
