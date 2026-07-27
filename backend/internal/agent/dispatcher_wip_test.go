package agent

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// newWIPTestDB opens a fresh temp SQLite DB (no seed data — tests build their
// own minimal workflow/labels/transitions/tasks) and returns generated queries
// bound to it.
func newWIPTestDB(t *testing.T) *gen.Queries {
	t.Helper()
	f, err := os.CreateTemp("", "dispatcher-wip-*.db")
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
	return gen.New(db.SQL())
}

// wipFixture is a small workflow with two labels ("work" -> "review") and a
// single unambiguous agent success transition between them, plus a repo to
// hang tasks off of.
type wipFixture struct {
	q          *gen.Queries
	workflowID string
	repoID     string
}

func newWIPFixture(t *testing.T, reviewLimit *int64, reviewHard bool) *wipFixture {
	t.Helper()
	ctx := context.Background()
	q := newWIPTestDB(t)

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: uuid.NewString(), Name: "wip-test-" + uuid.NewString()})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, Name: "work", Color: "#000", SortOrder: 0,
	}); err != nil {
		t.Fatalf("create label work: %v", err)
	}
	hard := int64(0)
	if reviewHard {
		hard = 1
	}
	if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, Name: "review", Color: "#000", SortOrder: 1,
		WipLimit: reviewLimit, WipLimitHard: hard,
	}); err != nil {
		t.Fatalf("create label review: %v", err)
	}

	if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
		ID: uuid.NewString(), WorkflowID: wf.ID, FromLabel: "work", ToLabel: "review", TriggerType: "agent",
	}); err != nil {
		t.Fatalf("create transition: %v", err)
	}

	repo, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: uuid.NewString(), Name: "repo", Path: "/tmp/repo-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}

	return &wipFixture{q: q, workflowID: wf.ID, repoID: repo.ID}
}

func (f *wipFixture) newTask(t *testing.T, label string) gen.Task {
	t.Helper()
	task, err := f.q.CreateTask(context.Background(), gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "t", Type: "task", Label: label,
		RepoID: f.repoID, WorkflowID: f.workflowID, Attachments: "[]",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

func int64p(v int64) *int64 { return &v }

func TestCheckWIPLimit(t *testing.T) {
	t.Run("no limit set: never blocks", func(t *testing.T) {
		f := newWIPFixture(t, nil, false)
		f.newTask(t, "review")
		f.newTask(t, "review")
		task := f.newTask(t, "work")

		d := &Dispatcher{q: f.q}
		blocked, err := d.checkWIPLimit(context.Background(), task)
		if err != nil {
			t.Fatalf("checkWIPLimit error: %v", err)
		}
		if blocked {
			t.Fatal("expected dispatch not blocked when no wip_limit is set")
		}
	})

	t.Run("soft limit at capacity: does not block", func(t *testing.T) {
		f := newWIPFixture(t, int64p(1), false) // hard=false
		f.newTask(t, "review")
		task := f.newTask(t, "work")

		d := &Dispatcher{q: f.q}
		blocked, err := d.checkWIPLimit(context.Background(), task)
		if err != nil {
			t.Fatalf("checkWIPLimit error: %v", err)
		}
		if blocked {
			t.Fatal("soft (non-hard) limit must never block dispatch")
		}
	})

	t.Run("hard limit under capacity: does not block", func(t *testing.T) {
		f := newWIPFixture(t, int64p(2), true)
		f.newTask(t, "review")
		task := f.newTask(t, "work")

		d := &Dispatcher{q: f.q}
		blocked, err := d.checkWIPLimit(context.Background(), task)
		if err != nil {
			t.Fatalf("checkWIPLimit error: %v", err)
		}
		if blocked {
			t.Fatal("expected dispatch not blocked when target label is under its hard limit")
		}
	})

	t.Run("hard limit at capacity: blocks dispatch", func(t *testing.T) {
		f := newWIPFixture(t, int64p(1), true)
		f.newTask(t, "review")
		task := f.newTask(t, "work")

		d := &Dispatcher{q: f.q}
		blocked, err := d.checkWIPLimit(context.Background(), task)
		if err != nil {
			t.Fatalf("checkWIPLimit error: %v", err)
		}
		if !blocked {
			t.Fatal("expected dispatch blocked when target label is at its hard limit")
		}
	})

	t.Run("hard limit over capacity: blocks dispatch", func(t *testing.T) {
		f := newWIPFixture(t, int64p(1), true)
		f.newTask(t, "review")
		f.newTask(t, "review")
		task := f.newTask(t, "work")

		d := &Dispatcher{q: f.q}
		blocked, err := d.checkWIPLimit(context.Background(), task)
		if err != nil {
			t.Fatalf("checkWIPLimit error: %v", err)
		}
		if !blocked {
			t.Fatal("expected dispatch blocked when target label is over its hard limit")
		}
	})

	t.Run("archived tasks on target label do not count toward occupancy", func(t *testing.T) {
		f := newWIPFixture(t, int64p(1), true)
		archived := f.newTask(t, "review")
		task := f.newTask(t, "work")

		if _, err := f.q.SetTaskArchived(context.Background(), gen.SetTaskArchivedParams{
			ID: archived.ID, Archived: 1,
		}); err != nil {
			t.Fatalf("archive task: %v", err)
		}

		d := &Dispatcher{q: f.q}
		blocked, err := d.checkWIPLimit(context.Background(), task)
		if err != nil {
			t.Fatalf("checkWIPLimit error: %v", err)
		}
		if blocked {
			t.Fatal("archived tasks must not count toward wip_limit occupancy")
		}
	})
}
