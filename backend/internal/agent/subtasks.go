package agent

import (
	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"

	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// SubtaskCoordinator owns the child→parent branch merge-back lifecycle
// (Mechanism 2). When a child reaches a terminal label, its branch is merged
// back into the parent's branch (a plain merge commit); the child's worktree and
// local branch are then removed. Children never push to origin or open PRs — the
// parent's branch is the only outward-facing artifact. Once every child is
// terminal and merged (or conflicted), the parent is marked integration_status=
// pending and the dispatcher runs its agent with an integration brief; a
// conflicting merge-back is flagged and resolved by that run.
type SubtaskCoordinator struct {
	q      *gen.Queries
	engine *workflow.Engine
	pub    Publisher

	GitName  string
	GitEmail string

	// Per-parent serialization: children reaching terminal concurrently must not
	// merge into the same parent worktree at once (git would race). All merge-back
	// / evaluate work for a given parent runs under its lock, so merges apply one
	// at a time in completion order.
	mu     sync.Mutex
	plocks map[string]*sync.Mutex
}

// NewSubtaskCoordinator builds a coordinator. gitName/gitEmail author the
// merge commits.
func NewSubtaskCoordinator(q *gen.Queries, engine *workflow.Engine, pub Publisher, gitName, gitEmail string) *SubtaskCoordinator {
	return &SubtaskCoordinator{q: q, engine: engine, pub: pub, GitName: gitName, GitEmail: gitEmail, plocks: map[string]*sync.Mutex{}}
}

// parentLock returns the per-parent mutex, creating it on first use.
func (c *SubtaskCoordinator) parentLock(parentID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.plocks == nil {
		c.plocks = map[string]*sync.Mutex{}
	}
	l := c.plocks[parentID]
	if l == nil {
		l = &sync.Mutex{}
		c.plocks[parentID] = l
	}
	return l
}

// IsSubtask reports whether a task is a child (has a parent). The pool uses this
// to skip the normal per-run push to origin for children.
func IsSubtask(t gen.Task) bool { return t.ParentTaskID != nil && *t.ParentTaskID != "" }

// OnChildTerminal is invoked when a subtask reaches a terminal label (from
// engine.OnTerminal). It merges the child's branch back into the parent's branch
// and re-evaluates the parent. If the parent has a run in flight the merge is
// deferred (merge_status=pending) and retried when that run finishes
// (FlushParent).
func (c *SubtaskCoordinator) OnChildTerminal(ctx context.Context, child gen.Task, repoPath string) {
	log := slog.With("component", "subtasks", "task_id", child.ID)
	if child.ParentTaskID == nil {
		return
	}
	lock := c.parentLock(*child.ParentTaskID)
	lock.Lock()
	defer lock.Unlock()

	parent, err := c.q.GetTask(ctx, *child.ParentTaskID)
	if err != nil {
		// Parent gone (deleted/orphaned): nothing to merge back into. Tear the
		// child's worktree down so we don't leak it.
		if child.WorktreePath != "" {
			gl := RepoGitLock(repoPath)
			gl.Lock()
			_ = RemoveWorktree(ctx, repoPath, child.WorktreePath)
			gl.Unlock()
		}
		return
	}

	// Merge safety: never merge into a parent branch while the parent has a run
	// in flight (a human may have force-moved it past the gate). Defer instead;
	// the pool flushes pending merges when the parent run completes.
	if parent.ActiveAgentRunID != nil {
		if _, serr := c.q.SetTaskMergeStatus(ctx, gen.SetTaskMergeStatusParams{MergeStatus: "pending", ID: child.ID}); serr != nil {
			log.Warn("subtasks: set pending merge status", "err", serr)
		}
		c.publishUpdated(child.ID, parent.ID)
		return
	}

	c.mergeChild(ctx, repoPath, parent, child)
	c.evaluateParent(ctx, parent.ID, repoPath)
}

// AfterParentRun is called by the pool once a parent's run completes. It marks
// any merge_conflict children resolved (a successful work run performed and
// committed the merges the conflict context asked for) and flushes children
// whose merge-back was deferred because the parent had a run in flight. It does
// NOT itself advance the parent. It re-evaluates readiness (flushed merges may
// complete the set) and, when an integration run (integration_status=pending)
// ended with a successful outcome and no conflicts remain, marks it done.
// A no-op for tasks that aren't parents. Returns whether anything changed.
func (c *SubtaskCoordinator) AfterParentRun(ctx context.Context, parentID, repoPath string, runSucceeded, outcomeSuccess bool) bool {
	lock := c.parentLock(parentID)
	lock.Lock()
	defer lock.Unlock()

	parent, err := c.q.GetTask(ctx, parentID)
	if err != nil {
		return false
	}
	children, err := c.q.ListSubtasks(ctx, &parentID)
	if err != nil || len(children) == 0 {
		return false
	}
	wasPending := c.integrationStatus(ctx, parentID) == "pending"
	changed := false
	for _, child := range children {
		switch {
		case child.MergeStatus == "pending":
			c.mergeChild(ctx, repoPath, parent, child)
			changed = true
		case child.MergeStatus == "merge_conflict" && runSucceeded:
			// The work run resolved and committed the conflicting merge on the
			// parent branch; mark the child merged and tear it down (under the
			// repo git lock so the ref writes don't race other tasks).
			c.setMerge(ctx, child.ID, "merged")
			lock := RepoGitLock(repoPath)
			lock.Lock()
			if child.WorktreePath != "" {
				_ = RemoveWorktree(ctx, repoPath, child.WorktreePath)
			}
			_ = DeleteLocalBranch(ctx, repoPath, child.Branch)
			lock.Unlock()
			c.publishUpdated(child.ID, parentID)
			changed = true
		}
	}
	if wasPending && outcomeSuccess {
		fresh, _ := c.q.ListSubtasks(ctx, &parentID)
		conflict := false
		for _, ch := range fresh {
			if ch.Archived == 0 && ch.MergeStatus == "merge_conflict" {
				conflict = true
			}
		}
		if !conflict {
			c.setIntegration(ctx, parentID, "done")
			c.publishUpdated(parentID)
			changed = true
		}
	}
	c.evaluateParent(ctx, parentID, repoPath)
	return changed
}

// mergeChild performs one child→parent merge-back and records the outcome. The
// whole git sequence (optional parent re-provision, merge, worktree/branch
// teardown) runs under the repo's git lock so it never overlaps a concurrent
// commit/merge on the same repo.
func (c *SubtaskCoordinator) mergeChild(ctx context.Context, repoPath string, parent, child gen.Task) {
	log := slog.With("component", "subtasks", "task_id", child.ID, "parent_id", parent.ID)
	if child.Branch == "" {
		// The child reached terminal without ever running: nothing to merge.
		c.setMerge(ctx, child.ID, "merged")
		c.publishUpdated(child.ID, parent.ID)
		return
	}
	if parent.Branch == "" {
		log.Warn("subtasks: parent has no branch; cannot merge child")
		return
	}

	lock := RepoGitLock(repoPath)
	lock.Lock()
	defer lock.Unlock()

	// Ensure the parent has a worktree to merge into (it was provisioned at plan
	// dispatch; re-provision defensively if it was pruned).
	parentDir := parent.WorktreePath
	if parentDir == "" || !gitcmd.DirExists(parentDir) {
		wtPath, _, baseRef, perr := provisionWorktree(ctx, repoPath, parent.ID, parent.Title)
		if perr != nil {
			log.Warn("subtasks: provision parent worktree", "err", perr)
			return
		}
		if serr := c.q.SetTaskWorktree(ctx, gen.SetTaskWorktreeParams{Branch: parent.Branch, WorktreePath: wtPath, BaseRef: baseRef, ID: parent.ID}); serr != nil {
			log.Warn("subtasks: persist parent worktree", "err", serr)
		}
		parentDir = wtPath
	}

	msg := fmt.Sprintf("Merge subtask %q (%s) into parent", child.Title, short8(child.ID))
	conflicted, files, err := MergeBranch(ctx, parentDir, child.Branch, msg, c.GitName, c.GitEmail)
	switch {
	case err != nil:
		log.Warn("subtasks: merge-back failed", "err", err)
		// Treat an unexpected merge error like a conflict so a human/agent looks.
		c.setMerge(ctx, child.ID, "merge_conflict")
		c.publishConflict(child.ID, parent.ID, files)
	case conflicted:
		log.Info("subtasks: merge-back conflict", "files", files)
		c.setMerge(ctx, child.ID, "merge_conflict")
		c.publishConflict(child.ID, parent.ID, files)
	default:
		c.setMerge(ctx, child.ID, "merged")
		// Children never push; tear down the child's worktree + local branch.
		if child.WorktreePath != "" {
			if rerr := RemoveWorktree(ctx, repoPath, child.WorktreePath); rerr != nil {
				log.Warn("subtasks: remove child worktree", "err", rerr)
			}
		}
		if derr := DeleteLocalBranch(ctx, repoPath, child.Branch); derr != nil {
			log.Warn("subtasks: delete child branch", "err", derr)
		}
		c.publishUpdated(child.ID, parent.ID)
	}
}

// evaluateParent decides what happens once the children settle. "Ready" means
// every non-archived child is terminal AND its merge-back has finished (merged
// or merge_conflict). A child that is terminal but not yet merged (its merge is
// queued behind this parent's lock) is not ready: the merge that completes it
// re-runs this check.
//
// When ready, the parent is marked integration_status=pending and, if its current
// label is agent-dispatchable, left for the dispatcher to run an integration
// pass (conflicts included). On an agent_ignore label no agent would ever run,
// so the old behaviour is kept as a fallback: auto-advance along the
// agent-success transition (only when there are no conflicts).
// Callers hold the per-parent lock.
func (c *SubtaskCoordinator) evaluateParent(ctx context.Context, parentID, repoPath string) {
	log := slog.With("component", "subtasks", "parent_id", parentID)
	children, err := c.q.ListSubtasks(ctx, &parentID)
	if err != nil || len(children) == 0 {
		return
	}
	labels, err := c.q.ListWorkflowLabels(ctx, mustWorkflowID(ctx, c.q, parentID))
	if err != nil {
		return
	}
	terminal := terminalLabelSet(labels)

	anyConflict, anyLive := false, false
	for _, ch := range children {
		if ch.Archived != 0 {
			continue
		}
		anyLive = true
		if ch.MergeStatus == "merge_conflict" {
			if !terminal[ch.Label] {
				return
			}
			anyConflict = true
			continue
		}
		if !terminal[ch.Label] || ch.MergeStatus != "merged" {
			return // still waiting on a child to finish and merge back
		}
	}
	if !anyLive {
		return
	}

	parent, err := c.q.GetTask(ctx, parentID)
	if err != nil {
		return
	}
	if c.dispatchableLabel(ctx, parent, labels) {
		if c.integrationStatus(ctx, parentID) == "" {
			c.setIntegration(ctx, parentID, "pending")
			c.publishUpdated(parentID)
		}
		return
	}

	// Fallback: no agent will run on this label; auto-advance.
	if anyConflict {
		return
	}
	if parent.Paused != 0 || parent.ActiveAgentRunID != nil {
		return // degradation: a human / the next dispatch drives it
	}
	target := c.agentSuccessTarget(ctx, parent)
	if target == "" {
		return // no agent-success transition; leave the parent unblocked
	}
	if err := c.engine.Transition(ctx, parentID, target, workflow.TriggerSubtasksComplete, "", "all subtasks complete"); err != nil {
		log.Warn("subtasks: auto-advance parent", "to", target, "err", err)
	}
}

// dispatchableLabel mirrors ListAgentPickupTasks: the label is not agent_ignore
// and an agent/both transition starts from it.
func (c *SubtaskCoordinator) dispatchableLabel(ctx context.Context, parent gen.Task, labels []gen.WorkflowLabel) bool {
	for _, l := range labels {
		if l.Name == parent.Label && l.AgentIgnore != 0 {
			return false
		}
	}
	// An agent must actually be configured for the label, else nothing would ever
	// run the integration pass.
	configs, cerr := c.q.ListAgentConfigs(ctx)
	if cerr != nil || len(matchConfigs(configs, parent.Label)) == 0 {
		return false
	}
	transitions, err := c.q.ListWorkflowTransitions(ctx, parent.WorkflowID)
	if err != nil {
		return false
	}
	for _, t := range transitions {
		if t.FromLabel == parent.Label && (t.TriggerType == "agent" || t.TriggerType == "both") {
			return true
		}
	}
	return false
}

func (c *SubtaskCoordinator) integrationStatus(ctx context.Context, taskID string) string {
	fo, err := c.q.GetTaskFanout(ctx, taskID)
	if err != nil {
		return ""
	}
	return fo.IntegrationStatus
}

func (c *SubtaskCoordinator) setIntegration(ctx context.Context, taskID, status string) {
	if err := c.q.SetTaskIntegrationStatus(ctx, gen.SetTaskIntegrationStatusParams{TaskID: taskID, IntegrationStatus: status}); err != nil {
		slog.Warn("subtasks: set integration status", "component", "subtasks", "task_id", taskID, "err", err)
	}
}

const (
	integrationNotesCap = 1500
	integrationStatMax  = 60
)

// BuildIntegrationContext renders the integration brief for a parent whose
// subtasks have all finished, or nil when the parent's integration_status is
// not "pending". It includes any merge conflicts the run must resolve.
func (c *SubtaskCoordinator) BuildIntegrationContext(ctx context.Context, parentID, verifyCommand string) *string {
	if c.integrationStatus(ctx, parentID) != "pending" {
		return nil
	}
	parent, err := c.q.GetTask(ctx, parentID)
	if err != nil {
		return nil
	}
	children, err := c.q.ListSubtasks(ctx, &parentID)
	if err != nil || len(children) == 0 {
		return nil
	}
	slices.Reverse(children) // newest-first from ListSubtasks; present oldest first
	fanout := map[string]string{}
	for _, ch := range children {
		if fo, ferr := c.q.GetTaskFanout(ctx, ch.ID); ferr == nil {
			fanout[ch.ID] = fo.FileScope
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "All subtasks of this task have finished. Each subtask branch was merged into this task's branch (%s) as it finished, in completion order; a merge that conflicted was aborted and is listed below for you to resolve.\n\n", parent.Branch)
	for i, ch := range children {
		state := "merged"
		switch {
		case ch.Archived != 0:
			state = "archived: skipped"
		case ch.MergeStatus == "merge_conflict":
			state = "CONFLICT"
		case ch.MergeStatus != "merged":
			state = ch.MergeStatus
		}
		fmt.Fprintf(&b, "%d. %q (%s) — label %s, merge %s\n", i+1, ch.Title, short8(ch.ID), ch.Label, state)
		scope := splitFileScope(fanout[ch.ID])
		if len(scope) == 0 {
			b.WriteString("   Scope: none declared\n")
		} else {
			fmt.Fprintf(&b, "   Scope: %s\n", strings.Join(scope, ", "))
		}
		switch {
		case ch.Archived != 0:
		case ch.MergeStatus == "merge_conflict":
			fmt.Fprintf(&b, "   Branch: %s — conflicts when merged into this branch. Run `git merge %s`, resolve the conflicts, and commit.\n", ch.Branch, ch.Branch)
		case ch.Branch != "":
			fmt.Fprintf(&b, "   Branch: %s (merged and deleted)\n", ch.Branch)
		default:
			b.WriteString("   Branch: none (the subtask never ran)\n")
		}
		if notes := strings.TrimSpace(ch.AgentNotes); notes != "" {
			notes = strings.ReplaceAll(notes, sourceCommentsEndMarkerForNotes, "")
			if len(notes) > integrationNotesCap {
				notes = notes[:integrationNotesCap] + "…(truncated)"
			}
			b.WriteString("   Notes:\n")
			for _, l := range strings.Split(notes, "\n") {
				b.WriteString("     " + l + "\n")
			}
		}
	}
	if parent.WorktreePath != "" && parent.BaseRef != "" && gitcmd.DirExists(parent.WorktreePath) {
		if stat := diffStatAgainstBase(ctx, parent.WorktreePath, parent.BaseRef, parent.Branch, integrationStatMax); stat != "" {
			b.WriteString("\nMerged result (git diff --stat against the base):\n" + stat + "\n")
		}
	}
	verify := "the repo's test/verify commands"
	if strings.TrimSpace(verifyCommand) != "" {
		verify = "`" + strings.TrimSpace(verifyCommand) + "`"
	}
	fmt.Fprintf(&b, "\nYour job now:\n"+
		"1. Resolve any CONFLICT entries above first.\n"+
		"2. Check the combined change for consistency across subtasks (duplicated helpers, mismatched interfaces, missing wiring, docs/CHANGELOG).\n"+
		"3. Run %s and fix any failures.\n"+
		"4. Commit, then signal_complete. ate runs the verify command again after you succeed.\n", verify)
	s := b.String()
	return &s
}

// agentSuccessTarget returns the destination of the agent (or both) transition
// with path "success" from the parent's current label, or "".
func (c *SubtaskCoordinator) agentSuccessTarget(ctx context.Context, parent gen.Task) string {
	transitions, err := c.q.ListWorkflowTransitions(ctx, parent.WorkflowID)
	if err != nil {
		return ""
	}
	for _, t := range transitions {
		if t.FromLabel == parent.Label && (t.TriggerType == "agent" || t.TriggerType == "both") && t.Path != nil && *t.Path == "success" {
			return t.ToLabel
		}
	}
	return ""
}

// BuildConflictContext renders the merge conflicts a parent's work agent must
// resolve, or nil if the parent has none. Used by the dispatcher to inject the
// context into the parent's run prompt.
func (c *SubtaskCoordinator) BuildConflictContext(ctx context.Context, parentID string) *string {
	children, err := c.q.ListSubtasks(ctx, &parentID)
	if err != nil {
		return nil
	}
	var b strings.Builder
	for _, ch := range children {
		if ch.MergeStatus != "merge_conflict" {
			continue
		}
		fmt.Fprintf(&b, "- Subtask %q (branch %s) conflicts when merged into this branch. Run `git merge %s`, resolve the conflicts, and commit.\n", ch.Title, ch.Branch, ch.Branch)
	}
	if b.Len() == 0 {
		return nil
	}
	s := b.String()
	return &s
}

func (c *SubtaskCoordinator) setMerge(ctx context.Context, taskID, status string) {
	if _, err := c.q.SetTaskMergeStatus(ctx, gen.SetTaskMergeStatusParams{MergeStatus: status, ID: taskID}); err != nil {
		slog.Warn("subtasks: set merge status", "component", "subtasks", "task_id", taskID, "status", status, "err", err)
	}
}

func (c *SubtaskCoordinator) publishUpdated(ids ...string) {
	if c.pub == nil {
		return
	}
	for _, id := range ids {
		c.pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: id})
	}
}

func (c *SubtaskCoordinator) publishConflict(childID, parentID string, files []string) {
	if c.pub == nil {
		return
	}
	c.pub.Publish(wsevent.TypeTaskSubtaskConflict, wsevent.TaskSubtaskConflict{
		TaskID:   childID,
		ParentID: parentID,
		Files:    files,
	})
	c.publishUpdated(childID, parentID)
}

// --- small helpers ---

func short8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func terminalLabelSet(labels []gen.WorkflowLabel) map[string]bool {
	m := make(map[string]bool, len(labels))
	for _, l := range labels {
		if l.IsTerminal != 0 {
			m[l.Name] = true
		}
	}
	return m
}

func mustWorkflowID(ctx context.Context, q *gen.Queries, taskID string) string {
	t, err := q.GetTask(ctx, taskID)
	if err != nil {
		return ""
	}
	return t.WorkflowID
}
