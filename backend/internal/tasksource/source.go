// Package tasksource imports tasks from external trackers (v1: GitHub
// Issues). A Source abstracts where candidate tasks come from; the Importer
// polls all opted-in repos and creates board tasks for items that haven't
// been imported yet, keyed by (source, source_ref).
package tasksource

import (
	"context"
	"fmt"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
)

// ExternalTask is a candidate task fetched from an external source.
type ExternalTask struct {
	Ref    string // unique within the source, e.g. "owner/repo#123"
	Title  string
	Body   string
	URL    string   // web link back to the external item
	Labels []string // labels on the external item (used for the task type heuristic)
}

// Source fetches candidate tasks for a repo from one external tracker.
type Source interface {
	// Name is the value stored in tasks.source for items from this source.
	Name() string
	// Fetch returns all currently-matching candidate tasks for the repo.
	// It must apply the repo's own filter settings (e.g. issue_sync_label).
	Fetch(ctx context.Context, repo gen.Repo) ([]ExternalTask, error)
}

// ExternalComment is one human comment on an external item.
type ExternalComment struct {
	ID        string
	Author    string
	Body      string
	CreatedAt string // RFC3339, as reported by the source
	// TrustedAuthor is true when the source vouches for the author having
	// write access to the repo. Only trusted comments are ingested.
	TrustedAuthor bool
}

// CommentSource is an optional Source capability: reading the human comment
// thread on an external item. A Source that doesn't implement it simply has
// no comments ingested.
type CommentSource interface {
	FetchComments(ctx context.Context, repo gen.Repo, ref string) ([]ExternalComment, error)
}

// GitHubIssues imports open GitHub issues via the `gh` CLI, honouring the
// repo's issue_sync_label filter (empty = all open issues).
type GitHubIssues struct{}

func (GitHubIssues) Name() string { return "github" }

func (GitHubIssues) Fetch(ctx context.Context, repo gen.Repo) ([]ExternalTask, error) {
	if repo.RemoteUrl == nil || *repo.RemoteUrl == "" {
		return nil, fmt.Errorf("repo %s has no remote URL", repo.Name)
	}
	ghName, ok := ghclient.ParseGitHubName(*repo.RemoteUrl)
	if !ok {
		return nil, fmt.Errorf("repo %s remote is not a GitHub URL", repo.Name)
	}

	issues, err := ghclient.ListOpenIssues(ctx, ghName, repo.IssueSyncLabel)
	if err != nil {
		return nil, fmt.Errorf("list issues for %s: %w", ghName, err)
	}

	tasks := make([]ExternalTask, 0, len(issues))
	for _, is := range issues {
		tasks = append(tasks, ExternalTask{
			Ref:    fmt.Sprintf("%s#%d", ghName, is.Number),
			Title:  is.Title,
			Body:   is.Body,
			URL:    is.URL,
			Labels: is.Labels,
		})
	}
	return tasks, nil
}

// FetchComments implements CommentSource for GitHubIssues: it reads the
// issue's comment thread via the gh CLI. TrustedAuthor is set from
// author_association (OWNER/MEMBER/COLLABORATOR only — CONTRIBUTOR/NONE/etc
// are untrusted). Comments containing this system's own write-back marker
// (see writeback.MarkerComment) are dropped entirely, since those are this
// system reading its own PR-opened/closed notices back as if they were human
// input.
func (GitHubIssues) FetchComments(ctx context.Context, repo gen.Repo, ref string) ([]ExternalComment, error) {
	ghName, issueNumber, ok := writeback.ParseSourceRef(ref)
	if !ok {
		return nil, fmt.Errorf("source ref %q is not a github issue ref", ref)
	}

	comments, err := ghclient.GetIssueComments(ctx, ghName, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("get issue comments for %s: %w", ref, err)
	}
	return mapIssueComments(comments), nil
}

// mapIssueComments converts ghclient.IssueComment values into
// ExternalComment, applying the write-access-only trust classification and
// dropping this system's own write-back marker comments. Split out from
// FetchComments so the classification logic is unit-testable without
// shelling out to `gh`.
func mapIssueComments(comments []ghclient.IssueComment) []ExternalComment {
	out := make([]ExternalComment, 0, len(comments))
	for _, c := range comments {
		if strings.Contains(c.Body, writeback.MarkerComment) {
			continue
		}
		trusted := false
		switch c.AuthorAssociation {
		case "OWNER", "MEMBER", "COLLABORATOR":
			trusted = true
		}
		out = append(out, ExternalComment{
			ID:            c.ID,
			Author:        c.Author,
			Body:          c.Body,
			CreatedAt:     c.CreatedAt,
			TrustedAuthor: trusted,
		})
	}
	return out
}
