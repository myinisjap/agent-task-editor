package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// seedTaskWithProviderList creates one agent config on "ready" whose provider
// list is [primary, fallback], plus a task on "ready".
func (h *e2eHarness) seedTaskWithProviderList(t *testing.T, wfID string) (taskID, configID, primaryPC, fallbackPC string) {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	primaryPC = h.createProviderConfig(t, "fake", "primary-model")
	fallbackPC = h.createProviderConfig(t, "fake", "fallback-model")
	configID = uuid.NewString()
	if _, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: configID, Name: "listed", ProviderConfigID: primaryPC,
		FallbackProviderConfigIds: `["` + fallbackPC + `"]`,
		Labels:                    `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}
	taskID = uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "do the thing", WorkflowID: wfID, RepoID: repoID, Label: "ready",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskID, configID, primaryPC, fallbackPC
}

func providerPool(t *testing.T, q *gen.Queries, providerConfigID string) string {
	t.Helper()
	pc, err := q.GetProviderConfig(context.Background(), providerConfigID)
	if err != nil {
		t.Fatalf("get provider config: %v", err)
	}
	return usagePoolOf(pc)
}

// TestE2E_ProviderList_FallsThroughToNextProvider: with the agent config's
// first provider at its limit, the run starts on the next provider in the
// same agent config, and the run records which provider it used.
func TestE2E_ProviderList_FallsThroughToNextProvider(t *testing.T) {
	step := fakeStep{
		result:  Result{Status: "completed", Outcome: "success"},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	fp := &fakeProvider{steps: []fakeStep{step}}
	h := newE2EHarness(t, fp)
	h.disp.RateLimits = NewRateLimitRegistry()
	wfID := seedE2EWorkflow(t, h.q)
	taskID, configID, primaryPC, fallbackPC := h.seedTaskWithProviderList(t, wfID)

	h.disp.RateLimits.Block(providerPool(t, h.q, primaryPC), time.Now().Add(time.Hour))

	select {
	case <-step.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was never invoked")
	}
	close(step.release)

	locked := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.ActiveAgentRunID != nil }, "run to be created")
	run, err := h.q.GetAgentRun(context.Background(), *locked.ActiveAgentRunID)
	if err != nil {
		t.Fatalf("get agent run: %v", err)
	}
	if run.AgentConfigID == nil || *run.AgentConfigID != configID {
		t.Fatalf("run agent config = %v, want %q", run.AgentConfigID, configID)
	}
	if run.ProviderConfigID == nil || *run.ProviderConfigID != fallbackPC {
		t.Fatalf("run provider config = %v, want fallback %q", run.ProviderConfigID, fallbackPC)
	}
}

// TestE2E_ProviderList_AllBlocked_NoDispatch: every provider in the list at
// its limit means no run at all.
func TestE2E_ProviderList_AllBlocked_NoDispatch(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	h.disp.RateLimits = NewRateLimitRegistry()
	wfID := seedE2EWorkflow(t, h.q)
	taskID, _, primaryPC, fallbackPC := h.seedTaskWithProviderList(t, wfID)

	h.disp.RateLimits.Block(providerPool(t, h.q, primaryPC), time.Now().Add(time.Hour))
	h.disp.RateLimits.Block(providerPool(t, h.q, fallbackPC), time.Now().Add(time.Hour))
	h.waitForSweeps(t, 5)

	runs, err := h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected no runs while every provider is blocked, got %d", len(runs))
	}
}

func TestAgentProviderIDs(t *testing.T) {
	cases := []struct {
		fallbacks string
		want      []string
	}{
		{"", []string{"p"}},
		{"[]", []string{"p"}},
		{"not json", []string{"p"}},
		{`["a","p","","a","b"]`, []string{"p", "a", "b"}},
	}
	for _, c := range cases {
		got := agentProviderIDs(&gen.AgentConfig{ProviderConfigID: "p", FallbackProviderConfigIds: c.fallbacks})
		if len(got) != len(c.want) {
			t.Errorf("%q: got %v, want %v", c.fallbacks, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: got %v, want %v", c.fallbacks, got, c.want)
				break
			}
		}
	}
}

// TestGetLatestTaskSession_KeyedByProvider: a session is only resumed on the
// provider that recorded it, so a task that fell back to another CLI never
// hands it a foreign session id. Runs from before the provider list (no
// provider recorded) belong to the agent config's primary.
func TestGetLatestTaskSession_KeyedByProvider(t *testing.T) {
	ctx := context.Background()
	f, err := os.CreateTemp("", "session-provider-*.db")
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
	q := gen.New(db.SQL())

	wf, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: uuid.NewString(), Name: "wf"})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: t.TempDir(), WorkflowID: &wf.ID}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	mkPC := func(name string) string {
		pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
			ID: uuid.NewString(), Name: name, Provider: "claude", Model: "sonnet", Env: "{}",
		})
		if err != nil {
			t.Fatalf("create provider config: %v", err)
		}
		return pc.ID
	}
	primary, fallback := mkPC("primary"), mkPC("fallback")
	cfgID := uuid.NewString()
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: cfgID, Name: "listed", ProviderConfigID: primary,
		FallbackProviderConfigIds: `["` + fallback + `"]`, Labels: `["ready"]`,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}
	taskID := uuid.NewString()
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: taskID, Title: "t", WorkflowID: wf.ID, RepoID: repoID, Label: "ready"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	addRun := func(providerConfigID *string, session string) {
		id := uuid.NewString()
		if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: id, TaskID: taskID, AgentConfigID: &cfgID}); err != nil {
			t.Fatalf("create run: %v", err)
		}
		if providerConfigID != nil {
			if err := q.SetAgentRunProviderConfig(ctx, gen.SetAgentRunProviderConfigParams{ProviderConfigID: providerConfigID, ID: id}); err != nil {
				t.Fatalf("set run provider: %v", err)
			}
		}
		if err := q.SetAgentRunSession(ctx, gen.SetAgentRunSessionParams{SessionID: session, ID: id}); err != nil {
			t.Fatalf("set run session: %v", err)
		}
	}
	latest := func(providerConfigID string) string {
		sid, err := q.GetLatestTaskSession(ctx, gen.GetLatestTaskSessionParams{
			TaskID: taskID, AgentConfigID: &cfgID, ProviderConfigID: &providerConfigID,
		})
		if err != nil {
			return ""
		}
		return sid
	}

	addRun(nil, "legacy")
	if got := latest(primary); got != "legacy" {
		t.Errorf("primary before fallback runs = %q, want legacy", got)
	}
	if got := latest(fallback); got != "" {
		t.Errorf("fallback must not resume the primary's legacy session, got %q", got)
	}
	addRun(&fallback, "on-fallback")
	if got := latest(fallback); got != "on-fallback" {
		t.Errorf("fallback = %q, want on-fallback", got)
	}
	if got := latest(primary); got != "legacy" {
		t.Errorf("primary must not resume the fallback's session, got %q", got)
	}
}
