package schedule

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

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
	f, err := os.CreateTemp("", "schedule-test-*.db")
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

// seedWorkflow creates a workflow with a "not_ready" label and a terminal
// "done" label, mirroring how real workflows are configured.
func seedWorkflow(t *testing.T, q *gen.Queries) gen.Workflow {
	t.Helper()
	ctx := context.Background()
	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{
		ID:          uuid.NewString(),
		Name:        "wf-" + uuid.NewString(),
		Description: "",
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	_, err = q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID:         uuid.NewString(),
		WorkflowID: wf.ID,
		Name:       "not_ready",
		Color:      "#888",
		SortOrder:  0,
	})
	if err != nil {
		t.Fatalf("create label not_ready: %v", err)
	}
	_, err = q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID:         uuid.NewString(),
		WorkflowID: wf.ID,
		Name:       "done",
		Color:      "#0f0",
		SortOrder:  1,
		IsTerminal: 1,
	})
	if err != nil {
		t.Fatalf("create label done: %v", err)
	}
	return wf
}

func seedRepo(t *testing.T, q *gen.Queries, workflowID *string) gen.Repo {
	t.Helper()
	remote := "https://github.com/acme/widgets"
	repo, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:         uuid.NewString(),
		Name:       "acme/widgets",
		Path:       t.TempDir(),
		RemoteUrl:  &remote,
		WorkflowID: workflowID,
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return repo
}

func seedTemplate(t *testing.T, q *gen.Queries) gen.TaskTemplate {
	t.Helper()
	tmpl, err := q.CreateTaskTemplate(context.Background(), gen.CreateTaskTemplateParams{
		ID:          uuid.NewString(),
		Name:        "tmpl-" + uuid.NewString(),
		Title:       "Upgrade dependencies",
		Description: "Run the upgrade script and fix breakage.",
		Type:        "chore",
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return tmpl
}

func seedSchedule(t *testing.T, q *gen.Queries, tmplID, repoID, cronExpr, targetLabel string, enabled bool) gen.TaskSchedule {
	t.Helper()
	sched, err := q.CreateTaskSchedule(context.Background(), gen.CreateTaskScheduleParams{
		ID:          uuid.NewString(),
		TemplateID:  tmplID,
		RepoID:      repoID,
		CronExpr:    cronExpr,
		TargetLabel: targetLabel,
		Enabled:     enabled,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	return sched
}

func TestSweepFiresDueSchedule(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wf := seedWorkflow(t, q)
	repo := seedRepo(t, q, &wf.ID)
	tmpl := seedTemplate(t, q)
	// "every minute" cron always due relative to created_at.
	seedSchedule(t, q, tmpl.ID, repo.ID, "* * * * *", "not_ready", true)

	pub := &recordingPub{}
	s := New(db.SQL(), pub, time.Minute)
	s.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task after sweep, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Source != "schedule" {
		t.Errorf("source = %q, want schedule", task.Source)
	}
	if task.Title != tmpl.Title {
		t.Errorf("title = %q, want %q", task.Title, tmpl.Title)
	}
	if task.Label != "not_ready" {
		t.Errorf("label = %q, want not_ready", task.Label)
	}
	if len(pub.events) != 1 || pub.events[0] != "task.created" {
		t.Errorf("events = %v, want [task.created]", pub.events)
	}
}

func TestSweepSkipsWhenNotDue(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wf := seedWorkflow(t, q)
	repo := seedRepo(t, q, &wf.ID)
	tmpl := seedTemplate(t, q)
	// Yearly on Jan 1 — essentially never due right after creation (unless
	// today happens to be Jan 1 at minute 0, astronomically unlikely for a
	// deterministic CI run given the fixed clock in this suite).
	seedSchedule(t, q, tmpl.ID, repo.ID, "0 0 1 1 *", "not_ready", true)

	s := New(db.SQL(), &recordingPub{}, time.Minute)
	s.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestSweepSkipsWhenOpenTaskExists(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wf := seedWorkflow(t, q)
	repo := seedRepo(t, q, &wf.ID)
	tmpl := seedTemplate(t, q)
	sched := seedSchedule(t, q, tmpl.ID, repo.ID, "* * * * *", "not_ready", true)

	s := New(db.SQL(), &recordingPub{}, time.Minute)
	ctx := context.Background()
	s.Sweep(ctx)

	tasks, err := q.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task after first sweep, got %d", len(tasks))
	}

	// Second sweep must not create another task while the first is still open.
	s.Sweep(ctx)
	tasks, err = q.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected still 1 task after second sweep, got %d", len(tasks))
	}

	// Sanity: the open task really is tagged with this schedule.
	wantPrefix := sched.ID + "#"
	if !strings.HasPrefix(tasks[0].SourceRef, wantPrefix) {
		t.Errorf("source_ref = %q, want prefix %q", tasks[0].SourceRef, wantPrefix)
	}
}

func TestSweepFiresAgainOnceTaskClosed(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wf := seedWorkflow(t, q)
	repo := seedRepo(t, q, &wf.ID)
	tmpl := seedTemplate(t, q)
	seedSchedule(t, q, tmpl.ID, repo.ID, "* * * * *", "not_ready", true)

	s := New(db.SQL(), &recordingPub{}, time.Minute)
	ctx := context.Background()
	s.Sweep(ctx)

	tasks, err := q.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	// Move the prior task to the terminal "done" label.
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{
		Label: "done",
		ID:    tasks[0].ID,
	}); err != nil {
		t.Fatalf("update label: %v", err)
	}

	// Force the schedule to be due again by resetting last_run_at into the past.
	scheds, err := q.ListEnabledTaskSchedules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Minute)
	if err := q.SetTaskScheduleLastRun(ctx, gen.SetTaskScheduleLastRunParams{
		LastRunAt: &past,
		ID:        scheds[0].ID,
	}); err != nil {
		t.Fatalf("reset last_run_at: %v", err)
	}

	s.Sweep(ctx)
	tasks, err = q.ListTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks after prior one closed, got %d", len(tasks))
	}
}

func TestSweepIgnoresDisabledSchedules(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wf := seedWorkflow(t, q)
	repo := seedRepo(t, q, &wf.ID)
	tmpl := seedTemplate(t, q)
	seedSchedule(t, q, tmpl.ID, repo.ID, "* * * * *", "not_ready", false)

	s := New(db.SQL(), &recordingPub{}, time.Minute)
	s.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks for disabled schedule, got %d", len(tasks))
	}
}

func TestSweepSkipsRepoWithoutWorkflow(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repo := seedRepo(t, q, nil)
	tmpl := seedTemplate(t, q)
	seedSchedule(t, q, tmpl.ID, repo.ID, "* * * * *", "not_ready", true)

	s := New(db.SQL(), &recordingPub{}, time.Minute)
	s.Sweep(context.Background())

	tasks, err := q.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks for repo without workflow, got %d", len(tasks))
	}
}
