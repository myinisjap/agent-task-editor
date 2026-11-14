package prreview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// maxBodyChars is kept below GitHub's 65536-character comment limit.
const maxBodyChars = 60000

const footer = "_Automated review by ATE. Comment-only: it never approves or requests changes._"

// Poster delivers pending review posts to the forge. Post is the body of the
// workflow engine's pr_review_post outbox hook, so it must be idempotent: the
// hidden marker in every posted body lets a retry detect a post that reached
// the forge before the database was updated.
type Poster struct {
	q        *gen.Queries
	pub      Publisher
	ForgeFor func(remoteURL string) (forge.Forge, string, bool)
}

// NewPoster builds a Poster.
func NewPoster(db *sql.DB, pub Publisher) *Poster {
	return &Poster{q: gen.New(db), pub: pub, ForgeFor: forge.ForRemote}
}

// Post delivers every pending review post of the task, in seq order. A
// transient error is returned so the outbox retries with backoff.
func (p *Poster) Post(ctx context.Context, task gen.Task, repo gen.Repo) error {
	if !forge.IsReviewSource(task.Source) {
		return nil
	}
	pending, err := p.q.ListPendingPRReviewPosts(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("list pending posts: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	f, name, ok := p.ForgeFor(derefStr(repo.RemoteUrl))
	if !ok {
		return fmt.Errorf("no forge for repo %s", repo.Name)
	}
	src, err := p.q.GetTaskPRSource(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("get pr source: %w", err)
	}
	for _, post := range pending {
		if err := p.postOne(ctx, f, name, task, int(src.PrNumber), post); err != nil {
			return err
		}
	}
	if p.pub != nil {
		p.pub.Publish(wsevent.TypeTaskUpdated, wsevent.TaskUpdated{ID: task.ID})
	}
	return nil
}

func (p *Poster) postOne(ctx context.Context, f forge.Forge, repoName string, task gen.Task, number int, post gen.TaskPrReviewPost) error {
	marker := Marker(post.ID)
	done := func(mode, ref string) error {
		if err := p.q.MarkPRReviewPostPosted(ctx, gen.MarkPRReviewPostPostedParams{PostedMode: mode, ForgeRef: ref, ID: post.ID}); err != nil {
			return fmt.Errorf("mark posted: %w", err)
		}
		if err := p.q.SetTaskPRSourceReviewed(ctx, gen.SetTaskPRSourceReviewedParams{ReviewedSha: post.HeadSha, TaskID: task.ID}); err != nil {
			return fmt.Errorf("record reviewed sha: %w", err)
		}
		return nil
	}

	// Idempotency: has this post already reached the forge?
	if existing, err := p.alreadyPosted(ctx, f, repoName, number, marker); err != nil {
		return fmt.Errorf("check existing review: %w", err)
	} else if existing {
		slog.Info("prreview: post already on forge; recording", "task_id", task.ID, "post_id", post.ID)
		return done("existing", "")
	}

	comments := ParseComments(post.CommentsJson)
	prior, err := p.priorFindings(ctx, task.ID, post.Seq)
	if err != nil {
		return err
	}
	resolved := ParseStrings(post.ResolvedJson)

	var inline []forge.ReviewDraftComment
	for i, c := range comments {
		inline = append(inline, forge.ReviewDraftComment{
			Path: c.Path, Line: c.Line, StartLine: c.StartLine,
			Body: fmt.Sprintf("**[%s]** %s", FindingID(post.Seq, i), c.Body),
		})
	}

	body := buildBody(post, resolved, prior, marker, nil)
	ref, err := f.PostCommentReview(ctx, repoName, number, forge.ReviewSubmission{CommitSHA: post.HeadSha, Body: body, Comments: inline})
	if err == nil {
		mode := "review"
		return done(mode, ref)
	}
	if !errors.Is(err, forge.ErrInlineAnchor) {
		return fmt.Errorf("post review: %w", err)
	}

	// Inline anchors rejected: fold the comments into the body.
	var lines []string
	for i, c := range comments {
		lines = append(lines, fmt.Sprintf("- `%s:%d` [%s] %s", c.Path, c.Line, FindingID(post.Seq, i), c.Body))
	}
	body = buildBody(post, resolved, prior, marker, lines)
	ref, err = f.PostCommentReview(ctx, repoName, number, forge.ReviewSubmission{CommitSHA: post.HeadSha, Body: body})
	if err == nil {
		return done("review_no_inline", ref)
	}
	if cerr := f.CommentOnIssue(ctx, repoName, number, body); cerr != nil {
		return fmt.Errorf("post review: %w; comment fallback: %v", err, cerr)
	}
	return done("comment", "")
}

func (p *Poster) alreadyPosted(ctx context.Context, f forge.Forge, repoName string, number int, marker string) (bool, error) {
	reviews, err := f.PRReviews(ctx, repoName, number)
	if err != nil {
		return false, err
	}
	for _, r := range reviews {
		if strings.Contains(r.Body, marker) {
			return true, nil
		}
	}
	comments, err := f.GetIssueComments(ctx, repoName, number)
	if err != nil {
		return false, err
	}
	for _, c := range comments {
		if strings.Contains(c.Body, marker) {
			return true, nil
		}
	}
	return false, nil
}

// priorFindings returns the findings of posted reviews before seq.
func (p *Poster) priorFindings(ctx context.Context, taskID string, seq int64) ([]Finding, error) {
	posted, err := p.q.ListPostedPRReviewPosts(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list posted: %w", err)
	}
	return FindingsFromPosts(posted, seq), nil
}

// FindingsFromPosts flattens the inline comments of posts with seq < before
// (before <= 0 means all) into identified findings.
func FindingsFromPosts(posts []gen.TaskPrReviewPost, before int64) []Finding {
	var out []Finding
	for _, po := range posts {
		if before > 0 && po.Seq >= before {
			continue
		}
		for i, c := range ParseComments(po.CommentsJson) {
			out = append(out, Finding{ID: FindingID(po.Seq, i), Path: c.Path, Line: c.Line, Body: c.Body})
		}
	}
	return out
}

func buildBody(post gen.TaskPrReviewPost, resolved []string, prior []Finding, marker string, inlineFallback []string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(post.Summary))
	if len(inlineFallback) > 0 {
		b.WriteString("\n\n**Findings**\n")
		b.WriteString(strings.Join(inlineFallback, "\n"))
	}
	if len(resolved) > 0 {
		byID := map[string]Finding{}
		for _, f := range prior {
			byID[f.ID] = f
		}
		b.WriteString("\n\n**Resolved since the previous review**\n")
		for _, id := range resolved {
			if f, ok := byID[id]; ok {
				fmt.Fprintf(&b, "- [%s] `%s:%d` %s\n", id, f.Path, f.Line, oneLine(truncate(f.Body, 200)))
			}
		}
	}
	tail := "\n\n---\n" + footer + "\n" + marker
	s := b.String()
	if len([]rune(s))+len(tail) > maxBodyChars {
		s = string([]rune(s)[:maxBodyChars-len(tail)-60]) + "\n\n_[review truncated: too long for the forge]_"
	}
	return s + tail
}
