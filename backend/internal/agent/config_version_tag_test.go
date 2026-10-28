package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// A run dispatched for a config that has version history is tagged with the
// latest version; a config without history leaves the column NULL.
func TestE2E_RunTaggedWithConfigVersion(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}
	pcID := h.createProviderConfig(t, "claude", "none")
	cfg, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake-agent", ProviderConfigID: pcID,
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1, ResumeSessions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Two versions exist before the task appears; the run must carry v2.
	for i := 0; i < 2; i++ {
		if _, err := h.q.CreateAgentConfigVersion(ctx, gen.CreateAgentConfigVersionParams{
			ID: uuid.NewString(), AgentConfigID: cfg.ID, Snapshot: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	taskID := uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "ready",
	}); err != nil {
		t.Fatal(err)
	}
	run := h.firstRun(t, taskID)
	if run.AgentConfigVersion == nil || *run.AgentConfigVersion != 2 {
		t.Fatalf("agent_config_version = %v, want 2", run.AgentConfigVersion)
	}
}

func TestE2E_RunWithoutVersionHistoryUntagged(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)
	run := h.firstRun(t, taskID)
	if run.AgentConfigVersion != nil {
		t.Fatalf("agent_config_version = %v, want NULL", *run.AgentConfigVersion)
	}
}

// Candidate-arm runs carry the candidate's config, no version, and the
// experiment tag; control-arm runs keep the latest version.
func TestE2E_ExperimentRunTagging(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}
	pc1 := h.createProviderConfig(t, "claude", "none")
	pc2 := h.createProviderConfig(t, "codex", "none")
	cfg, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake-agent", ProviderConfigID: pc1, SystemPrompt: "control prompt",
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.q.CreateAgentConfigVersion(ctx, gen.CreateAgentConfigVersionParams{ID: uuid.NewString(), AgentConfigID: cfg.ID, Snapshot: "{}"}); err != nil {
		t.Fatal(err)
	}
	snap := SnapshotOf(cfg)
	snap.SystemPrompt, snap.ProviderConfigID = "candidate prompt", pc2
	b, _ := json.Marshal(snap)
	exp, err := h.q.CreateAgentExperiment(ctx, gen.CreateAgentExperimentParams{
		ID: uuid.NewString(), AgentConfigID: cfg.ID, CandidateSnapshot: string(b), SharePercent: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	pick := func(want string) string {
		for {
			id := uuid.NewString()
			if assignArm(exp.ID, id, 50) == want {
				return id
			}
		}
	}
	for _, want := range []string{ArmCandidate, ArmControl} {
		taskID := pick(want)
		if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "ready"}); err != nil {
			t.Fatal(err)
		}
		run := h.firstRun(t, taskID)
		if run.ExperimentID == nil || *run.ExperimentID != exp.ID || run.ExperimentArm == nil || *run.ExperimentArm != want {
			t.Fatalf("%s: experiment tag = %v/%v", want, run.ExperimentID, run.ExperimentArm)
		}
		if want == ArmCandidate && run.AgentConfigVersion != nil {
			t.Fatalf("candidate run version = %d, want NULL", *run.AgentConfigVersion)
		}
		if want == ArmControl && (run.AgentConfigVersion == nil || *run.AgentConfigVersion != 1) {
			t.Fatalf("control run version = %v, want 1", run.AgentConfigVersion)
		}
	}
	// The provider saw the candidate config for exactly one of the runs.
	count := func() (cand, total int) {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		for _, in := range fp.inputs {
			if in.AgentConfig.SystemPrompt == "candidate prompt" && in.AgentConfig.Provider == "codex" {
				cand++
			}
		}
		return cand, len(fp.inputs)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, total := count(); total >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cand, total := count(); cand != 1 || total != 2 {
		t.Fatalf("candidate invocations = %d of %d", cand, total)
	}
}
