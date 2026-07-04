package tasksource

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// fakeSource returns a fixed set of external tasks (or an error).
type fakeSource struct {
	items []ExternalTask
	err   error
}

func (fakeSource) Name() string { return "github" }

func (f fakeSource) Fetch(context.Context, gen.Repo) ([]ExternalTask, error) {
	return f.items, f.err
}

// recordingPub records published events.
type recordingPub struct {
	mu     sync.Mutex
	events []string
}

func (p *recordingPub) Publish(eventType string, _ map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, eventType)
}

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "tasksource-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })

	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// seedRepo creates a workflow and an issue-sync-enabled repo pointing at it.
func seedRepo(t *testing.T, q *gen.Queries, syncEnabled int64, withWorkflow bool) gen.Repo {
	t.Helper()
	ctx := context.Background()

	var workflowID *string
	if withWorkflow {
		wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
			ID:          uuid.NewString(),
			Name:        "wf-" + uuid.NewString(),
			Description: "",
		})
		if err != nil {
			t.Fatalf("create workflow: %v", err)
		}
		workflowID = &wf.ID
	}

	remote := "https://github.com/acme/widgets"
	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:               uuid.NewString(),
		Name:             "acme/widgets",
		Path:             t.TempDir(),
		RemoteUrl:        &remote,
		WorkflowID:       workflowID,
		IssueSyncEnabled: syncEnabled,
		IssueSyncLabel:   "agent-ok",
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return repo
}

func TestSweepImportsAndDedupes(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepo(t, q, 1, true)

	src := fakeSource{items: []ExternalTask{
		{Ref: "acme/widgets#1", Title: "Fix crash", Body: "It crashes.", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug", "agent-ok"}},
		{Ref: "acme/widgets#2", Title: "Add dark mode", Body: "", URL: "https://github.com/acme/widgets/issues/2", Labels: []string{"enhancement"}},
	}}
	pub := &recordingPub{}
	im := New(db.SQL(), pub, time.Minute, src)

	ctx := context.Background()
	im.Sweep(ctx)

	tasks, err := q.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks after first sweep, got %d", len(tasks))
	}
	if len(pub.events) != 2 {
		t.Fatalf("expected 2 task.created events, got %d", len(pub.events))
	}

	// Second sweep must not duplicate.
	im.Sweep(ctx)
	tasks, err = q.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks after second sweep, got %d", len(tasks))
	}

	// Inspect the imported bug task.
	var bug gen.Task
	for _, task := range tasks {
		if task.SourceRef == "acme/widgets#1" {
			bug = task
		}
	}
	if bug.ID == "" {
		t.Fatal("imported task for issue #1 not found")
	}
	if bug.Source != "github" {
		t.Errorf("source = %q, want github", bug.Source)
	}
	if bug.Type != "bug" {
		t.Errorf("type = %q, want bug", bug.Type)
	}
	if bug.Label != "not_ready" {
		t.Errorf("label = %q, want not_ready", bug.Label)
	}
	if bug.Title != "Fix crash" {
		t.Errorf("title = %q", bug.Title)
	}
	if want := "It crashes.\n\n_Imported from https://github.com/acme/widgets/issues/1_"; bug.Description != want {
		t.Errorf("description = %q, want %q", bug.Description, want)
	}
}

func TestSweepSkipsRepoWithoutWorkflow(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepo(t, q, 1, false)

	src := fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "x"}}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	im.Sweep(context.Background())

	tasks, err := gen.New(db.SQL()).ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks for repo without workflow, got %d", len(tasks))
	}
}

func TestSweepIgnoresDisabledRepos(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepo(t, q, 0, true)

	src := fakeSource{items: []ExternalTask{{Ref: "acme/widgets#1", Title: "x"}}}
	im := New(db.SQL(), &recordingPub{}, time.Minute, src)
	im.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks for disabled repo, got %d", len(tasks))
	}
}

func TestSweepSurvivesFetchError(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	seedRepo(t, q, 1, true)

	im := New(db.SQL(), &recordingPub{}, time.Minute, fakeSource{err: errors.New("gh exploded")})
	im.Sweep(context.Background()) // must not panic

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks on fetch error, got %d", len(tasks))
	}
}

func TestTaskTypeFromLabels(t *testing.T) {
	cases := []struct {
		labels []string
		want   string
	}{
		{[]string{"Bug", "agent-ok"}, "bug"},
		{[]string{"enhancement"}, "feature"},
		{[]string{"dependencies"}, "chore"},
		{[]string{"question"}, "spike"},
		{[]string{"agent-ok"}, "feature"},
		{nil, "feature"},
	}
	for _, c := range cases {
		if got := TaskTypeFromLabels(c.labels); got != c.want {
			t.Errorf("TaskTypeFromLabels(%v) = %q, want %q", c.labels, got, c.want)
		}
	}
}
