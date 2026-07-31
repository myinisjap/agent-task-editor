package ghsync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// ingestPRFeedback checks a task's PR for new changes-requested reviews,
// inline review comments, failed GHA checks, and merge conflicts with the base
// branch since the last sweep, and surfaces them to the agent:
//   - inline review comments (have a file/line anchor) are inserted into
//     task_review_comments (source='github') so they flow through the
//     existing OPEN REVIEW COMMENTS prompt section and resolve loop.
//   - changes_requested review bodies, failed check names/links, and a
//     base-branch merge conflict (no anchor) are appended to the current run's
//     Feedback column, rendered under the FEEDBACK FROM PRIOR REVIEW: prompt
//     section.
//
// prState is the PR state resolved by the caller this sweep ("pr_open",
// "pr_merged", ...); merge-conflict detection only applies while the PR is
// still open.
//
// Ingestion is cursor-based (task_pr_review_state) and idempotent: re-sweeps
// never duplicate feedback already surfaced. When the PR's head commit SHA
// changes (the agent pushed), the cursor resets so previously-seen reviews
// don't block a fresh feedback cycle for new ones — but already-ingested
// inline comments are left as-is (matching how open review comments persist
// across pushes today) rather than being purged.
//
// Every step is best-effort: a `gh` hiccup on one signal (reviews, comments,
// checks) is logged and swallowed rather than aborting the others or failing
// the sweep, mirroring the writeback package's error-handling style.
func (s *Syncer) ingestPRFeedback(ctx context.Context, task gen.Task, repo repoInfo, prNumber int, prState string) {
	if prNumber == 0 || s.q == nil {
		return
	}
	log := slog.With("component", "ghsync", "task_id", task.ID, "pr_number", prNumber)

	state, err := s.q.GetTaskPRReviewState(ctx, task.ID)
	if err != nil {
		// No row yet is the common case (first sweep for this task's PR).
		state = gen.TaskPrReviewState{TaskID: task.ID}
	}

	var head forge.PRHead
	if s.getPRHead != nil {
		var err error
		head, err = s.getPRHead(ctx, repo, task.Branch)
		if err != nil {
			log.Warn("ghsync: get PR head", "err", err)
		}
	}

	// A new head SHA means the agent pushed since we last looked: start a
	// fresh feedback cycle so reviews/checks against the old commit don't
	// keep being (re-)considered "new" forever, but also don't re-inject
	// anything we've already surfaced under the old head.
	// LastConflictSha is deliberately not reset here: it already embeds the head
	// SHA, so a push produces a different fingerprint on its own.
	freshCycle := head.HeadSHA != "" && head.HeadSHA != state.HeadSha
	if freshCycle {
		state.LastReviewSubmittedAt = nil
		state.LastFailedCheckSha = nil
	}

	var feedbackParts []string
	changed := false

	if parts := s.ingestReviews(ctx, task, repo, prNumber, &state, log); len(parts) > 0 {
		feedbackParts = append(feedbackParts, parts...)
		changed = true
	}
	if s.ingestReviewComments(ctx, task, repo, prNumber, log) {
		changed = true
	}
	if parts := s.ingestFailedChecks(ctx, task, repo, prNumber, head.HeadSHA, &state, log); len(parts) > 0 {
		feedbackParts = append(feedbackParts, parts...)
		changed = true
	}
	if parts := s.ingestMergeConflict(ctx, task, head, prState, &state, log); len(parts) > 0 {
		feedbackParts = append(feedbackParts, parts...)
		changed = true
	}

	if len(feedbackParts) > 0 {
		s.appendRunFeedback(ctx, task, strings.Join(feedbackParts, "\n\n"), log)
	}

	if head.HeadSHA != "" {
		state.HeadSha = head.HeadSHA
	}
	if _, err := s.q.UpsertTaskPRReviewState(ctx, gen.UpsertTaskPRReviewStateParams{
		TaskID:                task.ID,
		HeadSha:               state.HeadSha,
		LastReviewSubmittedAt: state.LastReviewSubmittedAt,
		LastFailedCheckSha:    state.LastFailedCheckSha,
		LastConflictSha:       state.LastConflictSha,
	}); err != nil {
		log.Warn("ghsync: upsert pr review state", "err", err)
	}

	if changed && repo.repo.PrReviewAutoTransitionEnabled != 0 {
		s.autoTransitionOnFeedback(ctx, task, log)
	}
}

// ingestReviews fetches PR reviews and returns feedback text for every
// changes_requested review submitted after the stored cursor. Advances the
// cursor (state.LastReviewSubmittedAt) to the newest review's timestamp seen.
func (s *Syncer) ingestReviews(ctx context.Context, task gen.Task, repo repoInfo, prNumber int, state *gen.TaskPrReviewState, log *slog.Logger) []string {
	if s.getReviews == nil {
		return nil
	}
	reviews, err := s.getReviews(ctx, repo, prNumber)
	if err != nil {
		log.Warn("ghsync: get PR reviews", "err", err)
		return nil
	}

	lastSeen := ""
	if state.LastReviewSubmittedAt != nil {
		lastSeen = *state.LastReviewSubmittedAt
	}
	newest := lastSeen

	var parts []string
	for _, r := range reviews {
		if r.State != "CHANGES_REQUESTED" {
			continue
		}
		if r.SubmittedAt == "" || r.SubmittedAt <= lastSeen {
			continue
		}
		body := strings.TrimSpace(r.Body)
		if body == "" {
			body = "(no summary provided)"
		}
		parts = append(parts, fmt.Sprintf("GitHub review — changes requested by %s:\n%s", authorOrUnknown(r.Author), body))
		if r.SubmittedAt > newest {
			newest = r.SubmittedAt
		}
	}
	if newest != lastSeen {
		state.LastReviewSubmittedAt = &newest
	}
	return parts
}

// ingestReviewComments fetches inline PR review comments and inserts any not
// already ingested (deduped by external_id) into task_review_comments,
// tagged with the source forge's name (see reviewCommentSourceName —
// "github", "gitea", ...). Returns true if at least one new comment was
// inserted.
func (s *Syncer) ingestReviewComments(ctx context.Context, task gen.Task, repo repoInfo, prNumber int, log *slog.Logger) bool {
	if s.getReviewComments == nil {
		return false
	}
	comments, err := s.getReviewComments(ctx, repo, prNumber)
	if err != nil {
		log.Warn("ghsync: get PR review comments", "err", err)
		return false
	}

	sourceName := reviewCommentSourceName(repo)

	inserted := false
	for _, c := range comments {
		if c.Path == "" || c.ID == "" {
			continue
		}
		if _, err := s.q.GetTaskReviewCommentByExternalID(ctx, gen.GetTaskReviewCommentByExternalIDParams{
			TaskID:     task.ID,
			ExternalID: &c.ID,
		}); err == nil {
			continue // already ingested
		}

		side := "new"
		if strings.EqualFold(c.Side, "LEFT") {
			side = "old"
		}
		line := c.Line
		startLine := c.StartLine
		if line <= 0 {
			// Comment is on an outdated diff position with no live line
			// (GitHub omits `line` in that case); anchor to line 1 rather
			// than drop the comment, so the agent still sees the feedback.
			line = 1
			startLine = 1
		}
		if startLine <= 0 || startLine > line {
			startLine = line
		}

		created, err := s.q.CreateForgeTaskReviewComment(ctx, gen.CreateForgeTaskReviewCommentParams{
			ID:         uuid.NewString(),
			TaskID:     task.ID,
			FilePath:   c.Path,
			Side:       side,
			StartLine:  int64(startLine),
			EndLine:    int64(line),
			QuotedText: c.DiffHunk,
			Body:       c.Body,
			ExternalID: &c.ID,
			Source:     sourceName,
		})
		if err != nil {
			log.Warn("ghsync: create forge review comment", "external_id", c.ID, "source", sourceName, "err", err)
			continue
		}
		inserted = true
		s.hub.Publish("task.review_comment_added", map[string]any{
			"task_id":    task.ID,
			"comment_id": created.ID,
			"source":     sourceName,
		})
	}
	return inserted
}

// reviewCommentSourceName returns the tasks_review_comments.source value to
// tag an ingested inline review comment with: the resolved forge's own Name()
// when known, defaulting to "github" when repo.forge is nil (e.g. in tests
// that construct a repoInfo by hand without wiring a real forge.Forge — the
// overwhelming common case in production, prior to Gitea support, was and
// remains GitHub).
func reviewCommentSourceName(repo repoInfo) string {
	if repo.forge != nil {
		return repo.forge.Name()
	}
	return "github"
}

// ingestFailedChecks fetches failed/cancelled GHA checks for the PR and
// returns feedback text if the set of failures is new since the last sweep
// at this head SHA (tracked by state.LastFailedCheckSha, a compact fingerprint
// of the failing check names rather than the commit SHA itself, so a repeat
// sweep against the same failing commit doesn't re-inject the same feedback).
func (s *Syncer) ingestFailedChecks(ctx context.Context, task gen.Task, repo repoInfo, prNumber int, headSHA string, state *gen.TaskPrReviewState, log *slog.Logger) []string {
	if s.getFailedChecks == nil {
		return nil
	}
	checks, err := s.getFailedChecks(ctx, repo, prNumber)
	if err != nil {
		log.Warn("ghsync: get failed checks", "err", err)
		return nil
	}
	if len(checks) == 0 {
		return nil
	}

	names := make([]string, 0, len(checks))
	lines := make([]string, 0, len(checks))
	for _, c := range checks {
		names = append(names, c.Name)
		if c.Link != "" {
			lines = append(lines, fmt.Sprintf("- %s (%s): %s", c.Name, c.Bucket, c.Link))
		} else {
			lines = append(lines, fmt.Sprintf("- %s (%s)", c.Name, c.Bucket))
		}
	}
	fingerprint := headSHA + "|" + strings.Join(names, ",")

	prev := ""
	if state.LastFailedCheckSha != nil {
		prev = *state.LastFailedCheckSha
	}
	if fingerprint == prev {
		return nil // same failures already surfaced for this commit
	}
	state.LastFailedCheckSha = &fingerprint

	return []string{fmt.Sprintf("GitHub Actions — failed checks on the current commit:\n%s", strings.Join(lines, "\n"))}
}

// ingestMergeConflict records GitHub's mergeability verdict for the task's PR
// on the task row and returns feedback text the first time a conflict is seen
// for the PR's current head commit.
//
// GitHub recomputes mergeability asynchronously whenever either side moves, so
// a PR that merged cleanly when it was opened can start conflicting later
// without the task's branch changing at all - which is exactly the case worth
// telling the agent about.
//
// Dedup rules (state.LastConflictSha holds "<head sha>|<base ref>"):
//   - conflicting, same fingerprint as last time: already surfaced, stay quiet.
//   - conflicting, new fingerprint (agent pushed, or the PR was retargeted):
//     surface it again, since the last resolution attempt evidently failed.
//   - mergeable: clear the cursor, so a conflict reintroduced by a later
//     base-branch move is surfaced again even at an unchanged head commit.
//   - unknown: GitHub has not finished the test merge. Report nothing and
//     leave the cursor alone - clearing it here would make a conflict flap
//     back into fresh feedback every time the verdict briefly goes UNKNOWN.
func (s *Syncer) ingestMergeConflict(ctx context.Context, task gen.Task, head forge.PRHead, prState string, state *gen.TaskPrReviewState, log *slog.Logger) []string {
	if head.Mergeable == "" {
		return nil // getPRHead is unwired or failed - nothing was observed
	}
	s.setPRMergeable(ctx, task, string(head.Mergeable), log)

	if head.Mergeable == forge.MergeableClean {
		state.LastConflictSha = nil
		return nil
	}
	// Only an open PR can be usefully un-conflicted; a merged/closed one is
	// nobody's problem, and GitHub's verdict on it is meaningless.
	if head.Mergeable != forge.MergeableConflicting || prState != "pr_open" {
		return nil
	}

	fingerprint := head.HeadSHA + "|" + head.BaseRef
	prev := ""
	if state.LastConflictSha != nil {
		prev = *state.LastConflictSha
	}
	if fingerprint == prev {
		return nil // same conflict already surfaced for this commit
	}
	state.LastConflictSha = &fingerprint

	base := head.BaseRef
	if base == "" {
		base = "its base branch"
	}
	log.Info("ghsync: PR conflicts with base branch", "base_ref", head.BaseRef, "head_sha", head.HeadSHA)
	return []string{fmt.Sprintf(
		"GitHub — merge conflict: this PR no longer merges cleanly into %s. "+
			"Update the branch against the latest %s (merge it in or rebase onto it), "+
			"resolve every conflicted file, and push the resolution.", base, base)}
}

// setPRMergeable persists a change in the task's PR mergeability and notifies
// connected clients. A no-op when the verdict is unchanged, so a task isn't
// rewritten (or an event published) on every sweep. Best-effort: a write
// failure is logged and swallowed, like the rest of the ingestion path.
func (s *Syncer) setPRMergeable(ctx context.Context, task gen.Task, mergeable string, log *slog.Logger) {
	if task.PrMergeable == mergeable {
		return
	}
	if _, err := s.q.SetTaskPRMergeable(ctx, gen.SetTaskPRMergeableParams{
		PrMergeable: mergeable,
		ID:          task.ID,
	}); err != nil {
		log.Warn("ghsync: set pr mergeable", "mergeable", mergeable, "err", err)
		return
	}
	if s.hub != nil {
		s.hub.Publish("task.pr_mergeable_changed", map[string]any{
			"task_id":      task.ID,
			"pr_mergeable": mergeable,
		})
	}
}

// appendRunFeedback appends newFeedback to the task's current agent run's
// Feedback column via read-modify-write, so it doesn't clobber an existing
// human-authored rejection note; it's a no-op if the task has no current run
// (nothing to inject the feedback into).
func (s *Syncer) appendRunFeedback(ctx context.Context, task gen.Task, newFeedback string, log *slog.Logger) {
	if task.CurrentAgentRunID == nil || *task.CurrentAgentRunID == "" {
		return
	}
	run, err := s.q.GetAgentRun(ctx, *task.CurrentAgentRunID)
	if err != nil {
		log.Warn("ghsync: get current agent run for feedback", "err", err)
		return
	}
	combined := newFeedback
	if run.Feedback != nil && strings.TrimSpace(*run.Feedback) != "" {
		combined = strings.TrimSpace(*run.Feedback) + "\n\n" + newFeedback
	}
	if err := s.q.SetAgentRunFeedback(ctx, gen.SetAgentRunFeedbackParams{
		Feedback: &combined,
		ID:       *task.CurrentAgentRunID,
	}); err != nil {
		log.Warn("ghsync: set agent run feedback", "err", err)
	}
}

// autoTransitionOnFeedback moves the task back along its workflow's "failure"
// human-triggered path (mirroring the manual Reject action) when new PR
// feedback was ingested and the repo has opted into auto-transition. Best
// effort: any failure (no failure path defined, transition rejected, etc.) is
// logged and swallowed so it can never break the sweep — a human can always
// still transition the task manually.
func (s *Syncer) autoTransitionOnFeedback(ctx context.Context, task gen.Task, log *slog.Logger) {
	if s.engine == nil {
		return
	}
	target, err := s.failurePathTarget(ctx, task)
	if err != nil {
		log.Info("ghsync: no auto-transition failure path from current label", "label", task.Label, "err", err)
		return
	}
	if err := s.engine.Transition(ctx, task.ID, target, workflow.TriggerHuman, "", "GitHub PR review feedback ingested"); err != nil {
		log.Warn("ghsync: auto-transition on PR feedback failed", "to_label", target, "err", err)
		return
	}
	log.Info("ghsync: auto-transitioned task on PR feedback", "to_label", target)
}

// failurePathTarget returns the destination label of the "failure" human
// transition defined for the task's current label, mirroring
// TasksHandler.humanPathTarget (duplicated narrowly here to avoid an
// api/handlers -> ghsync dependency).
func (s *Syncer) failurePathTarget(ctx context.Context, task gen.Task) (string, error) {
	transitions, err := s.q.ListWorkflowTransitions(ctx, task.WorkflowID)
	if err != nil {
		return "", fmt.Errorf("list workflow transitions: %w", err)
	}
	for _, t := range transitions {
		if t.FromLabel == task.Label && t.TriggerType == "human" && t.Path != nil && *t.Path == "failure" {
			return t.ToLabel, nil
		}
	}
	return "", fmt.Errorf("no failure human transition defined from %q", task.Label)
}

func authorOrUnknown(author string) string {
	if author == "" {
		return "a reviewer"
	}
	return author
}
