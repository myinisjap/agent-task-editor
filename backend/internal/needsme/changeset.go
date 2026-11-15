package needsme

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// ReasonChangeSetPartial means part of a multi-repo change set merged while a
// sibling's PR did not (ADR 0041 stage 6). It is reported on the parent task.
// ATE never reverts merged work: a human reruns the failed child (moves it
// back to a work label) or archives it, which clears the reason.
const ReasonChangeSetPartial Reason = "change_set_partial"

// ChangeSetPR states.
const (
	ChangeSetMerged     = "pr_merged"
	ChangeSetClosed     = "pr_closed"
	ChangeSetLandFailed = "land_failed"
)

// ChangeSetPR is one member PR in a partial change set.
type ChangeSetPR struct {
	TaskID string
	Title  string
	RepoID string
	PRURL  string
	// State is pr_merged, pr_closed or land_failed.
	State string
}

// ChangeSetPartial lists which PRs of a change set merged and which failed.
type ChangeSetPartial struct {
	Merged []ChangeSetPR
	Failed []ChangeSetPR
	// Since is the latest update among the failed children.
	Since time.Time
}

// ClassifyChangeSet is the single definition of "partial change set".
//
// Members are the parent's foreign children (repo differs from the parent's;
// home children have no PR of their own). A member is merged when its
// git_state is pr_merged (never merge_status: a branchless child reads merged
// without a PR). A non-archived member on a terminal label has failed when its
// PR was closed unmerged, or its PR never landed and its terminal/create_pr
// side effect failed permanently. The change set is partial when at least one
// member merged and at least one failed. Moving a failed child back to a work
// label or archiving it clears the state.
//
// terminal and landFailed are keyed by child task id: terminal = the child sits
// on a terminal label of its workflow; landFailed = it has a permanently
// failed land side effect.
func ClassifyChangeSet(parent gen.Task, children []gen.Task, terminal, landFailed map[string]bool) (*ChangeSetPartial, bool) {
	if parent.Archived != 0 {
		return nil, false
	}
	cs := &ChangeSetPartial{}
	for _, c := range children {
		if c.RepoID == parent.RepoID || c.ParentTaskID == nil || *c.ParentTaskID != parent.ID {
			continue
		}
		pr := ChangeSetPR{TaskID: c.ID, Title: c.Title, RepoID: c.RepoID, PRURL: c.PrUrl}
		switch {
		case c.GitState == "pr_merged":
			pr.State = ChangeSetMerged
			cs.Merged = append(cs.Merged, pr)
		case c.Archived != 0 || !terminal[c.ID]:
		case c.GitState == "pr_closed":
			pr.State = ChangeSetClosed
			cs.Failed = append(cs.Failed, pr)
			cs.Since = laterOf(cs.Since, c.UpdatedAt)
		case c.GitState != "pr_open" && landFailed[c.ID]:
			pr.State = ChangeSetLandFailed
			cs.Failed = append(cs.Failed, pr)
			cs.Since = laterOf(cs.Since, c.UpdatedAt)
		}
	}
	if len(cs.Merged) == 0 || len(cs.Failed) == 0 {
		return nil, false
	}
	return cs, true
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// ChangeSetMessage is the human-readable summary used for the needs-me item
// and the task.needs_human event.
func ChangeSetMessage(cs *ChangeSetPartial) string {
	part := func(ps []ChangeSetPR, label func(ChangeSetPR) string) string {
		out := make([]string, 0, len(ps))
		for _, p := range ps {
			out = append(out, label(p))
		}
		return strings.Join(out, ", ")
	}
	merged := part(cs.Merged, func(p ChangeSetPR) string {
		s := p.RepoID + " · " + p.Title
		if p.PRURL != "" {
			s += " (" + p.PRURL + ")"
		}
		return s
	})
	failed := part(cs.Failed, func(p ChangeSetPR) string {
		why := "PR closed"
		if p.State == ChangeSetLandFailed {
			why = "PR could not be opened"
		}
		return fmt.Sprintf("%s · %s (%s)", p.RepoID, p.Title, why)
	})
	return fmt.Sprintf("Change set partly merged. Merged: %s. Not merged: %s.", merged, failed)
}

// classifyChildren sorts children stably, resolves terminal/land-failed facts
// with queries and runs ClassifyChangeSet.
func classifyParent(ctx context.Context, q *gen.Queries, parent gen.Task, children []gen.Task, wfLabels func(string) ([]gen.WorkflowLabel, error)) (*ChangeSetPartial, bool, error) {
	var foreign []gen.Task
	merged := false
	for _, c := range children {
		if c.RepoID == parent.RepoID {
			continue
		}
		foreign = append(foreign, c)
		if c.GitState == "pr_merged" {
			merged = true
		}
	}
	if !merged {
		return nil, false, nil
	}
	sort.SliceStable(foreign, func(i, j int) bool {
		if !foreign[i].CreatedAt.Equal(foreign[j].CreatedAt) {
			return foreign[i].CreatedAt.Before(foreign[j].CreatedAt)
		}
		return foreign[i].ID < foreign[j].ID
	})
	terminal := map[string]bool{}
	var candidates []string
	for _, c := range foreign {
		if c.Archived != 0 || c.GitState == "pr_merged" {
			continue
		}
		labels, err := wfLabels(c.WorkflowID)
		if err != nil {
			return nil, false, err
		}
		for _, l := range labels {
			if l.Name == c.Label && l.IsTerminal != 0 {
				terminal[c.ID] = true
			}
		}
		if terminal[c.ID] && c.GitState != "pr_open" && c.GitState != "pr_closed" {
			candidates = append(candidates, c.ID)
		}
	}
	landFailed := map[string]bool{}
	if len(candidates) > 0 {
		rows, err := q.ListTaskSideEffectsForTasks(ctx, candidates)
		if err != nil {
			return nil, false, err
		}
		for _, r := range rows {
			if r.Status == "failed" && (r.Kind == workflow.SideEffectTerminal || r.Kind == workflow.SideEffectCreatePR) {
				landFailed[r.TaskID] = true
			}
		}
	}
	cs, ok := ClassifyChangeSet(parent, foreign, terminal, landFailed)
	return cs, ok, nil
}

// ChangeSetPartialFor loads one parent and classifies its change set with the
// same definition Compute uses.
func ChangeSetPartialFor(ctx context.Context, q *gen.Queries, parentID string) (*ChangeSetPartial, gen.Task, bool, error) {
	parent, err := q.GetTask(ctx, parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gen.Task{}, false, nil
	}
	if err != nil {
		return nil, gen.Task{}, false, err
	}
	children, err := q.ListSubtasks(ctx, &parentID)
	if err != nil {
		return nil, parent, false, err
	}
	cache := map[string][]gen.WorkflowLabel{}
	cs, ok, err := classifyParent(ctx, q, parent, children, func(id string) ([]gen.WorkflowLabel, error) {
		if l, hit := cache[id]; hit {
			return l, nil
		}
		l, err := q.ListWorkflowLabels(ctx, id)
		cache[id] = l
		return l, err
	})
	return cs, parent, ok, err
}

// Publisher publishes WebSocket events.
type Publisher interface {
	Publish(eventType string, payload any)
}

// ChangeSetAnnouncer publishes a task.needs_human event when a parent becomes
// (or changes how it is) partially merged. The event is edge-triggered; the
// needs-me list itself is derived from state, so a missed or repeated event
// never changes it. Dedupe is in memory only: after a restart a still-partial
// parent can announce once more on the next trigger (notify debounces repeats).
type ChangeSetAnnouncer struct {
	q    *gen.Queries
	pub  Publisher
	mu   sync.Mutex
	last map[string]string
}

// NewChangeSetAnnouncer builds an announcer.
func NewChangeSetAnnouncer(q *gen.Queries, pub Publisher) *ChangeSetAnnouncer {
	return &ChangeSetAnnouncer{q: q, pub: pub, last: map[string]string{}}
}

// Check re-evaluates one parent and publishes when it newly is partial.
// Nil-safe; errors are swallowed (the derived list stays correct).
func (a *ChangeSetAnnouncer) Check(ctx context.Context, parentID string) {
	if a == nil || a.q == nil {
		return
	}
	cs, parent, ok, err := ChangeSetPartialFor(ctx, a.q, parentID)
	if err != nil {
		return
	}
	a.mu.Lock()
	if !ok || parent.Archived != 0 {
		delete(a.last, parentID)
		a.mu.Unlock()
		return
	}
	sig := signature(cs)
	if a.last[parentID] == sig {
		a.mu.Unlock()
		return
	}
	a.last[parentID] = sig
	a.mu.Unlock()
	if a.pub != nil {
		a.pub.Publish(wsevent.TypeTaskNeedsHuman, wsevent.TaskNeedsHuman{
			TaskID: parentID, RunID: "", Reason: string(ReasonChangeSetPartial), Message: ChangeSetMessage(cs),
		})
	}
}

func signature(cs *ChangeSetPartial) string {
	ids := func(ps []ChangeSetPR) string {
		s := make([]string, 0, len(ps))
		for _, p := range ps {
			s = append(s, p.TaskID+":"+p.State)
		}
		sort.Strings(s)
		return strings.Join(s, ",")
	}
	return ids(cs.Merged) + "|" + ids(cs.Failed)
}
