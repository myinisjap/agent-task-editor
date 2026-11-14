package distill

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// Evidence kinds.
const (
	KindRejection      = "rejection"
	KindHumanReply     = "human_reply"
	KindReviewFeedback = "review_feedback"
	KindVerifyFailure  = "verify_failure"
	KindShouldHaveAsk  = "should_have_asked"
)

// Evidence is one piece of what went wrong on a task. Text is untrusted.
type Evidence struct {
	TaskID string
	Title  string
	Kind   string
	Text   string
	At     time.Time
}

const (
	perSourceLimit    = 200
	minVerifyFailures = 2
	maxVerifyOutput   = 600
)

// GatherEvidence collects a repo's evidence since a cutoff, newest first,
// capped at MaxEvidence. Moves made by the pr-feedback-sync and policy actors
// are not human decisions and never appear (the SQL excludes them).
func GatherEvidence(ctx context.Context, q *gen.Queries, repoID string, since time.Time) ([]Evidence, error) {
	s := storage.FormatTime(since)
	var out []Evidence

	rej, err := q.ListRepoRejectionNotes(ctx, gen.ListRepoRejectionNotesParams{RepoID: repoID, Since: s, MaxRows: perSourceLimit})
	if err != nil {
		return nil, err
	}
	for _, r := range rej {
		out = append(out, Evidence{TaskID: r.TaskID, Title: r.Title, Kind: KindRejection, Text: r.Note, At: r.CreatedAt})
	}

	replies, err := q.ListRepoHumanReplies(ctx, gen.ListRepoHumanRepliesParams{RepoID: repoID, Since: s, MaxRows: perSourceLimit})
	if err != nil {
		return nil, err
	}
	for _, r := range replies {
		text := r.Reply
		if q := strings.TrimSpace(r.Question); q != "" {
			text = "agent asked: " + q + " | human replied: " + r.Reply
		}
		at := time.Time{}
		if r.RepliedAt != nil {
			at = *r.RepliedAt
		}
		out = append(out, Evidence{TaskID: r.TaskID, Title: r.Title, Kind: KindHumanReply, Text: text, At: at})
	}

	// Feedback is copied onto later runs of the same task; keep one per text.
	fb, err := q.ListRepoRunFeedback(ctx, gen.ListRepoRunFeedbackParams{RepoID: repoID, Since: s, MaxRows: perSourceLimit})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, r := range fb {
		key := r.TaskID + "\x00" + strings.TrimSpace(r.Feedback)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Evidence{TaskID: r.TaskID, Title: r.Title, Kind: KindReviewFeedback, Text: r.Feedback, At: r.CreatedAt})
	}

	vf, err := q.ListRepoVerifyFailures(ctx, gen.ListRepoVerifyFailuresParams{RepoID: repoID, Since: s, MaxRows: perSourceLimit})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, r := range vf {
		counts[r.TaskID]++
	}
	doneVerify := map[string]bool{}
	for _, r := range vf { // newest first: the latest failure represents the task
		if counts[r.TaskID] < minVerifyFailures || doneVerify[r.TaskID] {
			continue
		}
		doneVerify[r.TaskID] = true
		text := "verify command `" + r.Command + "` " + r.Status + " " + strconv.Itoa(counts[r.TaskID]) + " times; last output: " + tail(r.Output, maxVerifyOutput)
		out = append(out, Evidence{TaskID: r.TaskID, Title: r.Title, Kind: KindVerifyFailure, Text: text, At: r.CreatedAt})
	}

	sha, err := q.ListRepoShouldHaveAsked(ctx, gen.ListRepoShouldHaveAskedParams{RepoID: repoID, Since: s, MaxRows: perSourceLimit})
	if err != nil {
		return nil, err
	}
	for _, r := range sha {
		text := strings.TrimSpace(r.Note)
		if text == "" {
			text = "a person marked this task as one the autonomy policy should have asked about"
		}
		out = append(out, Evidence{TaskID: r.TaskID, Title: r.Title, Kind: KindShouldHaveAsk, Text: text, At: r.CreatedAt})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > MaxEvidence {
		out = out[:MaxEvidence]
	}
	return out, nil
}

// tail returns the last n runes of s.
func tail(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return "…" + string(r[len(r)-n:])
}
