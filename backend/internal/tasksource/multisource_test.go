package tasksource

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// namedFakeSource is like fakeSource but with a caller-chosen Name() and an
// AppliesTo predicate, letting tests exercise Importer.resolveSource picking
// the right Source out of several configured ones by remote-URL match,
// mirroring how GitHubIssues/GiteaIssues discriminate in production.
type namedFakeSource struct {
	name    string
	applies func(gen.Repo) bool
	items   []ExternalTask
	err     error
}

func (s namedFakeSource) Name() string              { return s.name }
func (s namedFakeSource) AppliesTo(r gen.Repo) bool { return s.applies(r) }
func (s namedFakeSource) Fetch(context.Context, gen.Repo) ([]ExternalTask, error) {
	return s.items, s.err
}

func hasPrefixRemote(prefix string) func(gen.Repo) bool {
	return func(r gen.Repo) bool {
		return r.RemoteUrl != nil && len(*r.RemoteUrl) >= len(prefix) && (*r.RemoteUrl)[:len(prefix)] == prefix
	}
}

// seedRepoWithRemoteURL is like seedRepo but with a caller-chosen remote URL.
func seedRepoWithRemoteURL(t *testing.T, q *gen.Queries, remoteURL string) gen.Repo {
	t.Helper()
	ctx := context.Background()

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: uuid.NewString(), Name: "wf-" + uuid.NewString()})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, Name: "triage", Color: "#000", SortOrder: 0, AgentIgnore: 1,
	}); err != nil {
		t.Fatalf("create gate label: %v", err)
	}

	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:               uuid.NewString(),
		Name:             "acme/widgets",
		Path:             t.TempDir(),
		RemoteUrl:        &remoteURL,
		WorkflowID:       &wf.ID,
		IssueSyncEnabled: 1,
		IssueSyncLabel:   "",
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return repo
}

// TestNewMulti_RoutesEachRepoToTheMatchingSource verifies an Importer
// configured with several Sources (as main.go wires GitHubIssues + GiteaIssues
// together) creates each repo's tasks under the correct Source's Name(),
// selected purely from the repo's remote URL — the core of the multi-forge
// issue-import behavior added for Gitea support.
func TestNewMulti_RoutesEachRepoToTheMatchingSource(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	githubRepo := seedRepoWithRemoteURL(t, q, "https://github.com/acme/widgets")
	giteaRepo := seedRepoWithRemoteURL(t, q, "https://git.example.com/acme/other")

	githubSrc := namedFakeSource{
		name:    "github",
		applies: hasPrefixRemote("https://github.com/"),
		items:   []ExternalTask{{Ref: "acme/widgets#1", Title: "from github"}},
	}
	giteaSrc := namedFakeSource{
		name:    "gitea",
		applies: hasPrefixRemote("https://git.example.com/"),
		items:   []ExternalTask{{Ref: "acme/other#1", Title: "from gitea"}},
	}

	im := NewMulti(db.SQL(), &recordingPub{}, time.Minute, []Source{githubSrc, giteaSrc})
	im.Sweep(context.Background())

	ghTask, err := q.GetTaskBySource(context.Background(), gen.GetTaskBySourceParams{Source: "github", SourceRef: "acme/widgets#1"})
	if err != nil {
		t.Fatalf("expected a github-sourced task to be created: %v", err)
	}
	if ghTask.RepoID != githubRepo.ID {
		t.Errorf("github task repo_id = %q, want %q", ghTask.RepoID, githubRepo.ID)
	}

	giteaTask, err := q.GetTaskBySource(context.Background(), gen.GetTaskBySourceParams{Source: "gitea", SourceRef: "acme/other#1"})
	if err != nil {
		t.Fatalf("expected a gitea-sourced task to be created: %v", err)
	}
	if giteaTask.RepoID != giteaRepo.ID {
		t.Errorf("gitea task repo_id = %q, want %q", giteaTask.RepoID, giteaRepo.ID)
	}
}

// TestNewMulti_RepoWithNoMatchingSourceIsSkipped verifies a repo whose remote
// doesn't match any configured Source is skipped entirely rather than
// erroring the whole sweep or falling through to the wrong Source.
func TestNewMulti_RepoWithNoMatchingSourceIsSkipped(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	seedRepoWithRemoteURL(t, q, "https://bitbucket.org/acme/widgets")

	githubSrc := namedFakeSource{name: "github", applies: hasPrefixRemote("https://github.com/")}
	im := NewMulti(db.SQL(), &recordingPub{}, time.Minute, []Source{githubSrc})
	im.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks created for an unrecognised remote, got %d", len(tasks))
	}
}
