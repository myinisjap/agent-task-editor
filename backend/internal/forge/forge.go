// Package forge defines the seam between agent-task-editor's core logic
// (ghsync, tasksource, writeback, the PR-creation HTTP handlers) and a
// specific git-hosting provider ("forge"). It owns the forge-neutral data
// types shared across providers and the Forge interface itself.
//
// Two implementations ship today: GitHub (see internal/ghclient, a thin `gh`
// CLI wrapper) and Gitea (see internal/forge/gitea, direct REST calls). Every
// operation here is deliberately provider-agnostic: no field or method name
// leaks GitHub-specific vocabulary that a self-hosted Gitea or GitLab
// instance couldn't also satisfy.
//
// forge is a leaf package: it must never import ghclient, ghsync,
// tasksource, writeback, or metrics, so any package may depend on it without
// risking an import cycle.
package forge

import "context"

// Mergeability is a forge's verdict on whether a PR/MR can be merged into its
// base branch without conflicts. Forges typically compute this asynchronously
// after each push to either branch, so MergeableUnknown is a normal
// transient answer rather than an error.
//
// Implementations MUST map any value they don't recognise (including a
// forge-specific enum member added after this code was written) to
// MergeableUnknown rather than guessing — never report MergeableConflicting
// unless the forge unambiguously said so. Silently misreporting "conflicting"
// would inject false feedback into an agent's run; misreporting "clean" would
// hide a real conflict. Unknown is always the safe default.
type Mergeability string

const (
	MergeableUnknown     Mergeability = "unknown"     // the forge hasn't computed the test merge yet, or returned an unrecognised value
	MergeableClean       Mergeability = "mergeable"   // merges cleanly into the base branch
	MergeableConflicting Mergeability = "conflicting" // conflicts with the base branch
)

// PRHead holds a PR/MR's number, current head commit SHA, base branch,
// mergeability, normalised state, web URL, and last-updated timestamp. Used
// by ghsync both to detect when the agent has pushed a new commit / whether
// the PR currently conflicts with its base, and (since #340) as the single
// forge call backing PR-state sync too — it returns the same normalised
// state/URL contract as PRForBranch (including "branch exists on the remote
// but has no PR yet" -> State: "pushed"), so ghsync no longer needs a
// separate PRForBranch call every sweep just to detect a git-state change.
type PRHead struct {
	Number    int
	HeadSHA   string
	BaseRef   string
	Mergeable Mergeability
	// State is the normalised PR/MR state: "" (branch not on the remote
	// yet), "pushed" (branch pushed, no PR yet), "pr_open", "pr_merged", or
	// "pr_closed" — the same value set PRForBranch returns.
	State string
	// URL is the PR/MR's web URL; "" when no PR exists yet.
	URL string
	// UpdatedAt is the PR's last-updated timestamp (RFC3339), used to gate
	// review/comment/check ingestion on whether anything could plausibly
	// have changed since the last sweep. "" if the forge implementation
	// doesn't report one — callers must treat that as "always fetch" rather
	// than silently skipping ingestion forever.
	UpdatedAt string
}

// Review is a single review left on a PR/MR (a "changes requested"/
// "approved"/"commented" submission with a body, as opposed to an inline
// review comment — see PRReviewComment).
type Review struct {
	ID          string
	State       string // "APPROVED", "CHANGES_REQUESTED", "COMMENTED", etc (uppercase)
	Body        string
	Author      string
	SubmittedAt string // RFC3339 timestamp string, compared lexically for cursor purposes
}

// PRReviewComment is a single inline (file/line-anchored) review comment left
// on a PR/MR's diff.
type PRReviewComment struct {
	ID        string
	Path      string
	Line      int    // the line the comment is anchored to; 0 if the comment is on an outdated/removed diff position
	StartLine int    // for multi-line comments; equals Line when the comment spans a single line
	Side      string // "LEFT" or "RIGHT" (maps to our "old"/"new")
	Body      string
	DiffHunk  string
	CommitID  string
	Author    string
	CreatedAt string
}

// Check is a single CI/status check result on a PR/MR.
type Check struct {
	Name string
	Link string
	// Bucket is a coarse classification: "pass", "fail", "pending", "skipping", "cancel".
	Bucket string
}

// Issue is an open issue/ticket as returned by a forge's issue-list API.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
	Labels []string // label names only
	// AuthorAssociation is the issue reporter's relationship to the repo:
	// OWNER | MEMBER | COLLABORATOR | CONTRIBUTOR | NONE | ... (same value
	// set as IssueComment.AuthorAssociation). Used by internal/intake's
	// match_author_assoc rule condition to gate auto-start decisions on
	// otherwise-untrusted imported issue content (see #331). Empty when a
	// forge implementation hasn't populated it.
	AuthorAssociation string
}

// IssueComment is a single comment on an issue (not a PR/MR review comment —
// see PRReviewComment).
type IssueComment struct {
	ID                string // forge comment id, stringified
	Author            string
	AuthorAssociation string // OWNER | MEMBER | COLLABORATOR | CONTRIBUTOR | NONE ...
	Body              string
	CreatedAt         string // RFC3339
}

// Forge captures the handful of git-hosting-provider operations that
// agent-task-editor actually uses: PR/MR state sync and creation, PR
// feedback ingestion (reviews/comments/checks), issue import (including
// comments), write-back to the source issue, and auth/URL helpers.
//
// Implementations must preserve the cross-forge contract already relied on
// by ghsync/tasksource/writeback:
//   - normalised PR states are exactly "pushed", "pr_open", "pr_merged", "pr_closed"
//   - Mergeability follows the MergeableUnknown-is-the-safe-default rule above
//   - ListOpenIssues and GetIssueComments must return the COMPLETE result set,
//     never silently capped/truncated (see issue #265) — reconciliation of
//     disappeared issues and comment-cursor ingestion both depend on a
//     complete fetch.
type Forge interface {
	// Name identifies this forge implementation (e.g. "github", "gitea"),
	// matching the corresponding tasksource.Source's Name() for the same
	// forge. Used to tag data ingested independently of tasksource's own
	// import sweep — e.g. ghsync's inline PR-review-comment ingestion
	// (task_review_comments.source) — with which forge it actually came
	// from, rather than assuming GitHub.
	Name() string

	// ParseRepoName extracts this forge's canonical repo name (e.g.
	// "org/repo") from a git remote URL, and reports whether remoteURL
	// belongs to this forge at all.
	ParseRepoName(remoteURL string) (name string, ok bool)

	// PRForBranch returns the normalised state ("pushed", "pr_open",
	// "pr_merged", "pr_closed", or "" if the branch doesn't exist on the
	// remote yet), the PR/MR web URL, and its number for the given repo+branch.
	PRForBranch(ctx context.Context, repoName, branch string) (state, prURL string, prNumber int, err error)

	// CreatePR opens a PR/MR for branch against base (or the repo's default
	// branch if base is empty), or returns the existing one if already open
	// (idempotent). Returns the normalised state and the PR/MR web URL.
	CreatePR(ctx context.Context, repoName, branch, base, title, body string) (state, prURL string, err error)

	// PRHead returns the PR/MR number, head commit SHA, base branch,
	// mergeability, normalised state, web URL, and last-updated timestamp
	// for the given repo+branch — the single forge call ghsync uses per
	// sweep per task for both PR-state sync and feedback ingestion (see
	// PRHead's doc comment). Returns a zero PRHead (State: "") if the
	// branch doesn't exist on the remote yet, or PRHead{State: "pushed"} if
	// the branch is pushed but has no PR yet.
	PRHead(ctx context.Context, repoName, branch string) (PRHead, error)

	// PRReviews returns all reviews submitted on the given PR/MR, oldest first.
	PRReviews(ctx context.Context, repoName string, prNumber int) ([]Review, error)

	// PRReviewComments returns all inline review comments on the given PR/MR's
	// diff, across all reviews.
	PRReviewComments(ctx context.Context, repoName string, prNumber int) ([]PRReviewComment, error)

	// FailedChecks returns the checks on the given PR/MR that failed or were
	// cancelled (pending/skipped/passing checks are excluded).
	FailedChecks(ctx context.Context, repoName string, prNumber int) ([]Check, error)

	// ListOpenIssues returns every open issue for repoName, optionally
	// filtered to issues carrying the given label (empty = all open issues).
	// MUST be a complete, un-truncated fetch (see the Forge doc comment).
	ListOpenIssues(ctx context.Context, repoName, label string) ([]Issue, error)

	// GetIssueComments returns every comment on the given issue, oldest
	// first. MUST be a complete, un-truncated fetch (see the Forge doc comment).
	GetIssueComments(ctx context.Context, repoName string, issueNumber int) ([]IssueComment, error)

	// AddIssueLabel adds a label to an issue. The label must already exist on
	// the repo's label set.
	AddIssueLabel(ctx context.Context, repoName string, issueNumber int, label string) error

	// CommentOnIssue posts a comment on an issue.
	CommentOnIssue(ctx context.Context, repoName string, issueNumber int, body string) error

	// CloseIssueWithComment closes an issue and posts a comment in the same call.
	CloseIssueWithComment(ctx context.Context, repoName string, issueNumber int, body string) error

	// AuthStatus reports whether this forge's client is authenticated, plus a
	// short human-readable note about how (or why not).
	AuthStatus() (authed bool, note string)

	// CompareURL builds a web URL that opens a pre-filled "create PR/MR" page
	// for branch against base, without requiring API auth. Used by the
	// one-click "open PR in browser" flow.
	CompareURL(repoName, base, branch, title, body string) string
}

// registry holds every Forge implementation available for selection by
// ForRemote, in priority order. Populated by each implementation's package
// (see ghclient's init) rather than hardcoded here, so adding a new forge
// (e.g. Gitea, GitLab) never requires editing this file — only registering
// it. Kept unexported; use Register to append to it.
var registry []Forge

// Register adds a Forge implementation to the set ForRemote considers. Meant
// to be called from an implementation package's init() function. Order of
// registration is the order ForRemote tries ParseRepoName in, so the
// first-registered implementation wins ties (shouldn't normally happen,
// since ParseRepoName is expected to key off the remote's host).
func Register(f Forge) {
	registry = append(registry, f)
}

// ForRemote returns the first registered Forge whose ParseRepoName
// recognises remoteURL, along with the parsed repo name. ok is false if no
// registered forge recognises the URL (e.g. a self-hosted instance whose
// forge implementation hasn't been added/registered yet).
//
// This is the extension point for supporting additional self-hosted forges:
// a future implementation registers itself (directly, or gated behind a
// config/env default such as a configured self-hosted host list) and
// ForRemote picks it up automatically — no caller of ForRemote needs to
// change.
func ForRemote(remoteURL string) (f Forge, repoName string, ok bool) {
	for _, cand := range registry {
		if name, ok := cand.ParseRepoName(remoteURL); ok {
			return cand, name, true
		}
	}
	return nil, "", false
}
