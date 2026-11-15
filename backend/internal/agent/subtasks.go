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
// local branch are then removed. Home children never push to origin or open PRs — the
// parent's branch is the only outward-facing artifact; foreign children (a
// different repo, ADR 0041) push and open their own PR instead. Once every child is
// terminal and merged (or conflicted), the parent is marked integration_status=
// pending and the dispatcher runs its agent with an integration brief; a
// conflicting merge-back is flagged and resolved by that run.
type SubtaskCoordinator struct {
	q      *gen.Queries
	engine *workflow.Engine
	pub    Publisher

	GitName  string
	GitEmail string

	// OpenPR pushes a foreign child's branch and opens (or reuses) its PR (ADR
	// 0041). Wired in cmd/server to prflow.CreateForTask (agent cannot import
	// prflow). Returns the resulting git_state. A precondition failure (no
	// remote / unsupported forge) must be returned as ("", nil) after logging;
	// any other error is returned so the OnTerminal outbox retries. Nil means
	// foreign children are torn down without a PR and stay merge_status=pending.
	OpenPR func(ctx context.Context, task gen.Task, repo gen.Repo) (gitState string, err error)

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
// to skip the normal per-run push to origin for home children; foreign children
// (IsForeignChild, ADR 0041) are pushed like top-level tasks.
func IsSubtask(t gen.Task) bool { return t.ParentTaskID != nil && *t.ParentTaskID != "" }

// IsForeignRepo reports whether a child in childRepoID would be foreign to a
// parent in parentRepoID (ADR 0041). An empty childRepoID means "inherit", so
// it is never foreign. Use this where no child row exists yet.
func IsForeignRepo(parentRepoID, childRepoID string) bool {
	return childRepoID != "" && childRepoID != parentRepoID
}

// Waiting codes shared by SubtaskReadiness, the dependency block reason and the
// integration prompt.
const (
	SubtaskWaitMergeBack    = "merge_back"
	SubtaskWaitAwaitingPR   = "awaiting_pr"
	SubtaskWaitPRClosed     = "pr_closed"
	SubtaskWaitVerifyFailed = "verify_failed"
)

// SubtaskReadiness reports whether a terminal child has landed, and if not what
// it is waiting on. It mirrors the subtask_landed SQL view (migration 097)
// exactly; keep both in sync (a parity test guards this). lastVerify is the
// child's latest run_verifications status, "" when none (no verify configured
// counts as passed). Home: merge_status merged/merge_conflict. Foreign: PR open
// or merged AND verify passed.
func SubtaskReadiness(foreign bool, mergeStatus, lastVerify string) (bool, string) {
	if foreign {
		if mergeStatus == "pr_closed" {
			return false, SubtaskWaitPRClosed
		}
		if lastVerify != "" && lastVerify != "pass" {
			return false, SubtaskWaitVerifyFailed
		}
		if mergeStatus == "pr_open" || mergeStatus == "merged" {
			return true, ""
		}
		return false, SubtaskWaitAwaitingPR
	}
	if mergeStatus == "merged" || mergeStatus == "merge_conflict" {
		return true, ""
	}
	return false, SubtaskWaitMergeBack
}

// latestVerify returns the latest verify status per task id (absent = none).
func (c *SubtaskCoordinator) latestVerify(ctx context.Context, ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	rows, err := c.q.ListLatestVerifyStatusForTasks(ctx, ids)
	if err != nil {
		slog.Warn("subtasks: latest verify", "component", "subtasks", "err", err)
		return out
	}
	for _, r := range rows {
		out[r.TaskID] = r.Status
	}
	return out
}

// ReevaluateParent re-checks a parent's readiness under its lock. ghsync calls
// it (injected) when a foreign child's PR state changes.
func (c *SubtaskCoordinator) ReevaluateParent(ctx context.Context, parentID string) {
	l := c.parentLock(parentID)
	l.Lock()
	defer l.Unlock()
	c.evaluateParent(ctx, parentID)
}

// IsForeignChild reports whether child is a subtask of parent that lives in a
// different repo (ADR 0041). Derived from the repo mismatch, never stored.
func IsForeignChild(parent, child gen.Task) bool {
	return child.ParentTaskID != nil && *child.ParentTaskID == parent.ID && IsForeignRepo(parent.RepoID, child.RepoID)
}

// ForeignChildMergeStatus maps a foreign child's git_state to its merge_status:
// the status follows the child's own PR. No PR yet (""/"pushed") is "pending".
func ForeignChildMergeStatus(gitState string) string {
	switch gitState {
	case "pr_open":
		return "pr_open"
	case "pr_merged":
		return "merged"
	case "pr_closed":
		return "pr_closed"
	default:
		return "pending"
	}
}

// OnChildTerminal is invoked when a subtask reaches a terminal label (from
// engine.OnTerminal). A home child is merged back into the parent's branch and
// the parent re-evaluated; if the parent has a run in flight the merge is
// deferred (merge_status=pending) and retried when that run finishes
// (AfterParentRun). A foreign child (different repo, ADR 0041) is instead pushed
// and given its own PR (landForeignChild). childRepo is the repo the child lives
// in. A non-nil error asks the side-effect outbox to retry.
func (c *SubtaskCoordinator) OnChildTerminal(ctx context.Context, child gen.Task, childRepo gen.Repo) error {
	log := slog.With("component", "subtasks", "task_id", child.ID)
	if child.ParentTaskID == nil {
		return nil
	}
	// Read the parent before taking any lock: the foreign path must not touch
	// the parent's lock or repo at all until its own repo work is done.
	parent, err := c.q.GetTask(ctx, *child.ParentTaskID)
	if err != nil {
		// Parent gone (deleted/orphaned): nothing to merge back into. Tear the
		// child's worktree down so we don't leak it.
		if child.WorktreePath != "" {
			gl := RepoGitLock(childRepo.Path)
			gl.Lock()
			_ = RemoveWorktree(ctx, childRepo.Path, child.WorktreePath)
			gl.Unlock()
		}
		return nil
	}
	if IsForeignChild(parent, child) {
		return c.landForeignChild(ctx, parent, child, childRepo)
	}

	lock := c.parentLock(parent.ID)
	lock.Lock()
	defer lock.Unlock()

	// Re-read under the lock for a fresh ActiveAgentRunID.
	if fresh, ferr := c.q.GetTask(ctx, parent.ID); ferr == nil {
		parent = fresh
	}

	// Merge safety: never merge into a parent branch while the parent has a run
	// in flight (a human may have force-moved it past the gate). Defer instead;
	// the pool flushes pending merges when the parent run completes.
	if parent.ActiveAgentRunID != nil {
		if _, serr := c.q.SetTaskMergeStatus(ctx, gen.SetTaskMergeStatusParams{MergeStatus: "pending", ID: child.ID}); serr != nil {
			log.Warn("subtasks: set pending merge status", "err", serr)
		}
		c.publishUpdated(child.ID, parent.ID)
		return nil
	}

	// A home child shares the parent's repo, so childRepo.Path is the parent repo path.
	c.mergeChild(ctx, childRepo.Path, parent, child)
	c.evaluateParent(ctx, parent.ID)
	return nil
}

// landForeignChild lands a foreign child via its own PR (ADR 0041). It never
// touches the parent's repo, worktree, branch or git lock, and never defers on
// the parent's active run (it writes nothing to the parent branch). Locks: the
// child repo lock is taken only inside OnTaskTerminal; the parent lock only for
// the final status write, so no two repo locks are ever held (ADR 0009).
func (c *SubtaskCoordinator) landForeignChild(ctx context.Context, parent, child gen.Task, childRepo gen.Repo) error {
	log := slog.With("component", "subtasks", "task_id", child.ID, "parent_id", parent.ID)
	if child.Branch == "" {
		// Never ran: nothing to push or PR.
		lock := c.parentLock(parent.ID)
		lock.Lock()
		defer lock.Unlock()
		c.setMerge(ctx, child.ID, "merged")
		c.publishUpdated(child.ID, parent.ID)
		c.evaluateParent(ctx, parent.ID)
		return nil
	}
	// Outbox retries re-enter here: only mark pending when no PR status is
	// recorded yet, so a later pr_open/merged is never overwritten.
	cur, err := c.q.GetTask(ctx, child.ID)
	if err != nil {
		return fmt.Errorf("reload child: %w", err)
	}
	if cur.MergeStatus == "" {
		c.setMerge(ctx, child.ID, "pending")
		c.publishUpdated(child.ID, parent.ID)
	}

	state := ""
	if c.OpenPR != nil {
		// No locks held: prflow pushes and calls the forge itself.
		state, err = c.OpenPR(ctx, cur, childRepo)
		if err != nil {
			return fmt.Errorf("open PR for foreign subtask: %w", err)
		}
	}
	reloaded, err := c.q.GetTask(ctx, child.ID)
	if err != nil {
		return fmt.Errorf("reload child: %w", err)
	}
	if state == "" {
		state = reloaded.GitState
	}
	if err := OnTaskTerminal(ctx, c.q, reloaded, childRepo); err != nil {
		log.Warn("subtasks: foreign child teardown", "err", err)
		return err
	}

	lock := c.parentLock(parent.ID)
	lock.Lock()
	defer lock.Unlock()
	// ghsync may have moved the PR on (e.g. merged) meanwhile; prefer the freshest state.
	if latest, lerr := c.q.GetTask(ctx, child.ID); lerr == nil && latest.GitState != "" {
		state = latest.GitState
	}
	c.setMerge(ctx, child.ID, ForeignChildMergeStatus(state))
	c.publishUpdated(child.ID, parent.ID)
	c.evaluateParent(ctx, parent.ID)
	return nil
}

// AfterParentRun is called by the pool once a parent's run completes. It marks
// any merge_conflict children resolved (a successful work run performed and
// committed the merges the conflict context asked for) and flushes children
// whose merge-back was deferred because the parent had a run in flight. It does
// NOT itself advance the parent. It re-evaluates readiness (flushed merges may
// complete the set) and, when an integration run (integration_status=pending)
// ended with a successful outcome and no conflicts remain, marks it done.
// A no-op for tasks that aren't parents. Returns whether anything changed.
func (c *SubtaskCoordinator) AfterParentRun(ctx context.Context, parentID, parentRepoPath string, runSucceeded, outcomeSuccess bool) bool {
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
		if IsForeignChild(parent, child) {
			// Foreign children (ADR 0041) have no branch to merge back and their
			// worktree is in another repo: nothing to flush or clean up here.
			continue
		}
		switch {
		case child.MergeStatus == "pending":
			c.mergeChild(ctx, parentRepoPath, parent, child)
			changed = true
		case child.MergeStatus == "merge_conflict" && runSucceeded:
			// The work run resolved and committed the conflicting merge on the
			// parent branch; mark the child merged and tear it down (under the
			// repo git lock so the ref writes don't race other tasks).
			c.setMerge(ctx, child.ID, "merged")
			lock := RepoGitLock(parentRepoPath)
			lock.Lock()
			if child.WorktreePath != "" {
				_ = RemoveWorktree(ctx, parentRepoPath, child.WorktreePath)
			}
			_ = DeleteLocalBranch(ctx, parentRepoPath, child.Branch)
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
	c.evaluateParent(ctx, parentID)
	return changed
}

// mergeChild performs one child→parent merge-back and records the outcome. The
// whole git sequence (optional parent re-provision, merge, worktree/branch
// teardown) runs under the repo's git lock so it never overlaps a concurrent
// commit/merge on the same repo.
func (c *SubtaskCoordinator) mergeChild(ctx context.Context, parentRepoPath string, parent, child gen.Task) {
	log := slog.With("component", "subtasks", "task_id", child.ID, "parent_id", parent.ID)
	if IsForeignChild(parent, child) {
		log.Warn("subtasks: refusing to merge a foreign child into the parent branch")
		return
	}
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

	lock := RepoGitLock(parentRepoPath)
	lock.Lock()
	defer lock.Unlock()

	// Ensure the parent has a worktree to merge into (it was provisioned at plan
	// dispatch; re-provision defensively if it was pruned).
	parentDir := parent.WorktreePath
	if parentDir == "" || !gitcmd.DirExists(parentDir) {
		wtPath, _, baseRef, perr := provisionWorktree(ctx, parentRepoPath, parent.ID, parent.Title)
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
			if rerr := RemoveWorktree(ctx, parentRepoPath, child.WorktreePath); rerr != nil {
				log.Warn("subtasks: remove child worktree", "err", rerr)
			}
		}
		if derr := DeleteLocalBranch(ctx, parentRepoPath, child.Branch); derr != nil {
			log.Warn("subtasks: delete child branch", "err", derr)
		}
		c.publishUpdated(child.ID, parent.ID)
	}
}

// evaluateParent decides what happens once the children settle. "Ready" means
// every non-archived child is terminal AND has landed: a home child's merge-back
// has finished (merged or merge_conflict); a foreign child (ADR 0041) has its
// PR open or merged with its latest verify passed (SubtaskReadiness). A child that is terminal but not yet merged (its merge is
// queued behind this parent's lock) is not ready: the merge that completes it
// re-runs this check.
//
// When ready, the parent is marked integration_status=pending and, if its current
// label is agent-dispatchable, left for the dispatcher to run an integration
// pass (conflicts included). On an agent_ignore label no agent would ever run,
// so the old behaviour is kept as a fallback: auto-advance along the
// agent-success transition (only when there are no conflicts).
// Callers hold the per-parent lock.
func (c *SubtaskCoordinator) evaluateParent(ctx context.Context, parentID string) {
	log := slog.With("component", "subtasks", "parent_id", parentID)
	children, err := c.q.ListSubtasks(ctx, &parentID)
	if err != nil || len(children) == 0 {
		return
	}
	parent, err := c.q.GetTask(ctx, parentID)
	if err != nil {
		return
	}
	var foreignIDs []string
	for _, ch := range children {
		if ch.Archived == 0 && IsForeignChild(parent, ch) {
			foreignIDs = append(foreignIDs, ch.ID)
		}
	}
	verify := c.latestVerify(ctx, foreignIDs)
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
		if IsForeignChild(parent, ch) {
			// Foreign child (ADR 0041): ready once terminal with its PR open or
			// merged and its last verify passed. Mirrors the subtask_landed view.
			if !terminal[ch.Label] {
				return
			}
			if ready, _ := SubtaskReadiness(true, ch.MergeStatus, verify[ch.ID]); !ready {
				return
			}
			continue
		}
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

	// Multi-repo (ADR 0041): any live foreign child switches to the richer
	// brief. Single-repo parents keep the legacy text byte for byte.
	var liveIDs []string
	multiRepo := false
	for _, ch := range children {
		if ch.Archived != 0 {
			continue
		}
		liveIDs = append(liveIDs, ch.ID)
		if IsForeignChild(parent, ch) {
			multiRepo = true
		}
	}
	var lastVerify map[string]string
	repoNames := map[string]string{}
	repoName := func(id string) string {
		if n, ok := repoNames[id]; ok {
			return n
		}
		n := short8(id)
		if r, rerr := c.q.GetRepo(ctx, id); rerr == nil && r.Name != "" {
			n = r.Name
		}
		repoNames[id] = n
		return n
	}
	if multiRepo {
		lastVerify = c.latestVerify(ctx, liveIDs)
	}

	var b strings.Builder
	if multiRepo {
		fmt.Fprintf(&b, "All subtasks of this task have finished. Subtasks in this repo (%s) were merged into this task's branch (%s) in completion order; a merge that conflicted was aborted and is listed below for you to resolve. Subtasks in other repos were NOT merged here: each has its own pull request, which stays open until this change set is reviewed. Nothing has been merged into another repo by ATE yet.\n\n", repoName(parent.RepoID), parent.Branch)
	} else {
		fmt.Fprintf(&b, "All subtasks of this task have finished. Each subtask branch was merged into this task's branch (%s) as it finished, in completion order; a merge that conflicted was aborted and is listed below for you to resolve.\n\n", parent.Branch)
	}
	for i, ch := range children {
		foreign := multiRepo && IsForeignChild(parent, ch)
		state := "merged"
		switch {
		case ch.Archived != 0:
			state = "archived: skipped"
		case ch.MergeStatus == "merge_conflict":
			state = "CONFLICT"
		case ch.MergeStatus != "merged":
			state = ch.MergeStatus
		}
		if multiRepo {
			where := "repo " + repoName(ch.RepoID)
			if foreign {
				where += " (other repo)"
			}
			fmt.Fprintf(&b, "%d. %q (%s) — %s, label %s, merge %s\n", i+1, ch.Title, short8(ch.ID), where, ch.Label, state)
		} else {
			fmt.Fprintf(&b, "%d. %q (%s) — label %s, merge %s\n", i+1, ch.Title, short8(ch.ID), ch.Label, state)
		}
		scope := splitFileScope(fanout[ch.ID])
		if len(scope) == 0 {
			b.WriteString("   Scope: none declared\n")
		} else {
			fmt.Fprintf(&b, "   Scope: %s\n", strings.Join(scope, ", "))
		}
		switch {
		case ch.Archived != 0:
		case foreign:
			fmt.Fprintf(&b, "   Branch: %s (own PR, not merged into this branch)\n", ch.Branch)
			url := ch.PrUrl
			if url == "" {
				url = "none"
			}
			prState := "not opened"
			switch ch.GitState {
			case "pr_open":
				prState = "open"
			case "pr_merged":
				prState = "merged"
			case "pr_closed":
				prState = "closed unmerged"
			}
			fmt.Fprintf(&b, "   PR: %s, state %s\n", url, prState)
		case ch.MergeStatus == "merge_conflict":
			fmt.Fprintf(&b, "   Branch: %s — conflicts when merged into this branch. Run `git merge %s`, resolve the conflicts, and commit.\n", ch.Branch, ch.Branch)
		case ch.Branch != "":
			fmt.Fprintf(&b, "   Branch: %s (merged and deleted)\n", ch.Branch)
		default:
			b.WriteString("   Branch: none (the subtask never ran)\n")
		}
		if multiRepo && ch.Archived == 0 {
			v := lastVerify[ch.ID]
			if v == "" {
				v = "none configured"
			}
			fmt.Fprintf(&b, "   Verify: %s\n", v)
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
	if multiRepo {
		b.WriteString("5. Review the whole change set across repos (interfaces/contracts between this repo and the other-repo PRs above). Do not edit other repos; describe any change they need in your summary or ask via request_human.\n")
	}
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
