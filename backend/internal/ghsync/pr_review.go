package ghsync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// ingestPRFeedback checks a task's PR for new changes-requested reviews,
// inline review comments, and failed GHA checks since the last sweep, and
// surfaces them to the agent:
//   - inline review comments (have a file/line anchor) are inserted into
//     task_review_comments (source='github') so they flow through the
//     existing OPEN REVIEW COMMENTS prompt section and resolve loop.
//   - changes_requested review bodies and failed check names/links (no
//     anchor) are appended to the current run's Feedback column, rendered
//     under the FEEDBACK FROM PRIOR REVIEW: prompt section.
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
func (s *Syncer) ingestPRFeedback(ctx context.Context, task gen.Task, repo repoInfo, prNumber int) {
	if prNumber == 0 || s.q == nil {
		return
	}
	log := slog.With("component", "ghsync", "task_id", task.ID, "pr_number", prNumber)

	state, err := s.q.GetTaskPRReviewState(ctx, task.ID)
	if err != nil {
		// No row yet is the common case (first sweep for this task's PR).
		state = gen.TaskPrReviewState{TaskID: task.ID}
	}

	var head ghclient.PRHead
	if s.getPRHead != nil {
		var err error
		head, err = s.getPRHead(ctx, repo.ghName, task.Branch)
		if err != nil {
			log.Warn("ghsync: get PR head", "err", err)
		}
	}

	// A new head SHA means the agent pushed since we last looked: start a
	// fresh feedback cycle so reviews/checks against the old commit don't
	// keep being (re-)considered "new" forever, but also don't re-inject
	// anything we've already surfaced under the old head.
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
	reviews, err := s.getReviews(ctx, repo.ghName, prNumber)
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
// already ingested (deduped by external_id) into task_review_comments with
// source='github'. Returns true if at least one new comment was inserted.
func (s *Syncer) ingestReviewComments(ctx context.Context, task gen.Task, repo repoInfo, prNumber int, log *slog.Logger) bool {
	if s.getReviewComments == nil {
		return false
	}
	comments, err := s.getReviewComments(ctx, repo.ghName, prNumber)
	if err != nil {
		log.Warn("ghsync: get PR review comments", "err", err)
		return false
	}

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

		created, err := s.q.CreateGitHubTaskReviewComment(ctx, gen.CreateGitHubTaskReviewCommentParams{
			ID:         uuid.NewString(),
			TaskID:     task.ID,
			FilePath:   c.Path,
			Side:       side,
			StartLine:  int64(startLine),
			EndLine:    int64(line),
			QuotedText: c.DiffHunk,
			Body:       c.Body,
			ExternalID: &c.ID,
		})
		if err != nil {
			log.Warn("ghsync: create github review comment", "external_id", c.ID, "err", err)
			continue
		}
		inserted = true
		s.hub.Publish("task.review_comment_added", map[string]any{
			"task_id":    task.ID,
			"comment_id": created.ID,
			"source":     "github",
		})
	}
	return inserted
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
	checks, err := s.getFailedChecks(ctx, repo.ghName, prNumber)
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
