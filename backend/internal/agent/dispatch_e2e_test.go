package agent

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

// This file exercises the dispatch → run → transition loop end-to-end: a real
// Dispatcher, Pool, and workflow Engine wired together over an in-file SQLite
// database and a real temp git repo (worktree provisioning shells out to git,
// which works in CI). The only fake is the Provider — the seam the whole design
// is built around — so every other seam (dispatcher ↔ pool ↔ engine ↔ storage)
// runs for real. See issue #58.

// recordingPub records published event types so tests can assert the loop
// emitted the expected WebSocket notifications.
type recordingPub struct {
	mu     sync.Mutex
	events []string
}

func (p *recordingPub) Publish(eventType string, _ map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, eventType)
}

func (p *recordingPub) has(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.events {
		if e == name {
			return true
		}
	}
	return false
}

// fakeStep is one scripted provider invocation: the logs it streams and the
// Result/error it returns. If gate is non-nil, Run closes started (once) and
// blocks on release before returning — used to observe in-flight run state.
type fakeStep struct {
	logs    []LogEntry
	result  Result
	err     error
	started chan struct{}
	release chan struct{}
}

// fakeProvider emits scripted steps in order across successive Run calls. The
// dispatcher builds a fresh Provider per dispatch via its factory, so the same
// *fakeProvider instance is handed out each time and advances its own cursor —
// letting a single script span multiple re-dispatches (e.g. retry → escalate).
// Once the script is exhausted the final step repeats.
type fakeProvider struct {
	mu     sync.Mutex
	steps  []fakeStep
	idx    int
	inputs []RunInput // RunInput of each invocation, in order
}

// input returns the RunInput of the i-th invocation (0-based).
func (f *fakeProvider) input(t *testing.T, i int) RunInput {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.inputs) {
		t.Fatalf("fakeProvider: only %d invocations recorded, want index %d", len(f.inputs), i)
	}
	return f.inputs[i]
}

func (f *fakeProvider) Run(_ context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	f.mu.Lock()
	i := f.idx
	if i >= len(f.steps) {
		i = len(f.steps) - 1
	}
	step := f.steps[i]
	f.idx++
	f.inputs = append(f.inputs, input)
	f.mu.Unlock()

	for _, l := range step.logs {
		logCh <- l
	}
	if step.started != nil {
		close(step.started)
	}
	if step.release != nil {
		<-step.release
	}
	return step.result, step.err
}

// e2eHarness bundles the fully wired loop plus the DB handle tests assert against.
type e2eHarness struct {
	q      *gen.Queries
	engine *workflow.Engine
	pool   *Pool
	disp   *Dispatcher
	pub    *recordingPub
	repo   string // path to the temp git repo
	cancel context.CancelFunc
}

// newE2EHarness stands up a seeded DB, a real git repo, and a running
// Dispatcher+Pool+Engine whose provider factory always returns fp. The
// dispatcher sweeps on a short interval so tests don't wait seconds per step.
// OnTerminal mirrors cmd/server/main.go: push the branch if the repo has a
// remote, then remove the worktree.
func newE2EHarness(t *testing.T, fp *fakeProvider) *e2eHarness {
	t.Helper()

	f, err := os.CreateTemp("", "dispatch-e2e-*.db")
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
	pub := &recordingPub{}
	engine := workflow.New(db.SQL(), pub)

	termQ := gen.New(db.SQL())
	engine.OnTerminal = func(ctx context.Context, task gen.Task) {
		if task.WorktreePath == "" {
			return
		}
		repo, err := termQ.GetRepo(ctx, task.RepoID)
		if err != nil {
			return
		}
		if repo.RemoteUrl != nil && *repo.RemoteUrl != "" && task.Branch != "" {
			_ = PushBranch(ctx, task.WorktreePath, task.Branch)
		}
		_ = RemoveWorktree(ctx, repo.Path, task.WorktreePath)
	}

	pool := NewPool(2, db.SQL(), engine, pub)
	pool.GitName, pool.GitEmail = "Test", "test@example.com"

	factory := func(AgentConfig) Provider { return fp }
	d := NewDispatcher(db.SQL(), pool, engine, factory)
	d.interval = 15 * time.Millisecond
	d.Publisher = pub

	ctx, cancel := context.WithCancel(context.Background())
	// poolDone/dispDone close when their goroutines fully return. Pool.Start
	// blocks on its internal WaitGroup, so poolDone closing means every worker —
	// including any in-flight run's safety-net git commit against the temp repo —
	// has finished. Dispatcher.Run returns on ctx cancel.
	poolDone := make(chan struct{})
	dispDone := make(chan struct{})
	go func() { defer close(poolDone); pool.Start(ctx) }()
	go func() { defer close(dispDone); d.Run(ctx) }()

	// Build the repo before registering the drain cleanup so that this cleanup —
	// registered last, therefore run first (t.Cleanup is LIFO) — cancels the loop
	// and blocks until both goroutines have drained BEFORE Go removes the temp
	// repo (initRepo's t.TempDir), closes the DB, or deletes the DB file. Without
	// this, teardown races an in-flight pool worker: the worker's safety-net
	// `git status`/commit runs against a repo dir being deleted out from under it
	// ("git status: signal: killed: fatal: not a git repository") and its DB
	// writes hit a closed handle. This is the E2E flake's root cause.
	h := &e2eHarness{q: q, engine: engine, pool: pool, disp: d, pub: pub, repo: initRepo(t), cancel: cancel}
	t.Cleanup(func() {
		cancel()
		<-poolDone
		<-dispDone
	})
	return h
}

// seedE2EWorkflow inserts a workflow purpose-built for these tests:
//
//	ready ──success──▶ next     (agent)   golden path lands here (no outgoing → rests)
//	ready ──failure──▶ final    (agent)   terminal; drives OnTerminal
//	ready ──────────▶ parked    (human)   human release path
//
// "ready" is the only agent-pickup label; next/final/parked have no outgoing
// agent transitions so a task that lands on one stops being re-dispatched.
func seedE2EWorkflow(t *testing.T, q *gen.Queries) string {
	t.Helper()
	ctx := context.Background()

	wfID := uuid.NewString()
	if _, err := q.CreateWorkflow(ctx, gen.CreateWorkflowParams{ID: wfID, Name: "E2E", Description: "dispatch loop test"}); err != nil {
		t.Fatalf("create workflow: %v", err)
	}

	labels := []struct {
		name     string
		terminal bool
	}{
		{"ready", false},
		{"next", false},
		{"final", true},
		{"parked", false},
	}
	for i, l := range labels {
		term := int64(0)
		if l.terminal {
			term = 1
		}
		if _, err := q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
			ID: uuid.NewString(), WorkflowID: wfID, Name: l.name, Color: "#000000", SortOrder: int64(i), IsTerminal: term,
		}); err != nil {
			t.Fatalf("create label %s: %v", l.name, err)
		}
	}

	sp := func(s string) *string { return &s }
	transitions := []struct {
		from, to, trigger string
		path              *string
	}{
		{"ready", "next", "agent", sp("success")},
		{"ready", "final", "agent", sp("failure")},
		{"ready", "parked", "human", nil},
	}
	for _, tr := range transitions {
		if _, err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
			ID: uuid.NewString(), WorkflowID: wfID, FromLabel: tr.from, ToLabel: tr.to, TriggerType: tr.trigger, Path: tr.path,
		}); err != nil {
			t.Fatalf("create transition %s→%s: %v", tr.from, tr.to, err)
		}
	}
	return wfID
}

// seedTaskOnReady creates a repo row pointing at the harness git repo, an
// enabled "ready"-triggered agent config, and a task sitting on "ready".
func (h *e2eHarness) seedTaskOnReady(t *testing.T, wfID string) string {
	return h.seedTaskOnReadyWithProvider(t, wfID, "fake", 0)
}

// createProviderConfig creates a provider config with the given provider/model
// and returns its id, for use as an agent config's provider_config_id.
func (h *e2eHarness) createProviderConfig(t *testing.T, provider, model string) string {
	t.Helper()
	ctx := context.Background()
	pc, err := h.q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
		ID: uuid.NewString(), Name: "test-provider", Provider: provider, Model: model, Env: `{}`,
	})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	return pc.ID
}

// seedTaskOnReadyWithProvider is seedTaskOnReady with control over the agent
// config's provider string and resume_sessions flag (the dispatcher's session
// resume lookup is gated on provider == "claude" && resume_sessions != 0; the
// harness factory returns the fake provider regardless of the string).
func (h *e2eHarness) seedTaskOnReadyWithProvider(t *testing.T, wfID, provider string, resumeSessions int64) string {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	pcID := h.createProviderConfig(t, provider, "none")
	if _, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake-agent", ProviderConfigID: pcID,
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
		ResumeSessions: resumeSessions,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	taskID := uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "do the thing", WorkflowID: wfID, RepoID: repoID, Label: "ready",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskID
}

// pollTask waits until cond(task) holds or the deadline elapses. Every caller
// waits for a condition that *should* become true, so this returns the instant
// it does — the deadline only bounds the failure case. It is set generously (15s)
// because CI runs the whole module with `-race`, whose 10-20x slowdown on a
// loaded 2-core runner can starve the dispatcher's sweep goroutine long enough
// that a tighter window (the old 5s) times out before a sweep lands, producing
// a spurious "task ... active=<nil>" flake even though the logic is correct.
func (h *e2eHarness) pollTask(t *testing.T, taskID string, cond func(gen.Task) bool, msg string) gen.Task {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		task, err := h.q.GetTask(context.Background(), taskID)
		if err == nil && cond(task) {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := h.q.GetTask(context.Background(), taskID)
	t.Fatalf("timed out waiting for task %s: %s (label=%q active=%v retry=%d)", taskID, msg, task.Label, task.ActiveAgentRunID, task.TransientRetryCount)
	return gen.Task{}
}

// TestE2E_GoldenPath covers issue #58 scenarios 1 & 2: a task on an agent label
// is picked up (run created, active_agent_run_id set while running), the fake
// provider completes with outcome success, and the task transitions, clears its
// lock, persists logs, and emits the loop's lifecycle events.
func TestE2E_GoldenPath(t *testing.T) {
	step := fakeStep{
		logs: []LogEntry{
			{Type: LogSystem, Content: "starting", At: time.Now()},
			{Type: LogStdout, Content: "did the work", At: time.Now()},
		},
		result:  Result{Status: "completed", Outcome: "success"},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	fp := &fakeProvider{steps: []fakeStep{step}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)

	// Scenario 1: the dispatcher provisions a worktree, creates a run, and locks
	// the task before the provider returns. Observe that in-flight state via the
	// step's gate.
	select {
	case <-step.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was never invoked — dispatcher did not pick up the task")
	}

	locked := h.pollTask(t, taskID, func(tk gen.Task) bool {
		return tk.ActiveAgentRunID != nil && tk.WorktreePath != "" && tk.Branch != ""
	}, "run to be created with the task locked and a worktree provisioned")

	runID := *locked.ActiveAgentRunID
	run, err := h.q.GetAgentRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get agent run: %v", err)
	}
	if run.Status != "running" {
		t.Errorf("expected run status 'running' while gated, got %q", run.Status)
	}
	if run.TaskID != taskID {
		t.Errorf("run belongs to task %q, want %q", run.TaskID, taskID)
	}

	// Let the provider finish.
	close(step.release)

	// Scenario 2: outcome success resolves to "next"; the transition clears the lock.
	final := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.Label == "next" }, "task to transition to 'next'")
	if final.ActiveAgentRunID != nil {
		t.Errorf("expected active_agent_run_id cleared after transition, got %q", *final.ActiveAgentRunID)
	}

	done, _ := h.q.GetAgentRun(context.Background(), runID)
	if done.Status != "completed" {
		t.Errorf("expected run status 'completed', got %q", done.Status)
	}

	logs, err := h.q.ListAgentLogs(context.Background(), runID)
	if err != nil {
		t.Fatalf("list agent logs: %v", err)
	}
	if len(logs) != 2 {
		t.Errorf("expected 2 persisted log entries, got %d", len(logs))
	}

	for _, want := range []string{"task.agent_started", "task.agent_done", "task.label_changed"} {
		if !h.pub.has(want) {
			t.Errorf("expected %s event to be published", want)
		}
	}
}

// TestE2E_TransientRetryThenEscalate covers scenario 3: a transient infra
// failure schedules a backed-off retry (budget under cap), and once the budget
// is exhausted the task escalates to waiting_human and stays locked.
func TestE2E_TransientRetryThenEscalate(t *testing.T) {
	// Every run fails transiently. With max_retries=1 the first failure schedules
	// a retry (count→1, next_retry_at set); the re-dispatch's failure exhausts the
	// budget and escalates.
	fp := &fakeProvider{steps: []fakeStep{
		{err: &ErrTransient{Cause: context.DeadlineExceeded}, result: Result{Status: "failed"}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)

	// First transient failure: retry scheduled with backoff.
	h.pollTask(t, taskID, func(tk gen.Task) bool {
		return tk.TransientRetryCount == 1 && tk.NextRetryAt != nil
	}, "first transient failure to schedule a backed-off retry")

	// After the backoff elapses the dispatcher re-picks the task, it fails again,
	// and the exhausted budget escalates to waiting_human. The counter resets so a
	// human-triggered re-dispatch starts fresh, and the task stays locked.
	esc := h.pollTask(t, taskID, func(tk gen.Task) bool {
		return tk.ActiveAgentRunID != nil && tk.TransientRetryCount == 0
	}, "retry budget to be exhausted and the task escalated to waiting_human")

	run, err := h.q.GetAgentRun(context.Background(), *esc.ActiveAgentRunID)
	if err != nil {
		t.Fatalf("get agent run: %v", err)
	}
	if run.Status != "waiting_human" {
		t.Errorf("expected escalated run status 'waiting_human', got %q", run.Status)
	}
	if esc.Label != "ready" {
		t.Errorf("expected task to stay on 'ready' while waiting_human, got %q", esc.Label)
	}
	if !h.pub.has("task.needs_human") {
		t.Error("expected task.needs_human event on retry-budget exhaustion")
	}
}

// TestE2E_TerminalLabelTearsDownWorktree covers scenario 4: completing with the
// outcome that routes to a terminal label runs OnTerminal, which (with no remote
// configured) skips the push and removes the worktree while keeping the branch.
func TestE2E_TerminalLabelTearsDownWorktree(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "completed", Outcome: "failure"}}, // ready --failure--> final (terminal)
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)

	// Wait until the worktree has been provisioned so we know which path to check.
	provisioned := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.WorktreePath != "" }, "worktree to be provisioned")
	wtPath := provisioned.WorktreePath
	branch := provisioned.Branch

	// Task reaches the terminal label.
	term := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.Label == "final" }, "task to reach terminal label 'final'")
	if term.ActiveAgentRunID != nil {
		t.Errorf("expected lock cleared on terminal transition, got %q", *term.ActiveAgentRunID)
	}

	// OnTerminal removes the worktree directory...
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(wtPath); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("expected worktree %q to be removed by OnTerminal", wtPath)
	}

	// ...but keeps the branch for later review.
	if !branchExists(t, h.repo, branch) {
		t.Errorf("expected branch %q to be kept after teardown", branch)
	}
}

// TestE2E_WaitingHumanStaysLocked covers scenario 5: a run that returns
// waiting_human leaves the task locked so the dispatcher never re-picks it, and
// a human transition is what finally releases the lock and moves the task.
func TestE2E_WaitingHumanStaysLocked(t *testing.T) {
	msg := "need a human"
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "waiting_human", Message: &msg}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)

	// The run reaches waiting_human and the task stays locked on "ready".
	locked := h.pollTask(t, taskID, func(tk gen.Task) bool {
		if tk.ActiveAgentRunID == nil {
			return false
		}
		run, err := h.q.GetAgentRun(context.Background(), *tk.ActiveAgentRunID)
		return err == nil && run.Status == "waiting_human"
	}, "run to reach waiting_human with the task locked")
	runID := *locked.ActiveAgentRunID

	if !h.pub.has("task.needs_human") {
		t.Error("expected task.needs_human event")
	}

	// The lock must hold: give the dispatcher room to sweep several times and
	// confirm it never starts a second run.
	time.Sleep(200 * time.Millisecond)
	still, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if still.ActiveAgentRunID == nil || *still.ActiveAgentRunID != runID {
		t.Fatalf("expected task to stay locked on run %q, got %v", runID, still.ActiveAgentRunID)
	}
	if still.Label != "ready" {
		t.Errorf("expected task to stay on 'ready' while waiting_human, got %q", still.Label)
	}
	runs, err := h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected exactly 1 run while waiting_human (no re-dispatch), got %d", len(runs))
	}

	// A human acts: transitioning ready→parked clears the lock (UpdateTaskLabel
	// always nulls active_agent_run_id) and moves the task off the agent label.
	if err := h.engine.Transition(context.Background(), taskID, "parked", workflow.TriggerHuman, "human-1", "handled manually"); err != nil {
		t.Fatalf("human transition: %v", err)
	}
	released, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task after transition: %v", err)
	}
	if released.Label != "parked" {
		t.Errorf("expected task moved to 'parked', got %q", released.Label)
	}
	if released.ActiveAgentRunID != nil {
		t.Errorf("expected lock cleared after human transition, got %q", *released.ActiveAgentRunID)
	}
}

// TestE2E_ReplyResumesSession covers the reply-to-agent flow (#78) plus session
// continuity (#77): a run pauses on waiting_human after recording its provider
// session; DispatchReply starts a new run that carries the human's message and
// the prior session id, records the reply in the new run's log, and leaves the
// replied-to run in waiting_human (matching the approve/reject flows).
func TestE2E_ReplyResumesSession(t *testing.T) {
	question := "which approach do you want?"
	fp := &fakeProvider{steps: []fakeStep{
		{result: Result{Status: "waiting_human", Message: &question, SessionID: "sess-123"}},
		{result: Result{Status: "completed", Outcome: "success"}},
	}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	// provider "claude" + resume_sessions on gates the dispatcher's session
	// lookup; the harness factory still returns the fake provider.
	taskID := h.seedTaskOnReadyWithProvider(t, wfID, "claude", 1)

	// First run reaches waiting_human with its session recorded.
	locked := h.pollTask(t, taskID, func(tk gen.Task) bool {
		if tk.ActiveAgentRunID == nil {
			return false
		}
		run, err := h.q.GetAgentRun(context.Background(), *tk.ActiveAgentRunID)
		return err == nil && run.Status == "waiting_human"
	}, "run to reach waiting_human")
	firstRunID := *locked.ActiveAgentRunID

	firstRun, err := h.q.GetAgentRun(context.Background(), firstRunID)
	if err != nil {
		t.Fatalf("get first run: %v", err)
	}
	if firstRun.SessionID != "sess-123" {
		t.Fatalf("expected session sess-123 persisted on the waiting run, got %q", firstRun.SessionID)
	}

	newRunID, err := h.disp.DispatchReply(context.Background(), taskID, "use approach B")
	if err != nil {
		t.Fatalf("DispatchReply: %v", err)
	}
	if newRunID == firstRunID {
		t.Fatal("expected a new run, got the waiting run's id")
	}

	// Second run completes with success → task transitions ready → next.
	h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.Label == "next" }, "reply run to complete and transition")

	// The reply run received the human's message and the prior session.
	in := fp.input(t, 1)
	if in.HumanReply == nil || *in.HumanReply != "use approach B" {
		t.Errorf("expected HumanReply %q, got %v", "use approach B", in.HumanReply)
	}
	if in.ResumeSessionID != "sess-123" {
		t.Errorf("expected ResumeSessionID sess-123, got %q", in.ResumeSessionID)
	}

	// The reply is recorded at the top of the new run's log.
	logs, err := h.q.ListAgentLogs(context.Background(), newRunID)
	if err != nil || len(logs) == 0 {
		t.Fatalf("expected logs on reply run, err=%v", err)
	}
	if want := "Human reply: use approach B"; logs[0].Content != want {
		t.Errorf("expected first log entry %q, got %q", want, logs[0].Content)
	}

	// The replied-to run keeps waiting_human (approve/reject parity).
	firstRun, err = h.q.GetAgentRun(context.Background(), firstRunID)
	if err != nil {
		t.Fatalf("get first run: %v", err)
	}
	if firstRun.Status != "waiting_human" {
		t.Errorf("expected replied-to run to stay waiting_human, got %q", firstRun.Status)
	}
}

// seedTaskWithTwoConfigs creates a repo, two enabled "ready"-triggered agent
// configs (priorities 0 and 1, so config0 is tried first), and a task on
// "ready". Returns the task id plus both config ids.
func (h *e2eHarness) seedTaskWithTwoConfigs(t *testing.T, wfID string) (taskID, config0ID, config1ID string) {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	config0ID = uuid.NewString()
	pc0ID := h.createProviderConfig(t, "fake", "none")
	if _, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: config0ID, Name: "primary", ProviderConfigID: pc0ID,
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
		Priority: 0,
	}); err != nil {
		t.Fatalf("create agent config 0: %v", err)
	}
	config1ID = uuid.NewString()
	pc1ID := h.createProviderConfig(t, "fake", "none")
	if _, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: config1ID, Name: "backup", ProviderConfigID: pc1ID,
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
		Priority: 1,
	}); err != nil {
		t.Fatalf("create agent config 1: %v", err)
	}

	taskID = uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{
		ID: taskID, Title: "do the thing", WorkflowID: wfID, RepoID: repoID, Label: "ready",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskID, config0ID, config1ID
}

// TestE2E_FailoverToBackupConfig covers the priority-based failover feature:
// two enabled configs share a label (priorities 0 and 1); when the
// priority-0 config is rate-limit-blocked, dispatch skips it and starts the
// run on the priority-1 backup instead. When both are blocked, no run is
// created at all.
func TestE2E_FailoverToBackupConfig(t *testing.T) {
	step := fakeStep{
		result:  Result{Status: "completed", Outcome: "success"},
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	fp := &fakeProvider{steps: []fakeStep{step}}
	h := newE2EHarness(t, fp)
	h.disp.RateLimits = NewRateLimitRegistry()
	wfID := seedE2EWorkflow(t, h.q)
	taskID, config0ID, config1ID := h.seedTaskWithTwoConfigs(t, wfID)

	// Block the primary (priority 0) config before the dispatcher ever sweeps.
	h.disp.RateLimits.Block(config0ID, time.Now().Add(time.Hour))

	select {
	case <-step.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was never invoked — dispatcher did not pick up the task")
	}
	close(step.release)

	locked := h.pollTask(t, taskID, func(tk gen.Task) bool { return tk.ActiveAgentRunID != nil }, "run to be created")
	run, err := h.q.GetAgentRun(context.Background(), *locked.ActiveAgentRunID)
	if err != nil {
		t.Fatalf("get agent run: %v", err)
	}
	if run.AgentConfigID == nil || *run.AgentConfigID != config1ID {
		t.Fatalf("expected run to use backup config %q, got %v", config1ID, run.AgentConfigID)
	}
}

// TestE2E_FailoverAllBlocked_NoDispatch covers the case where every config
// matching a task's label is rate-limited: dispatch must skip the sweep
// entirely rather than creating a run.
func TestE2E_FailoverAllBlocked_NoDispatch(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	h.disp.RateLimits = NewRateLimitRegistry()
	wfID := seedE2EWorkflow(t, h.q)
	taskID, config0ID, config1ID := h.seedTaskWithTwoConfigs(t, wfID)

	h.disp.RateLimits.Block(config0ID, time.Now().Add(time.Hour))
	h.disp.RateLimits.Block(config1ID, time.Now().Add(time.Hour))

	// Give the dispatcher several sweeps' worth of time to (not) act.
	time.Sleep(200 * time.Millisecond)

	task, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.ActiveAgentRunID != nil {
		t.Fatalf("expected no run created while all matching configs are blocked, got active run %q", *task.ActiveAgentRunID)
	}
	runs, err := h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("expected 0 runs while all matching configs are blocked, got %d", len(runs))
	}
}

// seedTwoTasksOnRepoWithLimit creates a single repo with the given
// max_concurrent_runs (nil = unset/global fallback), one enabled agent
// config matching "ready", and two tasks both sitting on "ready" against
// that repo. Used to exercise Dispatcher.repoAtLimit end-to-end: with a
// limit of 1, only one of the two tasks should ever hold an active run at
// once.
func (h *e2eHarness) seedTwoTasksOnRepoWithLimit(t *testing.T, wfID string, limit *int64) (task1ID, task2ID string) {
	t.Helper()
	ctx := context.Background()

	repoID := uuid.NewString()
	if _, err := h.q.CreateRepo(ctx, gen.CreateRepoParams{
		ID: repoID, Name: "repo", Path: h.repo, WorkflowID: &wfID, MaxConcurrentRuns: limit,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	pcID := h.createProviderConfig(t, "fake", "none")
	if _, err := h.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "fake-agent", ProviderConfigID: pcID,
		Labels: `["ready"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	task1ID = uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{
		ID: task1ID, Title: "task one", WorkflowID: wfID, RepoID: repoID, Label: "ready",
	}); err != nil {
		t.Fatalf("create task 1: %v", err)
	}
	task2ID = uuid.NewString()
	if _, err := h.q.CreateTask(ctx, gen.CreateTaskParams{
		ID: task2ID, Title: "task two", WorkflowID: wfID, RepoID: repoID, Label: "ready",
	}); err != nil {
		t.Fatalf("create task 2: %v", err)
	}
	return task1ID, task2ID
}

// TestE2E_RepoConcurrencyLimit covers issue #255: a repo with
// max_concurrent_runs=1 and two eligible tasks must only ever have one
// active run at a time — the second task is skipped by the dispatcher's
// repoAtLimit check until the first run's lock is released, even though the
// pool has a free worker slot (NewPool(2, ...) in newE2EHarness).
func TestE2E_RepoConcurrencyLimit(t *testing.T) {
	// Two distinct steps — the first task's run gates on step1's channels
	// while we assert the limit; the second task's eventual run (once the
	// first finishes and frees the repo's only slot) uses step2 and
	// completes immediately (ungated), since by that point there's nothing
	// further to observe about it beyond "it got dispatched".
	step1 := fakeStep{result: Result{Status: "completed", Outcome: "success"}, started: make(chan struct{}), release: make(chan struct{})}
	step2 := fakeStep{result: Result{Status: "completed", Outcome: "success"}}
	fp := &fakeProvider{steps: []fakeStep{step1, step2}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	limit := int64(1)
	task1ID, task2ID := h.seedTwoTasksOnRepoWithLimit(t, wfID, &limit)

	// Exactly one of the two tasks should pick up a run; the other must stay
	// unlocked while the first is gated in-flight (holding the repo's only slot).
	select {
	case <-step1.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was never invoked — dispatcher did not pick up either task")
	}

	// Give the dispatcher several more sweeps' worth of time to (not) also
	// dispatch the second task while the first holds the repo's only slot.
	time.Sleep(200 * time.Millisecond)

	task1, err := h.q.GetTask(context.Background(), task1ID)
	if err != nil {
		t.Fatalf("get task1: %v", err)
	}
	task2, err := h.q.GetTask(context.Background(), task2ID)
	if err != nil {
		t.Fatalf("get task2: %v", err)
	}
	lockedCount := 0
	if task1.ActiveAgentRunID != nil {
		lockedCount++
	}
	if task2.ActiveAgentRunID != nil {
		lockedCount++
	}
	if lockedCount != 1 {
		t.Fatalf("expected exactly 1 task locked with an active run while repo is at its limit of 1, got %d (task1 active=%v, task2 active=%v)", lockedCount, task1.ActiveAgentRunID, task2.ActiveAgentRunID)
	}

	// Let the first run finish; the second task should then get its turn.
	// step2 (the second task's run) is ungated and completes immediately, so
	// poll for it having left "ready" entirely (workflow transition on
	// success) rather than the transient active_agent_run_id lock, which can
	// already be cleared again by the time this observes it.
	close(step1.release)

	secondTaskID := task2ID
	if task1.ActiveAgentRunID == nil {
		secondTaskID = task1ID
	}
	second := h.pollTask(t, secondTaskID, func(tk gen.Task) bool { return tk.Label != "ready" }, "second task to be dispatched and transition once the repo's slot freed up")
	if second.Label != "next" {
		t.Fatalf("expected the second task to transition to 'next' on success, got label %q", second.Label)
	}
}

// TestE2E_RepoConcurrencyLimit_UnsetFallsBackToPool is the negative control
// for TestE2E_RepoConcurrencyLimit: with no repo-specific limit (nil), the
// pool's global MAX_WORKERS (2 in this harness) is the only cap, so both
// tasks on the same repo can be in flight simultaneously — "unset limit
// preserves today's behavior exactly" per the acceptance criteria.
func TestE2E_RepoConcurrencyLimit_UnsetFallsBackToPool(t *testing.T) {
	// Two distinct steps (one per task's run) so each gets its own
	// started/release channel pair — sharing a single fakeStep across two
	// concurrently in-flight runs would double-close its channels.
	step1 := fakeStep{result: Result{Status: "completed", Outcome: "success"}, started: make(chan struct{}), release: make(chan struct{})}
	step2 := fakeStep{result: Result{Status: "completed", Outcome: "success"}, started: make(chan struct{}), release: make(chan struct{})}
	fp := &fakeProvider{steps: []fakeStep{step1, step2}}
	h := newE2EHarness(t, fp)
	wfID := seedE2EWorkflow(t, h.q)
	task1ID, task2ID := h.seedTwoTasksOnRepoWithLimit(t, wfID, nil)

	task1 := h.pollTask(t, task1ID, func(tk gen.Task) bool { return tk.ActiveAgentRunID != nil }, "task1 to be dispatched")
	task2 := h.pollTask(t, task2ID, func(tk gen.Task) bool { return tk.ActiveAgentRunID != nil }, "task2 to be dispatched")
	if task1.ActiveAgentRunID == nil || task2.ActiveAgentRunID == nil {
		t.Fatalf("expected both tasks to hold an active run with no repo-specific limit set")
	}
	close(step1.release)
	close(step2.release)
}

// TestE2E_NilProviderFailsRunCleanly covers the deprecated-provider landmine
// fix: if ProviderFactory returns nil (e.g. the agent config's provider is
// disabled/unrecognized), startRun must fail the run without enqueuing a job
// with a nil Provider (which would panic the pool worker goroutine), AND must
// keep the task's active-run lock set (escalated to waiting_human) rather
// than clearing it.
//
// Clearing the lock was the first cut of this fix and is deliberately wrong:
// this failure happens before any real provider work runs, unlike a normal
// "failed" terminal run whose retry cadence is naturally throttled by however
// long the real attempt took. Clearing the lock here left the task
// immediately re-pickup-eligible (ListAgentPickupTasks gates only on
// active_agent_run_id being NULL), so the 15ms-interval sweep in this test
// harness re-dispatched the same task every tick — a real hot-loop bug in
// production too, not just a test artifact — creating a new failed run every
// 15ms until a human intervened. Escalating to waiting_human (mirroring
// checkCostBudget's exhausted-budget escalation) keeps the task locked on
// this one run, so exactly one run is ever created for it here.
// See internal/api/handlers/agents.go's deprecatedProviders and
// cmd/server/main.go's providerFactory default case.
func TestE2E_NilProviderFailsRunCleanly(t *testing.T) {
	fp := &fakeProvider{steps: []fakeStep{{result: Result{Status: "completed", Outcome: "success"}}}}
	h := newE2EHarness(t, fp)
	// Override the harness's always-succeed factory with one that mimics a
	// disabled/unrecognized provider by returning nil.
	h.disp.ProviderFactory = func(AgentConfig) Provider { return nil }
	wfID := seedE2EWorkflow(t, h.q)
	taskID := h.seedTaskOnReady(t, wfID)

	// persistRunRow creates the run row (and sets the task's active-run
	// pointer) *before* the nil-provider guard runs, so there's a real,
	// expected window where ListAgentRuns already returns the row while it's
	// still "pending". Poll until the run reaches its terminal
	// "waiting_human" state before asserting anything about it, instead of
	// failing on that intermediate "pending" state.
	deadline := time.Now().Add(5 * time.Second)
	var runs []gen.AgentRun
	for time.Now().Before(deadline) {
		var err error
		runs, err = h.q.ListAgentRuns(context.Background(), taskID)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		if len(runs) > 0 && runs[0].Status == "waiting_human" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(runs) == 0 || runs[0].Status != "waiting_human" {
		t.Fatalf("timed out waiting for a run to be created and escalated for the nil-provider task")
	}

	// Give the sweep several more ticks (it runs every 15ms in this harness)
	// to prove the lock actually prevents re-dispatch, instead of just
	// happening to win a single race.
	time.Sleep(150 * time.Millisecond)

	task, err := h.q.GetTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.ActiveAgentRunID == nil {
		t.Fatalf("expected task to remain locked on the escalated run, but its active-run lock was cleared")
	}
	runs, err = h.q.ListAgentRuns(context.Background(), taskID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected exactly one run (the lock should have prevented re-dispatch), got %d", len(runs))
	}
	if runs[0].Status != "waiting_human" {
		t.Fatalf("expected the run to stay waiting_human, got %q", runs[0].Status)
	}
}
