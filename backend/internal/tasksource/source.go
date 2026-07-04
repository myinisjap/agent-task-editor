// Package tasksource imports tasks from external trackers (v1: GitHub
// Issues). A Source abstracts where candidate tasks come from; the Importer
// polls all opted-in repos and creates board tasks for items that haven't
// been imported yet, keyed by (source, source_ref).
package tasksource

import (
	"context"
	"fmt"

	"github.com/myinisjap/agent-task-editor/backend/internal/ghclient"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ExternalTask is a candidate task fetched from an external source.
type ExternalTask struct {
	Ref    string   // unique within the source, e.g. "owner/repo#123"
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
