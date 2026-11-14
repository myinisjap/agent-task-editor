// Package reviewreply posts the agent's resolution of an ingested forge review
// comment back onto the originating review thread (opt-in per repo via
// repos.review_reply_enabled; optionally also resolving the thread via
// review_reply_resolve_enabled). It runs from the workflow side-effect outbox
// (kind review_reply), so it is retried with backoff and failures surface on
// the task like other write-back failures.
//
// A reply is only posted once the task's branch is pushed and the forge's PR
// head matches the local branch head, so a reply never points at code that is
// not on the PR. task_review_comments.reply_posted_at is the once-per-comment
// idempotency marker: it is set right after the forge accepts the reply, so a
// retry never double-posts.
package reviewreply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	_ "github.com/myinisjap/agent-task-editor/backend/internal/ghclient" // registers the GitHub forge.Forge implementation
	"github.com/myinisjap/agent-task-editor/backend/internal/gitcmd"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Marker is embedded (invisible when rendered) in every reply ATE posts so
// ingestion can recognise its own replies and never treat them as feedback.
const Marker = "<!-- agent-task-editor:review-reply -->"

// Prefix makes the reply visibly come from the agent.
const Prefix = "**Agent (agent-task-editor):** "

// MaxNoteRunes caps the agent's resolution text.
const MaxNoteRunes = 1000

// ErrNotPushed means the branch is not (yet) on the forge at the local head.
var ErrNotPushed = errors.New("branch head is not pushed to the PR yet")

// Replier posts review-thread replies.
type Replier struct {
	q *gen.Queries
	// localHead resolves the task branch's local head SHA. Overridable in tests.
	localHead func(ctx context.Context, dir, branch string) (string, error)
	// forgeFor resolves the forge for a remote URL. Overridable in tests.
	forgeFor func(remoteURL string) (forge.Forge, string, bool)
}

// New returns a Replier backed by q and the registered forges.
func New(q *gen.Queries) *Replier {
	return &Replier{q: q, localHead: gitHead, forgeFor: forge.ForRemote}
}

func gitHead(ctx context.Context, dir, branch string) (string, error) {
	if !gitcmd.IsValidRef(branch) {
		return "", fmt.Errorf("invalid branch %q", branch)
	}
	out, err := gitcmd.Output(ctx, dir, "rev-parse", "--verify", branch+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Body builds the reply text: prefix, the agent's note (length-capped, else
// as written), the short SHA of the pushed head, and the hidden marker.
func Body(note, headSHA string) string {
	note = strings.TrimSpace(note)
	if r := []rune(note); len(r) > MaxNoteRunes {
		note = strings.TrimSpace(string(r[:MaxNoteRunes])) + "..."
	}
	if note == "" {
		note = "Addressed."
	}
	short := headSHA
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf("%s%s\n\nPushed in `%s`.\n\n%s", Prefix, note, short, Marker)
}

// IsOwnReply reports whether a forge review comment is one ATE posted: it
// carries the marker, or its id is recorded as a reply on one of the task's
// comments.
func IsOwnReply(ctx context.Context, q *gen.Queries, taskID string, c forge.PRReviewComment) bool {
	if strings.Contains(c.Body, Marker) {
		return true
	}
	if c.ID == "" {
		return false
	}
	n, err := q.CountTaskReviewCommentReplyByExternalID(ctx, gen.CountTaskReviewCommentReplyByExternalIDParams{
		TaskID: taskID, ReplyExternalID: &c.ID,
	})
	return err == nil && n > 0
}

// Run posts the pending replies for task. It returns nil when there is nothing
// to do (feature off, no remote, no pending comments) and an error to be
// retried otherwise (including ErrNotPushed).
func (r *Replier) Run(ctx context.Context, task gen.Task) error {
	repo, err := r.q.GetRepo(ctx, task.RepoID)
	if err != nil {
		return fmt.Errorf("get repo: %w", err)
	}
	if repo.ReviewReplyEnabled == 0 || repo.RemoteUrl == nil || task.Branch == "" {
		return nil
	}
	f, name, ok := r.forgeFor(*repo.RemoteUrl)
	if !ok {
		return nil
	}
	pending, err := r.q.ListUnrepliedAgentResolvedForgeReviewComments(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("list resolved comments: %w", err)
	}
	// Only comments ingested from this repo's own forge can be replied to.
	var todo []gen.TaskReviewComment
	for _, c := range pending {
		if c.ExternalID != nil && *c.ExternalID != "" && c.Source == f.Name() {
			todo = append(todo, c)
		}
	}
	if len(todo) == 0 {
		return nil
	}

	head, err := f.PRHead(ctx, name, task.Branch)
	if err != nil {
		return fmt.Errorf("get PR head: %w", err)
	}
	if head.Number == 0 || head.HeadSHA == "" {
		return fmt.Errorf("%w: no PR for branch %s", ErrNotPushed, task.Branch)
	}
	dir := repo.Path
	if task.WorktreePath != "" && gitcmd.DirExists(task.WorktreePath) {
		dir = task.WorktreePath
	}
	local, err := r.localHead(ctx, dir, task.Branch)
	if err != nil {
		return fmt.Errorf("resolve local head: %w", err)
	}
	if local != head.HeadSHA {
		return fmt.Errorf("%w: local %.7s, PR head %.7s", ErrNotPushed, local, head.HeadSHA)
	}

	var firstErr error
	for _, c := range todo {
		note := ""
		if c.ResolutionNote != nil {
			note = *c.ResolutionNote
		}
		replyID, err := f.ReplyToReviewComment(ctx, name, head.Number, *c.ExternalID, Body(note, head.HeadSHA))
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reply to comment %s: %w", *c.ExternalID, err)
			}
			continue
		}
		if err := r.q.MarkTaskReviewCommentReplied(ctx, gen.MarkTaskReviewCommentRepliedParams{
			ReplyExternalID: &replyID, ID: c.ID, TaskID: task.ID,
		}); err != nil {
			// The reply is already on the forge; retrying would double-post.
			slog.Error("reviewreply: reply posted but marker not saved", "comment_id", c.ID, "err", err)
			continue
		}
		if repo.ReviewReplyResolveEnabled != 0 {
			// Best-effort: the reply is already recorded, so a failure here is
			// logged rather than retried.
			if err := f.ResolveReviewThread(ctx, name, head.Number, *c.ExternalID); err != nil && !errors.Is(err, forge.ErrThreadResolveUnsupported) {
				slog.Warn("reviewreply: resolve thread", "comment_id", c.ID, "err", err)
			}
		}
	}
	return firstErr
}
