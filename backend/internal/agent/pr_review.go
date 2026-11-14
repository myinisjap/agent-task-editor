package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/prreview"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const (
	// maxPRDiffBytes caps the diff embedded in the review prompt.
	maxPRDiffBytes = 150 * 1024
	// maxPRReviewComments caps inline comments stored per review.
	maxPRReviewComments = 50
)

// loadPRReviewInput builds the PRReviewInput for a review-only PR task from
// its task_pr_source row and the findings of earlier posted reviews. The diff
// is filled in by the pool once the worktree exists.
func loadPRReviewInput(ctx context.Context, q *gen.Queries, taskID string) (*PRReviewInput, error) {
	src, err := q.GetTaskPRSource(ctx, taskID)
	if err != nil {
		return nil, err
	}
	in := &PRReviewInput{
		Title: src.PrTitle, Body: src.PrBody, Author: src.PrAuthor, URL: src.PrUrl, BaseRef: src.BaseRef, HeadSHA: src.HeadSha,
	}
	if posted, perr := q.ListPostedPRReviewPosts(ctx, taskID); perr == nil {
		for _, f := range prreview.FindingsFromPosts(posted, 0) {
			in.PriorFindings = append(in.PriorFindings, PriorFinding{ID: f.ID, Path: f.Path, Line: f.Line, Body: f.Body})
		}
	}
	return in, nil
}

// fillPRDiff computes the capped review diff (PR head vs. its merge-base with
// the base ref) into in.Diff.
func fillPRDiff(ctx context.Context, in *PRReviewInput, wtPath, baseRef string) {
	if baseRef == "" {
		baseRef = "origin/" + in.BaseRef
	}
	d, err := diffAgainstBase(ctx, wtPath, baseRef, "HEAD")
	if err != nil {
		slog.Warn("pool: PR review diff failed", "err", err)
		in.Diff = ""
		return
	}
	if len(d) > maxPRDiffBytes {
		d = d[:maxPRDiffBytes] + "\n[diff truncated: run `git diff $(git merge-base " + baseRef + " HEAD) HEAD` in the worktree for the rest]\n"
	}
	in.Diff = d
}

// sanitizePRReview drops comments with an empty/escaping path or a
// non-positive line, caps the count, and applies the summary fallback.
func sanitizePRReview(r *PRReviewResult, result Result) PRReviewResult {
	var out PRReviewResult
	if r != nil {
		out.Summary = strings.TrimSpace(r.Summary)
		out.ResolvedPrior = r.ResolvedPrior
		for _, c := range r.Comments {
			p := path.Clean(strings.TrimSpace(c.Path))
			if p == "" || p == "." || strings.HasPrefix(p, "/") || p == ".." || strings.HasPrefix(p, "../") || c.Line <= 0 || strings.TrimSpace(c.Body) == "" {
				continue
			}
			c.Path = p
			if len(out.Comments) >= maxPRReviewComments {
				break
			}
			out.Comments = append(out.Comments, c)
		}
	}
	if out.Summary == "" {
		switch {
		case result.Notes != nil && strings.TrimSpace(*result.Notes) != "":
			out.Summary = strings.TrimSpace(*result.Notes)
		case result.Message != nil && strings.TrimSpace(*result.Message) != "":
			out.Summary = strings.TrimSpace(*result.Message)
		default:
			out.Summary = "No findings reported."
		}
	}
	return out
}

// recordPRReview stores the finished review as a pending post. It must run
// before the success transition: that transition enqueues the pr_review_post
// outbox effect, which looks the row up.
func (p *Pool) recordPRReview(ctx context.Context, job Job, result Result, log *slog.Logger) {
	taskID := job.Input.Task.ID
	rev := sanitizePRReview(result.PRReview, result)
	comments := make([]prreview.StoredComment, 0, len(rev.Comments))
	for _, c := range rev.Comments {
		comments = append(comments, prreview.StoredComment{Path: c.Path, Line: c.Line, StartLine: c.StartLine, Body: c.Body})
	}
	cj, _ := json.Marshal(comments)
	resolved := rev.ResolvedPrior
	if resolved == nil {
		resolved = []string{}
	}
	if job.Input.PRReview != nil {
		known := map[string]bool{}
		for _, f := range job.Input.PRReview.PriorFindings {
			known[f.ID] = true
		}
		kept := make([]string, 0, len(resolved))
		for _, id := range resolved {
			if known[id] {
				kept = append(kept, id)
			}
		}
		resolved = kept
	}
	rj, _ := json.Marshal(resolved)

	headSHA := ""
	if job.Input.PRReview != nil {
		headSHA = job.Input.PRReview.HeadSHA
	}
	seq, err := p.q.NextPRReviewPostSeq(ctx, taskID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Warn("pool: next PR review seq", "err", err)
		return
	}
	if err := p.q.CreatePRReviewPost(ctx, gen.CreatePRReviewPostParams{
		ID: uuid.NewString(), TaskID: taskID, AgentRunID: &job.RunID, HeadSha: headSHA,
		Summary: rev.Summary, CommentsJson: string(cj), ResolvedJson: string(rj), Seq: seq,
	}); err != nil {
		log.Warn("pool: record PR review", "err", err)
	}
}

// isReviewJob reports whether the job is a review-only PR run, which must
// never commit or push.
func isReviewJob(job Job) bool {
	return job.Input.PRReview != nil || forge.IsReviewSource(job.Input.Task.Source)
}
