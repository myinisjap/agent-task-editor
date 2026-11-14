package gitea

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
)

// giteaFullPR is the PR payload fields the review-only path reads.
type giteaFullPR struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	HTMLURL string `json:"html_url"`
	Draft   *bool  `json:"draft"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref  string `json:"ref"`
		Sha  string `json:"sha"`
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

// draftTitle reports whether a PR title carries one of Gitea's work-in-progress
// prefixes.
func draftTitle(t string) bool {
	l := strings.ToLower(strings.TrimSpace(t))
	for _, p := range []string{"wip:", "[wip]", "draft:", "[draft]"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func (g *Gitea) toForgePR(ctx context.Context, repoName string, r giteaFullPR, cache map[string]bool) forge.PullRequest {
	pr := forge.PullRequest{
		Number:      r.Number,
		Title:       r.Title,
		Body:        r.Body,
		URL:         r.HTMLURL,
		Author:      r.User.Login,
		AuthorIsBot: strings.HasSuffix(strings.ToLower(r.User.Login), "[bot]"),
		HeadRef:     r.Head.Ref,
		HeadSHA:     r.Head.Sha,
		BaseRef:     r.Base.Ref,
		State:       "open",
	}
	if r.Draft != nil {
		pr.Draft = *r.Draft
	} else {
		pr.Draft = draftTitle(r.Title)
	}
	if r.Head.Repo != nil {
		pr.HeadRepo = r.Head.Repo.FullName
	}
	if r.Merged {
		pr.State = "merged"
	} else if strings.EqualFold(r.State, "closed") {
		pr.State = "closed"
	}
	for _, l := range r.Labels {
		pr.Labels = append(pr.Labels, l.Name)
	}
	// Gitea has no author_association: the repo owner is OWNER, otherwise
	// collaborator-or-none.
	if owner, _, ok := strings.Cut(repoName, "/"); ok && strings.EqualFold(owner, r.User.Login) {
		pr.AuthorAssociation = "OWNER"
	} else {
		pr.AuthorAssociation = g.associationFor(ctx, repoName, r.User.Login, cache)
	}
	return pr
}

// GetPR implements forge.Forge.
func (g *Gitea) GetPR(ctx context.Context, repoName string, number int) (forge.PullRequest, error) {
	metrics.GiteaCallsTotal.WithLabelValues("pr_get").Inc()
	var raw giteaFullPR
	if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repoName, number), nil, &raw); err != nil {
		return forge.PullRequest{}, fmt.Errorf("gitea get pr: %w", err)
	}
	return g.toForgePR(ctx, repoName, raw, map[string]bool{}), nil
}

// ListOpenPRs implements forge.Forge; paginates through the complete set.
func (g *Gitea) ListOpenPRs(ctx context.Context, repoName string) ([]forge.PullRequest, error) {
	const pageSize = 50
	var out []forge.PullRequest
	cache := map[string]bool{}
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("state", "open")
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("page", strconv.Itoa(page))
		metrics.GiteaCallsTotal.WithLabelValues("pr_list").Inc()
		var raw []giteaFullPR
		if _, err := g.apiCall(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls?%s", repoName, q.Encode()), nil, &raw); err != nil {
			return nil, fmt.Errorf("gitea list prs: %w", err)
		}
		for _, r := range raw {
			out = append(out, g.toForgePR(ctx, repoName, r, cache))
		}
		if len(raw) < pageSize {
			break
		}
	}
	return out, nil
}

// PostCommentReview implements forge.Forge. The event is always COMMENT.
func (g *Gitea) PostCommentReview(ctx context.Context, repoName string, number int, r forge.ReviewSubmission) (string, error) {
	metrics.GiteaCallsTotal.WithLabelValues("pr_review_post").Inc()
	type inline struct {
		Path        string `json:"path"`
		Body        string `json:"body"`
		NewPosition int    `json:"new_position"`
	}
	req := struct {
		Event    string   `json:"event"`
		Body     string   `json:"body"`
		CommitID string   `json:"commit_id,omitempty"`
		Comments []inline `json:"comments,omitempty"`
	}{Event: "COMMENT", Body: r.Body, CommitID: r.CommitSHA}
	for _, c := range r.Comments {
		req.Comments = append(req.Comments, inline{Path: c.Path, Body: c.Body, NewPosition: c.Line})
	}
	var resp struct {
		ID int64 `json:"id"`
	}
	if _, err := g.apiCall(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/reviews", repoName, number), req, &resp); err != nil {
		msg := err.Error()
		if len(r.Comments) > 0 && (strings.Contains(msg, "status 422") || strings.Contains(msg, "status 400")) {
			return "", fmt.Errorf("gitea post review: %w: %v", forge.ErrInlineAnchor, err)
		}
		return "", fmt.Errorf("gitea post review: %w", err)
	}
	return strconv.FormatInt(resp.ID, 10), nil
}
