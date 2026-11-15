package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestSubtaskReadiness(t *testing.T) {
	type tc struct {
		foreign    bool
		ms, verify string
		ready      bool
		wait       string
	}
	cases := []tc{
		{false, "", "", false, SubtaskWaitMergeBack},
		{false, "pending", "", false, SubtaskWaitMergeBack},
		{false, "merged", "", true, ""},
		{false, "merge_conflict", "", true, ""},
		{false, "merged", "fail", true, ""}, // home children ignore verify here
		{true, "", "", false, SubtaskWaitAwaitingPR},
		{true, "pending", "pass", false, SubtaskWaitAwaitingPR},
		{true, "pr_open", "", true, ""},
		{true, "pr_open", "pass", true, ""},
		{true, "pr_open", "fail", false, SubtaskWaitVerifyFailed},
		{true, "pr_open", "timeout", false, SubtaskWaitVerifyFailed},
		{true, "pr_open", "error", false, SubtaskWaitVerifyFailed},
		{true, "merged", "pass", true, ""},
		{true, "merged", "fail", false, SubtaskWaitVerifyFailed},
		{true, "pr_closed", "", false, SubtaskWaitPRClosed},
		{true, "pr_closed", "pass", false, SubtaskWaitPRClosed},
	}
	for _, c := range cases {
		r, w := SubtaskReadiness(c.foreign, c.ms, c.verify)
		if r != c.ready || w != c.wait {
			t.Errorf("SubtaskReadiness(%v,%q,%q) = %v,%q want %v,%q", c.foreign, c.ms, c.verify, r, w, c.ready, c.wait)
		}
	}
}

// putVerify upserts the (single) verify row for a task's run.
func putVerify(t *testing.T, q *gen.Queries, taskID, runID, status string) {
	t.Helper()
	ctx := context.Background()
	if _, err := q.GetRunVerification(ctx, runID); err != nil {
		if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: taskID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.UpsertRunVerification(ctx, gen.UpsertRunVerificationParams{
		RunID: runID, TaskID: taskID, Command: "c", Status: status,
	}); err != nil {
		t.Fatal(err)
	}
}

func setMS(t *testing.T, q *gen.Queries, id, ms string) {
	t.Helper()
	if _, err := q.SetTaskMergeStatus(context.Background(), gen.SetTaskMergeStatusParams{MergeStatus: ms, ID: id}); err != nil {
		t.Fatal(err)
	}
}

func labelOf(t *testing.T, q *gen.Queries, id string) string {
	t.Helper()
	tk, err := q.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tk.Label
}

func TestParentReadiness_HomeAndForeign(t *testing.T) {
	coord, q, _, parent, home := setupCoordEnv(t)
	ctx := context.Background()
	foreign, _ := setupForeignChild(t, q, parent)
	run := uuid.NewString()

	// Foreign pr_open + pass, home not merged back: not ready.
	setMS(t, q, foreign.ID, "pr_open")
	putVerify(t, q, foreign.ID, run, "pass")
	coord.ReevaluateParent(ctx, parent.ID)
	if l := labelOf(t, q, parent.ID); l != "work" {
		t.Fatalf("advanced to %q while home child not merged", l)
	}
	// Home merged, foreign PR not opened: not ready.
	setMS(t, q, home.ID, "merged")
	setMS(t, q, foreign.ID, "pending")
	coord.ReevaluateParent(ctx, parent.ID)
	if l := labelOf(t, q, parent.ID); l != "work" {
		t.Fatalf("advanced to %q while foreign PR not opened", l)
	}
	// PR open but latest verify failed: not ready.
	setMS(t, q, foreign.ID, "pr_open")
	putVerify(t, q, foreign.ID, run, "fail")
	coord.ReevaluateParent(ctx, parent.ID)
	if l := labelOf(t, q, parent.ID); l != "work" {
		t.Fatalf("advanced to %q while foreign verify failed", l)
	}
	// PR closed unmerged: not ready.
	putVerify(t, q, foreign.ID, run, "pass")
	setMS(t, q, foreign.ID, "pr_closed")
	coord.ReevaluateParent(ctx, parent.ID)
	if l := labelOf(t, q, parent.ID); l != "work" {
		t.Fatalf("advanced to %q while foreign PR closed", l)
	}
	// PR open + verify pass: ready (fallback auto-advance, no agent configured).
	setMS(t, q, foreign.ID, "pr_open")
	coord.ReevaluateParent(ctx, parent.ID)
	if l := labelOf(t, q, parent.ID); l == "work" {
		t.Fatalf("parent did not advance with home merged and foreign pr_open+pass")
	}
}

func TestParentReadiness_ForeignNoVerifyRow(t *testing.T) {
	coord, q, _, parent, home := setupCoordEnv(t)
	foreign, _ := setupForeignChild(t, q, parent)
	setMS(t, q, home.ID, "merged")
	setMS(t, q, foreign.ID, "pr_open")
	coord.ReevaluateParent(context.Background(), parent.ID)
	if l := labelOf(t, q, parent.ID); l == "work" {
		t.Fatal("no verify row must count as passed")
	}
}

func TestParentReadiness_IntegrationPendingWhenDispatchable(t *testing.T) {
	coord, q, _, parent, home := setupCoordEnv(t)
	ctx := context.Background()
	foreign, _ := setupForeignChild(t, q, parent)
	pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: uuid.NewString(), Name: "p", Provider: "fake", Model: "none", Env: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "a", ProviderConfigID: pc.ID, Labels: `["work"]`, MaxRetries: 1, RetryBackoffSecs: 1,
	}); err != nil {
		t.Fatal(err)
	}
	setMS(t, q, home.ID, "merged")
	setMS(t, q, foreign.ID, "pr_open")
	putVerify(t, q, foreign.ID, uuid.NewString(), "pass")
	coord.ReevaluateParent(ctx, parent.ID)
	if got := coord.integrationStatus(ctx, parent.ID); got != "pending" {
		t.Fatalf("integration_status = %q, want pending", got)
	}
	if l := labelOf(t, q, parent.ID); l != "work" {
		t.Fatalf("parent moved to %q; integration run should be dispatched instead", l)
	}

	// Integration prompt lists the foreign child with repo, PR and verify.
	if _, err := q.SetTaskPR(ctx, gen.SetTaskPRParams{GitState: "pr_open", PrUrl: "https://example.test/b/pr/7", ID: foreign.ID}); err != nil {
		t.Fatal(err)
	}
	p := coord.BuildIntegrationContext(ctx, parent.ID, "make test")
	if p == nil {
		t.Fatal("nil integration context")
	}
	for _, want := range []string{"Subtasks in other repos were NOT merged here", "repo b (other repo)", "own PR, not merged into this branch", "https://example.test/b/pr/7, state open", "Verify: pass", "Verify: none configured", "Review the whole change set across repos", foreign.Branch} {
		if !strings.Contains(*p, want) {
			t.Errorf("prompt missing %q:\n%s", want, *p)
		}
	}
}

func TestBuildIntegrationContext_SingleRepoUnchanged(t *testing.T) {
	coord, q, _, parent, home := setupCoordEnv(t)
	ctx := context.Background()
	setMS(t, q, home.ID, "merged")
	coord.setIntegration(ctx, parent.ID, "pending")
	p := coord.BuildIntegrationContext(ctx, parent.ID, "make test")
	if p == nil {
		t.Fatal("nil")
	}
	for _, bad := range []string{"other repo", "Verify:", "across repos", "PR:"} {
		if strings.Contains(*p, bad) {
			t.Errorf("single-repo prompt contains %q:\n%s", bad, *p)
		}
	}
	if !strings.HasPrefix(*p, "All subtasks of this task have finished. Each subtask branch was merged into this task's branch (") {
		t.Errorf("legacy header changed:\n%s", *p)
	}
	if !strings.Contains(*p, "   Branch: "+home.Branch+" (merged and deleted)\n") || !strings.HasSuffix(*p, "ate runs the verify command again after you succeed.\n") {
		t.Errorf("legacy body changed:\n%s", *p)
	}
}

// The SQL view (ListTaskBlockers.satisfied) and Go SubtaskReadiness must agree.
func TestSubtaskLandedParity(t *testing.T) {
	_, q, _, parent, home := setupCoordEnv(t)
	ctx := context.Background()
	foreign, _ := setupForeignChild(t, q, parent)
	for _, id := range []string{home.ID, foreign.ID} {
		if err := q.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{TaskID: parent.ID, DependsOnTaskID: id}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(verify string) {
		t.Helper()
		blockers, err := q.ListTaskBlockers(ctx, parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range blockers {
			var ms string
			isForeign := b.TaskID == foreign.ID
			tk, _ := q.GetTask(ctx, b.TaskID)
			ms = tk.MergeStatus
			v := ""
			if isForeign {
				v = verify
			}
			ready, _ := SubtaskReadiness(isForeign, ms, v)
			if (b.Satisfied != 0) != ready {
				t.Errorf("foreign=%v ms=%q verify=%q: SQL satisfied=%d, Go ready=%v", isForeign, ms, v, b.Satisfied, ready)
			}
		}
	}
	statuses := []string{"pending", "pr_open", "merged", "pr_closed", "merge_conflict"}
	run := uuid.NewString()
	for _, verify := range []string{"", "pass", "fail", "timeout", "error"} {
		if verify != "" {
			putVerify(t, q, foreign.ID, run, verify)
		}
		for _, ms := range statuses {
			setMS(t, q, foreign.ID, ms)
			setMS(t, q, home.ID, ms)
			check(verify)
		}
	}
}

func TestBlockReason_ForeignChildWaitingOn(t *testing.T) {
	f := newBlockReasonFixture(t)
	ctx := context.Background()
	if _, err := f.q.CreateWorkflowLabel(ctx, gen.CreateWorkflowLabelParams{
		ID: uuid.NewString(), WorkflowID: f.workflowID, Name: "fin", Color: "#000", SortOrder: 2, IsTerminal: 1,
	}); err != nil {
		t.Fatal(err)
	}
	f.createEnabledConfig(t, "ready")
	repoB, err := f.q.CreateRepo(ctx, gen.CreateRepoParams{ID: uuid.NewString(), Name: "b", Path: "/tmp/br-b-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	parent := f.newTask(t, "ready")
	mk := func(title string) gen.Task {
		c, err := f.q.CreateSubtask(ctx, gen.CreateSubtaskParams{
			ID: uuid.NewString(), Title: title, Type: "task", Label: "fin", RepoID: repoB.ID, WorkflowID: f.workflowID, ParentTaskID: &parent.ID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.q.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{TaskID: parent.ID, DependsOnTaskID: c.ID}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	closed := mk("closed-one")
	failed := mk("failed-one")
	setMS(t, f.q, closed.ID, "pr_closed")
	setMS(t, f.q, failed.ID, "pr_open")
	putVerify(t, f.q, failed.ID, uuid.NewString(), "fail")

	r := NewBlockReasonResolver(f.q, nil, nil, nil)
	reason := resolveOne(t, r, parent)
	if reason == nil || reason.Code != BlockDependency {
		t.Fatalf("want dependency reason, got %+v", reason)
	}
	if !strings.Contains(reason.Message, "needs attention: ") || !strings.Contains(reason.Message, `"closed-one" (PR closed unmerged)`) || !strings.Contains(reason.Message, `"failed-one" (verify failed)`) {
		t.Errorf("message = %q", reason.Message)
	}
	got := map[string]string{}
	for _, d := range reason.Detail.([]blockerDetail) {
		got[d.Title] = d.WaitingOn
	}
	if got["closed-one"] != SubtaskWaitPRClosed || got["failed-one"] != SubtaskWaitVerifyFailed {
		t.Errorf("waiting_on = %v", got)
	}
	// And the parent is not dispatchable.
	tasks, err := f.q.ListAgentPickupTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tasks {
		if tk.ID == parent.ID {
			t.Fatal("parent must not be pickup-eligible while foreign children are blocked")
		}
	}
}
