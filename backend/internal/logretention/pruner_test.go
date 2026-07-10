package logretention_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/logretention"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "logretention-test-*.db")
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

	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// seedRun creates a repo/task/agent-config/agent-run and returns the run ID,
// then seeds a single agent_log row for it. status/completedAt are applied
// directly via raw SQL after creation so tests can control the exact
// completed_at timestamp (sqlc's SetAgentRunCompleted always stamps
// CURRENT_TIMESTAMP, which isn't controllable enough for aging tests).
func seedRun(t *testing.T, db *storage.DB, q *gen.Queries, wfID, status string, completedAt *time.Time) string {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
		ID:         repoID,
		Name:       "repo-" + repoID,
		Path:       t.TempDir(),
		WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	taskID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID:         taskID,
		Title:      "task",
		WorkflowID: wfID,
		RepoID:     repoID,
		Label:      "plan",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	agCfgID := uuid.NewString()
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID:       agCfgID,
		Name:     "agent-" + agCfgID,
		Provider: "mock",
		Model:    "none",
		Labels:   `["plan"]`,
		Env:      `{}`,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	runID := uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{
		ID:            runID,
		TaskID:        taskID,
		AgentConfigID: &agCfgID,
	}); err != nil {
		t.Fatalf("create agent run: %v", err)
	}

	if _, err := db.SQL().ExecContext(ctx,
		`UPDATE agent_runs SET status = ?, completed_at = ? WHERE id = ?`,
		status, completedAt, runID); err != nil {
		t.Fatalf("set run status/completed_at: %v", err)
	}

	if err := q.CreateAgentLog(ctx, gen.CreateAgentLogParams{
		ID:         uuid.NewString(),
		AgentRunID: runID,
		Timestamp:  time.Now().UTC(),
		Type:       "stdout",
		Content:    "hello",
	}); err != nil {
		t.Fatalf("create agent log: %v", err)
	}

	return runID
}

func logCount(t *testing.T, q *gen.Queries, runID string) int64 {
	t.Helper()
	n, err := q.CountAgentLogs(context.Background(), runID)
	if err != nil {
		t.Fatalf("count agent logs: %v", err)
	}
	return n
}

func TestRunOnce_PrunesOldTerminalRunLogs(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatalf("expected seeded workflow: %v", err)
	}
	wfID := wfs[0].ID

	old := time.Now().UTC().AddDate(0, 0, -60)
	recent := time.Now().UTC().AddDate(0, 0, -1)

	oldTerminalRun := seedRun(t, db, q, wfID, "completed", &old)
	recentTerminalRun := seedRun(t, db, q, wfID, "completed", &recent)
	runningRun := seedRun(t, db, q, wfID, "running", nil)

	p := logretention.New(q, 30, time.Hour)
	if err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if n := logCount(t, q, oldTerminalRun); n != 0 {
		t.Errorf("expected old terminal run's logs to be pruned, got %d remaining", n)
	}
	if n := logCount(t, q, recentTerminalRun); n != 1 {
		t.Errorf("expected recent terminal run's logs to survive, got %d", n)
	}
	if n := logCount(t, q, runningRun); n != 1 {
		t.Errorf("expected running run's logs to survive (never pruned), got %d", n)
	}
}

func TestRunOnce_DaysZeroOrNegative_DisablesPruning(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())

	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatalf("expected seeded workflow: %v", err)
	}
	wfID := wfs[0].ID

	old := time.Now().UTC().AddDate(0, 0, -400)
	oldTerminalRun := seedRun(t, db, q, wfID, "completed", &old)

	p := logretention.New(q, 0, time.Hour)
	if err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := logCount(t, q, oldTerminalRun); n != 1 {
		t.Errorf("expected no pruning with days<=0, got %d remaining", n)
	}

	p = logretention.New(q, -5, time.Hour)
	if err := p.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n := logCount(t, q, oldTerminalRun); n != 1 {
		t.Errorf("expected no pruning with negative days, got %d remaining", n)
	}
}
