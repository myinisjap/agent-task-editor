// Package writeback implements per-repo opt-in status write-back to the
// GitHub issue an imported task originated from: a label applied when the
// task first leaves "not_ready", a comment posted when its PR opens, and the
// issue closed (with a comment) once that PR merges.
//
// Idempotency is tracked on the task row itself (the writeback_* columns),
// not by scraping issue comments — cheaper, and unaffected by a human editing
// or deleting the marker comment on GitHub. Every entry point is best-effort:
// a failed `gh` call is logged and swallowed, never propagated to the caller,
// so a GitHub hiccup can't fail a sweep, a workflow transition, or an API
// request. See docs/task-sources.md for the full behavior writeup.
package writeback

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// InProgressLabel is the fixed label applied to the source issue when an
// imported task first leaves "not_ready". Not currently configurable — see
// docs/task-sources.md for why a fixed default was chosen for v2.
const InProgressLabel = "agent-in-progress"

// markerComment is embedded (as an HTML comment, invisible when rendered) in
// every comment/close body this package posts, purely so a human glancing at
// the issue can tell an agent-task-editor write-back apart from their own
// comments. It plays no role in idempotency — that's tracked in the DB.
const markerComment = "<!-- agent-task-editor:writeback -->"

// querier is the subset of *gen.Queries the writeback package needs. Kept
// narrow so tests can substitute a real sqlite-backed *gen.Queries without
// needing to implement the whole generated surface.
type querier interface {
	SetTaskWritebackInProgress(ctx context.Context, id string) error
	SetTaskWritebackPRCommented(ctx context.Context, id string) error
	SetTaskWritebackClosed(ctx context.Context, id string) error
}

// Writeback owns the three GitHub write-back actions. Constructed once and
// shared across the workflow engine's OnLeaveNotReady hook, ghsync's sweep,
// and the CreatePR/GitHubStatus/UpdateGitState HTTP handlers.
type Writeback struct {
	q querier

	// addLabel, commentOnIssue, closeWithComment wrap the corresponding
	// ghclient functions. Overridable in tests.
	addLabel         func(ctx context.Context, repoName string, issueNumber int, label string) error
	commentOnIssue   func(ctx context.Context, repoName string, issueNumber int, body string) error
	closeWithComment func(ctx context.Context, repoName string, issueNumber int, body string) error
}

// New creates a Writeback backed by the given queries and the real gh CLI.
func New(q *gen.Queries) *Writeback {
	return &Writeback{
		q:                q,
		addLabel:         ghclient.AddIssueLabel,
		commentOnIssue:   ghclient.CommentOnIssue,
		closeWithComment: ghclient.CloseIssueWithComment,
	}
}

// NewWithClient creates a Writeback backed by the given queries and the given
// gh-calling functions. Exported for use by other packages' tests (e.g.
// ghsync, api/handlers) that need to fake out the actual `gh` calls without
// depending on writeback's unexported struct fields.
func NewWithClient(
	q *gen.Queries,
	addLabel func(ctx context.Context, repoName string, issueNumber int, label string) error,
	commentOnIssue func(ctx context.Context, repoName string, issueNumber int, body string) error,
	closeWithComment func(ctx context.Context, repoName string, issueNumber int, body string) error,
) *Writeback {
	return &Writeback{
		q:                q,
		addLabel:         addLabel,
		commentOnIssue:   commentOnIssue,
		closeWithComment: closeWithComment,
	}
}

// ParseSourceRef parses a tasks.source_ref value of the form "owner/repo#123"
// into its GitHub repo name ("owner/repo") and issue number. ok is false if
// ref doesn't match that shape (missing "/", missing "#", or a non-numeric
// issue number).
func ParseSourceRef(ref string) (ghName string, issueNumber int, ok bool) {
	hashIdx := strings.LastIndex(ref, "#")
	if hashIdx < 0 || hashIdx == len(ref)-1 {
		return "", 0, false
	}
	name := ref[:hashIdx]
	numStr := ref[hashIdx+1:]
	if !strings.Contains(name, "/") {
		return "", 0, false
	}
	parts := strings.SplitN(name, "/", 2)
	if parts[0] == "" || parts[1] == "" {
		return "", 0, false
	}
	num, err := strconv.Atoi(numStr)
	if err != nil || num <= 0 {
		return "", 0, false
	}
	return name, num, true
}

// eligible reports whether a task is a candidate for any write-back action at
// all: it must have come from the GitHub Issues source (source == "github"),
// carry a parseable source_ref, and belong to a repo with write-back enabled.
func eligible(task gen.Task, repo gen.Repo) (ghName string, issueNumber int, ok bool) {
	if task.Source != "github" || task.SourceRef == "" {
		return "", 0, false
	}
	if repo.IssueWritebackEnabled == 0 {
		return "", 0, false
	}
	return ParseSourceRef(task.SourceRef)
}

// OnLeaveNotReady applies the InProgressLabel to the task's source issue the
// first time the task moves off "not_ready". Best-effort: on a `gh` failure
// this still marks the flag done rather than retrying forever, since this is
// explicitly the "optional" signal (see docs/task-sources.md) and infinite
// retry noise for a one-time cosmetic label is worse than an occasional miss
// (e.g. the repo not having the label defined yet).
func (wb *Writeback) OnLeaveNotReady(ctx context.Context, task gen.Task, repo gen.Repo) {
	log := slog.With("component", "writeback", "task_id", task.ID)
	if task.WritebackInProgressSent != 0 {
		return
	}
	ghName, issueNumber, ok := eligible(task, repo)
	if !ok {
		return
	}
	if err := wb.addLabel(ctx, ghName, issueNumber, InProgressLabel); err != nil {
		log.Warn("writeback: add in-progress label failed", "ref", task.SourceRef, "err", err)
	}
	if err := wb.q.SetTaskWritebackInProgress(ctx, task.ID); err != nil {
		log.Warn("writeback: mark in-progress done failed", "err", err)
	}
}

// OnPROpened comments on the task's source issue with a link to its PR, once
// the task has a PR URL. Safe to call unconditionally whenever a task's PR
// URL is (re)persisted — the writeback_pr_commented flag makes it a no-op
// after the first successful call, and a failed `gh` call leaves the flag
// unset so a later call (next sweep, next handler invocation) retries.
func (wb *Writeback) OnPROpened(ctx context.Context, task gen.Task, repo gen.Repo) {
	log := slog.With("component", "writeback", "task_id", task.ID)
	if task.PrUrl == "" || task.WritebackPrCommented != 0 {
		return
	}
	ghName, issueNumber, ok := eligible(task, repo)
	if !ok {
		return
	}
	body := fmt.Sprintf("%s\nA pull request has been opened for this issue: %s", markerComment, task.PrUrl)
	if err := wb.commentOnIssue(ctx, ghName, issueNumber, body); err != nil {
		log.Warn("writeback: PR-opened comment failed", "ref", task.SourceRef, "err", err)
		return
	}
	if err := wb.q.SetTaskWritebackPRCommented(ctx, task.ID); err != nil {
		log.Warn("writeback: mark PR-commented done failed", "err", err)
	}
}

// OnPRMerged closes the task's source issue with a comment linking the merged
// PR, once the task's git_state is "pr_merged". Same retry semantics as
// OnPROpened: safe to call unconditionally, a no-op once writeback_closed is
// set, and left unset (so it retries) if the `gh` call fails.
func (wb *Writeback) OnPRMerged(ctx context.Context, task gen.Task, repo gen.Repo) {
	log := slog.With("component", "writeback", "task_id", task.ID)
	if task.GitState != "pr_merged" || task.WritebackClosed != 0 {
		return
	}
	ghName, issueNumber, ok := eligible(task, repo)
	if !ok {
		return
	}
	body := fmt.Sprintf("%s\nClosing — the pull request for this issue has merged: %s", markerComment, task.PrUrl)
	if err := wb.closeWithComment(ctx, ghName, issueNumber, body); err != nil {
		log.Warn("writeback: PR-merged close failed", "ref", task.SourceRef, "err", err)
		return
	}
	if err := wb.q.SetTaskWritebackClosed(ctx, task.ID); err != nil {
		log.Warn("writeback: mark closed done failed", "err", err)
	}
}
