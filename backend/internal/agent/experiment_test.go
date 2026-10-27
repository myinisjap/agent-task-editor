package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestAssignArmShareDistribution(t *testing.T) {
	for _, share := range []int64{10, 50, 90} {
		n, cand := 10000, 0
		for i := 0; i < n; i++ {
			if assignArm("exp-1", uuid.NewString(), share) == ArmCandidate {
				cand++
			}
		}
		pct := float64(cand) * 100 / float64(n)
		if math.Abs(pct-float64(share)) > 3 {
			t.Errorf("share %d: got %.2f%% candidate", share, pct)
		}
	}
}

func TestAssignArmDeterministicAndExperimentSpecific(t *testing.T) {
	diff := 0
	for i := 0; i < 500; i++ {
		id := uuid.NewString()
		if assignArm("e1", id, 50) != assignArm("e1", id, 50) {
			t.Fatal("not deterministic")
		}
		if assignArm("e1", id, 50) != assignArm("e2", id, 50) {
			diff++
		}
	}
	if diff == 0 {
		t.Fatal("different experiments produced identical partitions")
	}
}

type expFixture struct {
	d    *Dispatcher
	q    *gen.Queries
	sql  *sql.DB
	cfg  gen.AgentConfig
	exp  gen.AgentExperiment
	wf   string
	repo string
}

func newExpFixture(t *testing.T, share int64) *expFixture {
	t.Helper()
	db, err := storage.Open(t.TempDir() + "/exp.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := gen.New(db.SQL())
	ctx := context.Background()
	wf := seedE2EWorkflow(t, q)
	repo := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repo, Name: "r", Path: t.TempDir(), WorkflowID: &wf}); err != nil {
		t.Fatal(err)
	}
	mkPC := func(model string) string {
		pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: uuid.NewString(), Name: model, Provider: "claude", Model: model, Env: `{}`})
		if err != nil {
			t.Fatal(err)
		}
		return pc.ID
	}
	pc1, pc2 := mkPC("a"), mkPC("b")
	cfg, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "agent", ProviderConfigID: pc1, SystemPrompt: "control prompt", Labels: `["ready"]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := SnapshotOf(cfg)
	snap.SystemPrompt, snap.ProviderConfigID = "candidate prompt", pc2
	b, _ := json.Marshal(snap)
	exp, err := q.CreateAgentExperiment(ctx, gen.CreateAgentExperimentParams{
		ID: uuid.NewString(), AgentConfigID: cfg.ID, CandidateSnapshot: string(b), SharePercent: share,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &expFixture{sql: db.SQL(), d: &Dispatcher{q: q}, q: q, cfg: cfg, exp: exp, wf: wf, repo: repo}
}

func (f *expFixture) task(t *testing.T, id string) gen.Task {
	t.Helper()
	task, err := f.q.CreateTask(context.Background(), gen.CreateTaskParams{ID: id, Title: "t", WorkflowID: f.wf, RepoID: f.repo, Label: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestResolveExperimentSticky(t *testing.T) {
	f := newExpFixture(t, 50)
	ctx := context.Background()
	var firstArms = map[string]string{}
	var tasks []gen.Task
	for i := 0; i < 30; i++ {
		task := f.task(t, uuid.NewString())
		tasks = append(tasks, task)
		eff, arm := f.d.resolveExperiment(ctx, task, f.cfg, &f.exp)
		firstArms[task.ID] = arm.Arm
		if arm.ExperimentID != f.exp.ID {
			t.Fatalf("experiment id = %q", arm.ExperimentID)
		}
		if eff.ID != f.cfg.ID || eff.Name != f.cfg.Name {
			t.Fatal("identity changed")
		}
		if arm.Arm == ArmCandidate && (eff.SystemPrompt != "candidate prompt" || eff.ProviderConfigID == f.cfg.ProviderConfigID) {
			t.Fatalf("candidate not overlaid: %+v", eff)
		}
		if arm.Arm == ArmControl && eff.SystemPrompt != "control prompt" {
			t.Fatal("control was overlaid")
		}
	}
	for _, share := range []int64{1, 90} {
		if _, err := f.sql.ExecContext(ctx, `UPDATE agent_experiments SET share_percent = ? WHERE id = ?`, share, f.exp.ID); err != nil {
			t.Fatal(err)
		}
		exp := f.exp
		exp.SharePercent = share
		for _, task := range tasks {
			if _, arm := f.d.resolveExperiment(ctx, task, f.cfg, &exp); arm.Arm != firstArms[task.ID] {
				t.Fatalf("arm changed for %s after share change", task.ID)
			}
		}
	}
}

func TestResolveExperimentPreExistingRunNotEnrolled(t *testing.T) {
	f := newExpFixture(t, 90)
	ctx := context.Background()
	task := f.task(t, uuid.NewString())
	if _, err := f.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: task.ID, AgentConfigID: &f.cfg.ID}); err != nil {
		t.Fatal(err)
	}
	// Make the run predate the experiment.
	if _, err := f.sql.ExecContext(ctx, `UPDATE agent_runs SET created_at = datetime('now','-1 day') WHERE task_id = ?`, task.ID); err != nil {
		t.Fatal(err)
	}
	eff, arm := f.d.resolveExperiment(ctx, task, f.cfg, &f.exp)
	if arm != (experimentArm{}) || eff.SystemPrompt != "control prompt" {
		t.Fatalf("pre-existing task enrolled: %+v", arm)
	}
	if _, err := f.q.GetAgentExperimentAssignment(ctx, gen.GetAgentExperimentAssignmentParams{ExperimentID: f.exp.ID, TaskID: task.ID}); err == nil {
		t.Fatal("assignment row written for pre-existing task")
	}
}

func TestResolveExperimentNoneRunning(t *testing.T) {
	f := newExpFixture(t, 90)
	task := f.task(t, uuid.NewString())
	eff, arm := f.d.resolveExperiment(context.Background(), task, f.cfg, nil)
	if arm != (experimentArm{}) || eff != f.cfg {
		t.Fatal("expected untouched control")
	}
}
