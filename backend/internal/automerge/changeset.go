package automerge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Change-set rules (ADR 0041 stage 6).
//
// A change-set member PR is the PR of a foreign child (child.repo_id differs
// from its parent's). Home children have no PR of their own: they merge into
// the parent branch. Merge order comes from task_dependencies edges between
// siblings; the same edges the PR bodies render.
//
// A predecessor is merged when its git_state is pr_merged (never merge_status:
// a branchless foreign child is "merged" without ever having a PR). A home
// predecessor counts as merged when the parent's PR is merged.

// ChangeSetMember names one sibling in a change set.
type ChangeSetMember struct {
	TaskID string
	Title  string
	PRURL  string
}

// ChangeSetFacts are the stored change-set facts Precheck consumes. The zero
// value (single-repo tasks) blocks nothing.
type ChangeSetFacts struct {
	// PendingPredecessors are predecessors whose PR is not merged yet.
	PendingPredecessors []ChangeSetMember
	// RedSiblings are siblings whose PR is closed unmerged or has failing checks.
	RedSiblings []ChangeSetMember
}

// PendingPredecessors applies the "merged" rules to the predecessor rows
// (ListChangeSetPredecessorsForTasks) of taskID. Archived predecessors are
// skipped. It is the single definition used by auto-merge and the task API.
func PendingPredecessors(rows []gen.ListChangeSetPredecessorsForTasksRow, taskID string) []ChangeSetMember {
	var out []ChangeSetMember
	for _, r := range rows {
		if r.TaskID != taskID || r.PredArchived != 0 {
			continue
		}
		merged := r.PredGitState == "pr_merged"
		if r.PredRepoID == r.ParentRepoID { // home child: lands through the parent PR
			merged = r.ParentGitState == "pr_merged"
		}
		if !merged {
			out = append(out, ChangeSetMember{TaskID: r.PredID, Title: r.PredTitle, PRURL: r.PredPrUrl})
		}
	}
	return out
}

// LoadChangeSetFacts reads the stored facts for task. Empty facts when the task
// is not a foreign child. No forge calls are made. (The foreign check is
// inlined: this package must not import internal/agent.)
func LoadChangeSetFacts(ctx context.Context, q *gen.Queries, task gen.Task) (ChangeSetFacts, error) {
	var facts ChangeSetFacts
	if task.ParentTaskID == nil {
		return facts, nil
	}
	parent, err := q.GetTask(ctx, *task.ParentTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return facts, nil
	}
	if err != nil {
		return facts, fmt.Errorf("get parent: %w", err)
	}
	if task.RepoID == parent.RepoID {
		return facts, nil
	}
	rows, err := q.ListChangeSetPredecessorsForTasks(ctx, []string{task.ID})
	if err != nil {
		return facts, fmt.Errorf("list predecessors: %w", err)
	}
	facts.PendingPredecessors = PendingPredecessors(rows, task.ID)

	sibs, err := q.ListSubtasks(ctx, &parent.ID)
	if err != nil {
		return facts, fmt.Errorf("list siblings: %w", err)
	}
	for _, s := range sibs {
		if s.ID == task.ID || s.Archived != 0 || s.RepoID == parent.RepoID {
			continue
		}
		red, err := siblingRed(ctx, q, s)
		if err != nil {
			return facts, err
		}
		if red {
			facts.RedSiblings = append(facts.RedSiblings, ChangeSetMember{TaskID: s.ID, Title: s.Title, PRURL: s.PrUrl})
		}
	}
	return facts, nil
}

// siblingRed: PR closed unmerged, or an open PR with failing checks at its
// current head, judged from stored facts only.
func siblingRed(ctx context.Context, q *gen.Queries, s gen.Task) (bool, error) {
	switch s.GitState {
	case "pr_closed":
		return true, nil
	case "pr_open":
	default:
		return false, nil
	}
	am, err := q.GetTaskAutoMerge(ctx, s.ID)
	if err == nil && am.Reason == ReasonChecksFailed {
		return true, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("get sibling auto-merge: %w", err)
	}
	st, err := q.GetTaskPRReviewState(ctx, s.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get sibling review state: %w", err)
	}
	if st.LastFailedCheckSha != nil && st.HeadSha != "" && strings.HasPrefix(*st.LastFailedCheckSha, st.HeadSha+"|") {
		return true, nil
	}
	return false, nil
}

// titleList renders up to two titles plus "and N more".
func titleList(ms []ChangeSetMember) string {
	var names []string
	for i, m := range ms {
		if i == 2 {
			names = append(names, fmt.Sprintf("and %d more", len(ms)-2))
			break
		}
		names = append(names, m.Title)
	}
	return strings.Join(names, ", ")
}

// MergeOrderTitles is the short title list used in order warnings.
func MergeOrderTitles(ms []ChangeSetMember) string { return titleList(ms) }
