package ghclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
)

// rawPullRequest is the subset of GET /pulls payloads the review path reads.
type rawPullRequest struct {
	Number            int    `json:"number"`
	Title             string `json:"title"`
	Body              string `json:"body"`
	HTMLURL           string `json:"html_url"`
	State             string `json:"state"`
	Draft             bool   `json:"draft"`
	MergedAt          string `json:"merged_at"`
	AuthorAssociation string `json:"author_association"`
	User              struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (r rawPullRequest) toForge() forge.PullRequest {
	pr := forge.PullRequest{
		Number:            r.Number,
		Title:             r.Title,
		Body:              r.Body,
		URL:               r.HTMLURL,
		Author:            r.User.Login,
		AuthorAssociation: strings.ToUpper(r.AuthorAssociation),
		AuthorIsBot:       strings.EqualFold(r.User.Type, "Bot") || strings.HasSuffix(strings.ToLower(r.User.Login), "[bot]"),
		Draft:             r.Draft,
		HeadRef:           r.Head.Ref,
		HeadSHA:           r.Head.SHA,
		BaseRef:           r.Base.Ref,
		State:             "open",
	}
	if r.Head.Repo != nil {
		pr.HeadRepo = r.Head.Repo.FullName
	}
	if r.MergedAt != "" {
		pr.State = "merged"
	} else if strings.EqualFold(r.State, "closed") {
		pr.State = "closed"
	}
	for _, l := range r.Labels {
		pr.Labels = append(pr.Labels, l.Name)
	}
	return pr
}

// GetPR returns one pull request's metadata.
func GetPR(ctx context.Context, repoName string, number int) (forge.PullRequest, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_get").Inc()
	b, _, err := apiDo(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repoPath(repoName), number), nil)
	if err != nil {
		return forge.PullRequest{}, fmt.Errorf("github get pr: %w", err)
	}
	var raw rawPullRequest
	if err := json.Unmarshal(b, &raw); err != nil {
		return forge.PullRequest{}, fmt.Errorf("github get pr: %w", err)
	}
	return raw.toForge(), nil
}

// ListOpenPRs returns every open pull request, following pagination.
func ListOpenPRs(ctx context.Context, repoName string) ([]forge.PullRequest, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_list").Inc()
	var out []forge.PullRequest
	err := apiPages(ctx, fmt.Sprintf("%s/pulls?state=open&per_page=100", repoPath(repoName)), func(b []byte) error {
		var page []rawPullRequest
		if err := json.Unmarshal(b, &page); err != nil {
			return err
		}
		for _, r := range page {
			out = append(out, r.toForge())
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("github list prs: %w", err)
	}
	return out, nil
}

// PostCommentReview posts a COMMENT-type review (never APPROVE or
// REQUEST_CHANGES: the event is hard-coded). A 422 that mentions an inline
// position wraps forge.ErrInlineAnchor.
func PostCommentReview(ctx context.Context, repoName string, number int, r forge.ReviewSubmission) (string, error) {
	metrics.GhCallsTotal.WithLabelValues("pr_review_post").Inc()
	payload := map[string]any{"event": "COMMENT", "body": r.Body}
	if r.CommitSHA != "" {
		payload["commit_id"] = r.CommitSHA
	}
	if len(r.Comments) > 0 {
		cs := make([]map[string]any, 0, len(r.Comments))
		for _, c := range r.Comments {
			m := map[string]any{"path": c.Path, "line": c.Line, "side": "RIGHT", "body": c.Body}
			if c.StartLine > 0 && c.StartLine < c.Line {
				m["start_line"] = c.StartLine
				m["start_side"] = "RIGHT"
			}
			cs = append(cs, m)
		}
		payload["comments"] = cs
	}
	b, _, err := apiDo(ctx, http.MethodPost, fmt.Sprintf("%s/pulls/%d/reviews", repoPath(repoName), number), payload)
	if err != nil {
		var ae *apiError
		if len(r.Comments) > 0 && errors.As(err, &ae) && ae.Status == http.StatusUnprocessableEntity {
			low := strings.ToLower(ae.Body)
			if strings.Contains(low, "line") || strings.Contains(low, "path") || strings.Contains(low, "pull_request_review_thread") {
				return "", fmt.Errorf("github post review: %w: %v", forge.ErrInlineAnchor, err)
			}
		}
		return "", fmt.Errorf("github post review: %w", err)
	}
	var resp struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(b, &resp)
	return fmt.Sprint(resp.ID), nil
}
