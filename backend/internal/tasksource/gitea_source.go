package tasksource

import (
	"context"
	"fmt"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	// blank-imported so its init() registers the configured Gitea forge.Forge
	// implementation (see internal/forge/gitea's package doc) whenever this
	// package is linked in, mirroring ghclient's registration for GitHub.
	_ "github.com/myinisjap/agent-task-editor/backend/internal/forge/gitea"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/writeback"
)

// GiteaIssues imports open issues from a self-hosted Gitea instance via its
// REST API (through the Gitea forge.Forge implementation in
// internal/forge/gitea), honouring the repo's issue_sync_label filter (empty
// = all open issues). Unlike GitHubIssues (a single well-known host), which
// Gitea instance(s) this recognises is driven entirely by forge.ForRemote —
// see internal/forge/gitea's package doc for the GITEA_HOST/GITEA_TOKEN/
// GITEA_BASE_URL environment variables that configure it. With no Gitea
// forge registered (GITEA_HOST unset), AppliesTo/Fetch never match any repo,
// so this Source is inert rather than erroring — safe to always include in
// the configured source list.
type GiteaIssues struct{}

func (GiteaIssues) Name() string { return "gitea" }

// AppliesTo reports whether repo's remote URL is recognised by the
// registered Gitea forge.Forge (if any), letting Importer.resolveSource pick
// this Source out of several configured ones without making any network
// call.
func (GiteaIssues) AppliesTo(repo gen.Repo) bool {
	if repo.RemoteUrl == nil {
		return false
	}
	_, _, ok := giteaForge(*repo.RemoteUrl)
	return ok
}

func (GiteaIssues) Fetch(ctx context.Context, repo gen.Repo) ([]ExternalTask, error) {
	if repo.RemoteUrl == nil || *repo.RemoteUrl == "" {
		return nil, fmt.Errorf("repo %s has no remote URL", repo.Name)
	}
	f, repoName, ok := giteaForge(*repo.RemoteUrl)
	if !ok {
		return nil, fmt.Errorf("repo %s remote is not a recognised Gitea URL", repo.Name)
	}

	issues, err := f.ListOpenIssues(ctx, repoName, repo.IssueSyncLabel)
	if err != nil {
		return nil, fmt.Errorf("list issues for %s: %w", repoName, err)
	}

	tasks := make([]ExternalTask, 0, len(issues))
	for _, is := range issues {
		tasks = append(tasks, ExternalTask{
			Ref:    fmt.Sprintf("%s#%d", repoName, is.Number),
			Title:  is.Title,
			Body:   is.Body,
			URL:    is.URL,
			Labels: is.Labels,
		})
	}
	return tasks, nil
}

// FetchComments implements CommentSource for GiteaIssues: it reads the
// issue's comment thread via the Gitea API. TrustedAuthor is derived the same
// way GitHubIssues does (mapIssueComments), from AuthorAssociation as
// reported by the Gitea forge.Forge implementation (which classifies via a
// collaborator-status check — see internal/forge/gitea). Comments containing
// this system's own write-back marker are dropped, same as GitHubIssues.
//
// Unlike GitHubIssues.FetchComments (which can derive the GitHub forge from
// the "owner/repo" shape of ref alone, since GitHub is a single well-known
// host), resolving which Gitea instance a ref belongs to needs repo's actual
// RemoteUrl — a self-hosted host isn't inferable from "owner/repo#N" alone.
func (GiteaIssues) FetchComments(ctx context.Context, repo gen.Repo, ref string) ([]ExternalComment, error) {
	if repo.RemoteUrl == nil || *repo.RemoteUrl == "" {
		return nil, fmt.Errorf("repo %s has no remote URL", repo.Name)
	}
	f, repoName, ok := giteaForge(*repo.RemoteUrl)
	if !ok {
		return nil, fmt.Errorf("repo %s remote is not a recognised Gitea URL", repo.Name)
	}

	wantRepoName, issueNumber, ok := writeback.ParseSourceRef(ref)
	if !ok || wantRepoName != repoName {
		return nil, fmt.Errorf("source ref %q is not a valid issue ref for repo %s", ref, repoName)
	}

	comments, err := f.GetIssueComments(ctx, repoName, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("get issue comments for %s: %w", ref, err)
	}
	return mapIssueComments(comments), nil
}

// giteaForge resolves the registered Gitea forge.Forge (if any) that
// recognises remoteURL, returning it alongside the parsed repo name. Thin
// wrapper around forge.ForRemote scoped to the Gitea implementation, so a
// non-Gitea registered forge (e.g. GitHub) can never be selected here even if
// it somehow also matched remoteURL (it won't in practice, since
// ParseRepoName is host-keyed, but this keeps the guarantee explicit rather
// than relying on registration order).
func giteaForge(remoteURL string) (forge.Forge, string, bool) {
	f, name, ok := forge.ForRemote(remoteURL)
	if !ok {
		return nil, "", false
	}
	if !strings.Contains(strings.ToLower(fmt.Sprintf("%T", f)), "gitea") {
		return nil, "", false
	}
	return f, name, true
}
