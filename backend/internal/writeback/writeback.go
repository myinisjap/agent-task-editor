// Package writeback implements per-repo opt-in status write-back to the
// source issue (GitHub or Gitea — see knownSources) an imported task
// originated from: a label applied when the task first leaves "not_ready",
// a comment posted when its PR opens, and the issue closed (with a comment)
// once that PR merges.
//
// Idempotency is tracked on the task row itself (the writeback_* columns),
// not by scraping issue comments — cheaper, and unaffected by a human
// editing or deleting the marker comment on the forge. Every entry point is
// best-effort: a failed forge call is logged and swallowed, never propagated
// to the caller, so a forge hiccup can't fail a sweep, a workflow
// transition, or an API request. See docs/task-sources.md for the full
// behavior writeup.
package writeback

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// InProgressLabel is the default label applied to the source issue when an
// imported task first leaves "not_ready". Per-repo configurable via
// repos.issue_writeback_label; this constant is the fallback used when that
// column is empty. See docs/task-sources.md for the full behavior writeup.
const InProgressLabel = "agent-in-progress"

// MarkerComment is embedded (as an HTML comment, invisible when rendered) in
// every comment/close body this package posts, purely so a human glancing at
// the issue can tell an agent-task-editor write-back apart from their own
// comments. It plays no role in idempotency — that's tracked in the DB.
// Exported so the issue-sync importer can filter this system's own
// write-back comments out of the ingested comment thread.
const MarkerComment = "<!-- agent-task-editor:writeback -->"

// querier is the subset of *gen.Queries the writeback package needs. Kept
// narrow so tests can substitute a real sqlite-backed *gen.Queries without
// needing to implement the whole generated surface.
type querier interface {
	SetTaskWritebackInProgress(ctx context.Context, id string) error
	SetTaskWritebackPRCommented(ctx context.Context, id string) error
	SetTaskWritebackClosed(ctx context.Context, id string) error
}

// Writeback owns the three write-back actions (add-label, comment, close)
// against a task's source issue. Constructed once and shared across the
// workflow engine's OnLeaveNotReady hook, ghsync's sweep, and the
// CreatePR/GitHubStatus/UpdateGitState HTTP handlers.
type Writeback struct {
	q querier

	// addLabel, commentOnIssue, closeWithComment are this Writeback's default
	// forge functions, used for any task whose source's forge can't be
	// resolved from the repo's remote URL (in practice: GitHub tasks, plus
	// any test that constructs a Writeback via NewWithClient without a real
	// repo row to resolve against). Overridable in tests.
	//
	// Tasks from a Source with its own forge.Forge (e.g. GiteaIssues) are
	// instead routed through resolveForgeForTask, which resolves the actual
	// forge per-repo via forge.ForRemote — see eligible/actionsFor.
	addLabel         func(ctx context.Context, repoName string, issueNumber int, label string) error
	commentOnIssue   func(ctx context.Context, repoName string, issueNumber int, body string) error
	closeWithComment func(ctx context.Context, repoName string, issueNumber int, body string) error
}

// New creates a Writeback backed by the given queries and the real GitHub
// forge.Forge implementation (internal/ghclient) as its default forge.
// Tasks from a non-GitHub source (e.g. Gitea) are still handled correctly:
// see resolveForgeForTask.
func New(q *gen.Queries) *Writeback {
	f := ghclient.GitHub{}
	return &Writeback{
		q:                q,
		addLabel:         f.AddIssueLabel,
		commentOnIssue:   f.CommentOnIssue,
		closeWithComment: f.CloseIssueWithComment,
	}
}

// NewWithClient creates a Writeback backed by the given queries and the given
// gh-calling functions as its default forge. Exported for use by other
// packages' tests (e.g. ghsync, api/handlers) that need to fake out the
// actual `gh` calls without depending on writeback's unexported struct
// fields. Since these tests exercise GitHub-sourced tasks against a repo
// whose remote URL forge.ForRemote won't otherwise resolve (a fake test
// remote), the default functions given here are what actually get called —
// see resolveForgeForTask.
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

// actions is the set of write-back operations needed against one task's
// source issue, resolved once per call by resolveForgeForTask.
type actions struct {
	addLabel         func(ctx context.Context, repoName string, issueNumber int, label string) error
	commentOnIssue   func(ctx context.Context, repoName string, issueNumber int, body string) error
	closeWithComment func(ctx context.Context, repoName string, issueNumber int, body string) error
}

// resolveForgeForTask picks which forge.Forge's methods to call for a given
// task+repo: for a GitHub-sourced task (task.Source == "github"), always
// this Writeback's own default functions (preserving exact prior behavior,
// including in tests that construct a Writeback via NewWithClient against a
// repo with no real/resolvable remote URL). For any other source (e.g.
// "gitea"), resolves the forge from the repo's remote URL via
// forge.ForRemote, so write-back works against whichever forge the task was
// actually imported from rather than being hardcoded to GitHub.
func (wb *Writeback) resolveForgeForTask(task gen.Task, repo gen.Repo) actions {
	if task.Source == "github" {
		return actions{addLabel: wb.addLabel, commentOnIssue: wb.commentOnIssue, closeWithComment: wb.closeWithComment}
	}
	if repo.RemoteUrl != nil {
		if f, _, ok := forge.ForRemote(*repo.RemoteUrl); ok {
			return actions{addLabel: f.AddIssueLabel, commentOnIssue: f.CommentOnIssue, closeWithComment: f.CloseIssueWithComment}
		}
	}
	// No registered forge recognises this repo's remote — fall back to the
	// Writeback's own default functions rather than a nil actions value, so
	// behavior degrades to "attempt it against the default forge and let it
	// fail/log" instead of silently no-op-ing.
	return actions{addLabel: wb.addLabel, commentOnIssue: wb.commentOnIssue, closeWithComment: wb.closeWithComment}
}

// ParseSourceRef parses a tasks.source_ref value of the form "owner/repo#123"
// into its repo name ("owner/repo") and issue number. This shape is shared
// across every forge write-back currently supports (GitHub, Gitea); ok is
// false if ref doesn't match it (missing "/", missing "#", or a non-numeric
// issue number).
func ParseSourceRef(ref string) (repoName string, issueNumber int, ok bool) {
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

// knownSources lists every tasks.source value write-back is eligible to act
// on. Every tasksource.Source's Name() that supports write-back-style issue
// actions (label/comment/close) should appear here — currently "github" and
// "gitea" (see tasksource.GitHubIssues/GiteaIssues). A task from an
// unrecognised source is never eligible, regardless of repo settings.
var knownSources = map[string]bool{
	"github": true,
	"gitea":  true,
}

// eligible reports whether a task is a candidate for any write-back action at
// all: it must have come from a known issue-import source (see knownSources),
// carry a parseable source_ref, and belong to a repo with write-back enabled.
func eligible(task gen.Task, repo gen.Repo) (repoName string, issueNumber int, ok bool) {
	if !knownSources[task.Source] || task.SourceRef == "" {
		return "", 0, false
	}
	if repo.IssueWritebackEnabled == 0 {
		return "", 0, false
	}
	return ParseSourceRef(task.SourceRef)
}

// OnLeaveNotReady applies the repo's configured in-progress label (or
// InProgressLabel if unset) to the task's source issue the first time the
// task moves off "not_ready". Best-effort: on a `gh` failure this still
// marks the flag done rather than retrying forever, since this is explicitly
// the "optional" signal (see docs/task-sources.md) and infinite retry noise
// for a one-time cosmetic label is worse than an occasional miss (e.g. the
// repo not having the label defined yet).
func (wb *Writeback) OnLeaveNotReady(ctx context.Context, task gen.Task, repo gen.Repo) {
	log := slog.With("component", "writeback", "task_id", task.ID)
	if task.WritebackInProgressSent != 0 {
		return
	}
	repoName, issueNumber, ok := eligible(task, repo)
	if !ok {
		return
	}
	label := strings.TrimSpace(repo.IssueWritebackLabel)
	if label == "" {
		label = InProgressLabel
	}
	act := wb.resolveForgeForTask(task, repo)
	if err := act.addLabel(ctx, repoName, issueNumber, label); err != nil {
		log.Warn("writeback: add in-progress label failed", "ref", task.SourceRef, "label", label, "err", err)
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
	repoName, issueNumber, ok := eligible(task, repo)
	if !ok {
		return
	}
	body := fmt.Sprintf("%s\nA pull request has been opened for this issue: %s", MarkerComment, task.PrUrl)
	act := wb.resolveForgeForTask(task, repo)
	if err := act.commentOnIssue(ctx, repoName, issueNumber, body); err != nil {
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
	repoName, issueNumber, ok := eligible(task, repo)
	if !ok {
		return
	}
	body := fmt.Sprintf("%s\nClosing — the pull request for this issue has merged: %s", MarkerComment, task.PrUrl)
	act := wb.resolveForgeForTask(task, repo)
	if err := act.closeWithComment(ctx, repoName, issueNumber, body); err != nil {
		log.Warn("writeback: PR-merged close failed", "ref", task.SourceRef, "err", err)
		return
	}
	if err := wb.q.SetTaskWritebackClosed(ctx, task.ID); err != nil {
		log.Warn("writeback: mark closed done failed", "err", err)
	}
}
