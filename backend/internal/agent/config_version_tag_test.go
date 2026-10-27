package agent

import (
	"context"
	"testing"

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
