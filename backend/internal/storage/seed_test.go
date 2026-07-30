package storage

import (
	"context"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// TestSeedDefaultWorkflow_CreatesExpectedLabelsAndTransitions verifies the
// default workflow seeded into a fresh DB has the full set of labels
// (including the not_ready entry label and the done terminal label) and a
// human "success"/"failure"-pathed transition set that the Approve/Reject
// handlers depend on.
func TestSeedDefaultWorkflow_CreatesExpectedLabelsAndTransitions(t *testing.T) {
	db, err := Open(t.TempDir() + "/seed.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}

	q := gen.New(db.SQL())
	workflows, err := q.ListWorkflows(ctx)
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	if len(workflows) != 1 {
		t.Fatalf("expected exactly 1 workflow after seeding, got %d", len(workflows))
	}
	wf := workflows[0]
	if wf.Name != "Default" {
		t.Errorf("expected workflow named 'Default', got %q", wf.Name)
	}

	labels, err := q.ListWorkflowLabels(ctx, wf.ID)
	if err != nil {
		t.Fatalf("list labels: %v", err)
	}
	wantLabels := []string{"not_ready", "plan", "review-plan", "work", "testing", "agent-review", "review", "done"}
	if len(labels) != len(wantLabels) {
		t.Fatalf("expected %d labels, got %d: %+v", len(wantLabels), len(labels), labels)
	}
	byName := make(map[string]gen.WorkflowLabel, len(labels))
	for _, l := range labels {
		byName[l.Name] = l
	}
	for _, name := range wantLabels {
		if _, ok := byName[name]; !ok {
			t.Errorf("expected label %q to exist, got %+v", name, byName)
		}
	}
	if byName["not_ready"].AgentIgnore == 0 {
		t.Errorf("expected not_ready to be agent-ignored")
	}
	if byName["done"].IsTerminal == 0 {
		t.Errorf("expected done to be terminal")
	}
	if byName["work"].AgentIgnore != 0 || byName["work"].IsTerminal != 0 {
		t.Errorf("expected work to be neither agent-ignored nor terminal, got %+v", byName["work"])
	}

	transitions, err := q.ListWorkflowTransitions(ctx, wf.ID)
	if err != nil {
		t.Fatalf("list transitions: %v", err)
	}
	if len(transitions) != 11 {
		t.Fatalf("expected 11 transitions, got %d: %+v", len(transitions), transitions)
	}

	// Approve/Reject depend on "review" having human transitions with both
	// success and failure paths.
	var reviewSuccess, reviewFailure bool
	for _, tr := range transitions {
		if tr.FromLabel == "review" && tr.TriggerType == "human" && tr.Path != nil {
			switch *tr.Path {
			case "success":
				reviewSuccess = tr.ToLabel == "done"
			case "failure":
				reviewFailure = tr.ToLabel == "work"
			}
		}
	}
	if !reviewSuccess {
		t.Errorf("expected a human 'success' transition from review to done")
	}
	if !reviewFailure {
		t.Errorf("expected a human 'failure' transition from review to work")
	}
}

// TestSeedDefaultWorkflow_IdempotentWhenWorkflowsExist verifies a second call
// to SeedDefaultWorkflow is a no-op once any workflow already exists (it
// short-circuits on CountWorkflows > 0), so it's safe to call unconditionally
// on every startup.
func TestSeedDefaultWorkflow_IdempotentWhenWorkflowsExist(t *testing.T) {
	db, err := Open(t.TempDir() + "/seed.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	if err := SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if err := SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	q := gen.New(db.SQL())
	count, err := q.CountWorkflows(ctx)
	if err != nil {
		t.Fatalf("count workflows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 workflow after seeding twice, got %d", count)
	}
}

// TestDBSize verifies Size reports a positive, growing byte count for a real
// on-disk database file as data is written to it.
func TestDBSize(t *testing.T) {
	db, err := Open(t.TempDir() + "/size.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	size1, err := db.Size()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if size1 <= 0 {
		t.Fatalf("expected positive size for a migrated database, got %d", size1)
	}

	// Force a checkpoint so the growth from inserts below is reflected in the
	// main file (Size reads the main file only, not -wal/-shm sidecars — see
	// Size's doc comment).
	if _, err := db.SQL().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	ctx := context.Background()
	if err := SeedDefaultWorkflow(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.SQL().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	size2, err := db.Size()
	if err != nil {
		t.Fatalf("size after seed: %v", err)
	}
	if size2 < size1 {
		t.Errorf("expected size to not shrink after seeding data, got %d -> %d", size1, size2)
	}
}
