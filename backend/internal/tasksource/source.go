// Package tasksource defines the interface for external task sources (GitHub Issues, Jira, etc.)
// and provides a stub GitHub Issues adapter.
package tasksource

import "context"

// ExternalTask is a task imported from an external source.
type ExternalTask struct {
	ExternalID  string
	Title       string
	Description string
	Type        string // feature|bug|chore|spike — mapped from source labels
	Labels      []string
}

// Source is the interface all external task sources must satisfy.
// Implementations are expected to be pull-based (called on a schedule).
type Source interface {
	// Name returns a human-readable name for logging.
	Name() string
	// Fetch returns new/updated tasks since the last sync.
	// The caller is responsible for deduplication by ExternalID.
	Fetch(ctx context.Context) ([]ExternalTask, error)
}

// GitHubIssuesSource is a stub adapter for GitHub Issues.
// Full implementation would use the GitHub REST or GraphQL API.
type GitHubIssuesSource struct {
	Owner  string
	Repo   string
	Token  string
	Labels []string // only import issues with these labels
}

func (s *GitHubIssuesSource) Name() string {
	return "github:" + s.Owner + "/" + s.Repo
}

// Fetch is a stub — returns empty until the GitHub API client is implemented.
func (s *GitHubIssuesSource) Fetch(_ context.Context) ([]ExternalTask, error) {
	// TODO: implement with net/http GitHub REST API
	// GET /repos/{owner}/{repo}/issues?labels=...&state=open
	return nil, nil
}
