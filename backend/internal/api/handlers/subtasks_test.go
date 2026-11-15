package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func setupSubtaskRouter(t *testing.T) (http.Handler, *storage.DB, *gen.Queries, string, string) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	engine := workflow.New(db.SQL(), noopPub{})

	wfs, _ := q.ListWorkflows(context.Background())
	wfID := wfs[0].ID
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfID,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	tasksH := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil, nil)
	subH := handlers.NewSubtasksHandler(q, db.SQL(), noopPub{})
	depsH := handlers.NewDependenciesHandler(q, db.SQL(), noopPub{})

	r := chi.NewRouter()
	r.Get("/tasks", tasksH.List)
	r.Get("/tasks/{id}", tasksH.Get)
	r.Post("/tasks/{id}/subtasks", subH.Create)
	r.Get("/tasks/{id}/dependencies", depsH.List)
	return r, db, q, wfID, repoID
}

func postSubtask(t *testing.T, r http.Handler, parentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+parentID+"/subtasks", jsonBody(t, body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestSubtasks_Create_DefaultsAndEdge verifies a child lands on the human-gate
// label, records its parent, and gets a parent→child dependency edge.
func TestSubtasks_Create_DefaultsAndEdge(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")

	w := postSubtask(t, r, parent.ID, map[string]any{"title": "child A", "description": "do A"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create subtask: %d %s", w.Code, w.Body.String())
	}
	var child struct {
		ID           string  `json:"id"`
		Label        string  `json:"label"`
		ParentTaskID *string `json:"parent_task_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &child); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if child.Label != "not_ready" {
		t.Fatalf("child should land on the agent_ignore gate label not_ready, got %q", child.Label)
	}
	if child.ParentTaskID == nil || *child.ParentTaskID != parent.ID {
		t.Fatalf("child parent_task_id not set to parent")
	}

	// Parent→child edge: the parent is blocked by the child.
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+parent.ID+"/dependencies", nil)
	dw := httptest.NewRecorder()
	r.ServeHTTP(dw, req)
	var deps struct {
		BlockedBy []struct {
			TaskID string `json:"task_id"`
		} `json:"blocked_by"`
	}
	_ = json.Unmarshal(dw.Body.Bytes(), &deps)
	if len(deps.BlockedBy) != 1 || deps.BlockedBy[0].TaskID != child.ID {
		t.Fatalf("expected parent blocked by child, got %+v", deps.BlockedBy)
	}
}

// TestSubtasks_DepthLimit verifies a subtask cannot itself create subtasks.
func TestSubtasks_DepthLimit(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	w := postSubtask(t, r, parent.ID, map[string]any{"title": "child", "description": "x"})
	var child struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &child)

	w2 := postSubtask(t, r, child.ID, map[string]any{"title": "grandchild", "description": "x"})
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("grandchild should be rejected (depth limit), got %d", w2.Code)
	}
}

// TestSubtasks_NonGateLabelRejected verifies a child can't be dropped straight
// into an agent-triggerable label.
func TestSubtasks_NonGateLabelRejected(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	w := postSubtask(t, r, parent.ID, map[string]any{"title": "child", "description": "x", "label": "work"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-agent_ignore label should be rejected, got %d %s", w.Code, w.Body.String())
	}
}

// TestSubtasks_Rollup verifies GET /tasks/{parent} carries subtask rollup counts
// and the parent_id filter returns the family.
func TestSubtasks_Rollup(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	for i := 0; i < 3; i++ {
		if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "description": "x"}); w.Code != http.StatusCreated {
			t.Fatalf("subtask %d: %d", i, w.Code)
		}
	}
	// Move one child to a terminal label so done=1.
	children, _ := q.ListSubtasks(context.Background(), &parent.ID)
	if _, err := q.UpdateTaskLabel(context.Background(), gen.UpdateTaskLabelParams{Label: "done", ID: children[0].ID}); err != nil {
		t.Fatalf("move child: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/"+parent.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var pr struct {
		SubtaskTotal int `json:"subtask_total"`
		SubtaskDone  int `json:"subtask_done"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &pr)
	if pr.SubtaskTotal != 3 || pr.SubtaskDone != 1 {
		t.Fatalf("rollup total/done = %d/%d, want 3/1", pr.SubtaskTotal, pr.SubtaskDone)
	}

	// parent_id filter returns the three children.
	req = httptest.NewRequest(http.MethodGet, "/tasks?parent_id="+parent.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var fam []struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &fam)
	if len(fam) != 3 {
		t.Fatalf("parent_id filter returned %d, want 3", len(fam))
	}
}

// TestSubtasks_OptInGate verifies a run whose config has subtasks disabled is
// refused, and an enabled one is allowed, when the parent has an active run.
func TestSubtasks_OptInGate(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	ctx := context.Background()
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")

	// Config with subtasks disabled + an active run pointing at it.
	pcOff, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{
		ID: uuid.NewString(), Name: "off-provider", Provider: "claude", Model: "sonnet", Env: "{}",
	})
	if err != nil {
		t.Fatalf("create provider config: %v", err)
	}
	cfgOff, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "off", ProviderConfigID: pcOff.ID,
		Labels: `["plan"]`, MaxTokens: 8192, TimeoutSecs: 600, MaxTurns: 50,
		EnabledPlugins: "[]", EnabledMcpServers: "[]", CommandAllowlist: "[]", CommandDenylist: "[]",
		MaxRetries: 3, RetryBackoffSecs: 30, ResumeSessions: 1, SubtasksEnabled: 0, MaxSubtasks: 10,
	})
	if err != nil {
		t.Fatalf("create cfg: %v", err)
	}
	runID := uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: parent.ID, AgentConfigID: &cfgOff.ID}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: parent.ID}); err != nil {
		t.Fatalf("set active run: %v", err)
	}

	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "description": "x"}); w.Code != http.StatusForbidden {
		t.Fatalf("subtasks-disabled config should be 403, got %d %s", w.Code, w.Body.String())
	}

	// Flip the config on; now allowed.
	if _, err := q.UpdateAgentConfig(ctx, gen.UpdateAgentConfigParams{
		Name: cfgOff.Name, ProviderConfigID: cfgOff.ProviderConfigID, SystemPrompt: cfgOff.SystemPrompt,
		Labels: cfgOff.Labels, MaxTokens: cfgOff.MaxTokens, TimeoutSecs: cfgOff.TimeoutSecs,
		MaxTurns: cfgOff.MaxTurns, Enabled: 1, EnabledPlugins: cfgOff.EnabledPlugins, EnabledMcpServers: cfgOff.EnabledMcpServers,
		CommandAllowlist: cfgOff.CommandAllowlist, CommandDenylist: cfgOff.CommandDenylist,
		MaxRetries: cfgOff.MaxRetries, RetryBackoffSecs: cfgOff.RetryBackoffSecs, ResumeSessions: cfgOff.ResumeSessions,
		SubtasksEnabled: 1, MaxSubtasks: 2, ID: cfgOff.ID,
	}); err != nil {
		t.Fatalf("update cfg: %v", err)
	}
	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c1", "description": "x"}); w.Code != http.StatusCreated {
		t.Fatalf("subtasks-enabled should allow, got %d %s", w.Code, w.Body.String())
	}
	// Cap is 2: create one more (ok), then the third should hit the cap.
	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c2", "description": "x"}); w.Code != http.StatusCreated {
		t.Fatalf("second subtask should be allowed, got %d", w.Code)
	}
	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c3", "description": "x"}); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("third subtask should hit cap (422), got %d %s", w.Code, w.Body.String())
	}
}

func createScoped(t *testing.T, r http.Handler, parentID, title string, scope []string) (id string, body map[string]any) {
	t.Helper()
	b := map[string]any{"title": title}
	if scope != nil {
		b["file_scope"] = scope
	}
	w := postSubtask(t, r, parentID, b)
	if w.Code != http.StatusCreated {
		t.Fatalf("create %s: %d %s", title, w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body["id"].(string), body
}

func TestSubtasks_FileScopeStoredAndReturned(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")

	id, body := createScoped(t, r, parent.ID, "A", []string{" backend/a/** ", "docs/"})
	if fs, _ := body["file_scope"].([]any); len(fs) != 2 || fs[0] != "backend/a/**" {
		t.Fatalf("POST response file_scope = %v", body["file_scope"])
	}
	if od, ok := body["overlap_dependencies"].([]any); !ok || len(od) != 0 {
		t.Fatalf("overlap_dependencies = %v", body["overlap_dependencies"])
	}

	for _, path := range []string{"/tasks/" + id, "/tasks?parent_id=" + parent.ID} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if !strings.Contains(w.Body.String(), `"file_scope":["backend/a/**","docs/"]`) {
			t.Fatalf("GET %s missing file_scope: %s", path, w.Body.String())
		}
	}
}

func TestSubtasks_InvalidFileScope(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	for _, sc := range [][]string{{"/abs"}, {"../x"}, {"a\nb"}} {
		w := postSubtask(t, r, parent.ID, map[string]any{"title": "x", "file_scope": sc})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("scope %v: got %d", sc, w.Code)
		}
	}
}

func TestSubtasks_OverlapCreatesDependency(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	ctx := context.Background()
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")

	a, _ := createScoped(t, r, parent.ID, "A", []string{"backend/a/**"})
	b, _ := createScoped(t, r, parent.ID, "B", []string{"frontend/**"}) // disjoint
	c, cBody := createScoped(t, r, parent.ID, "C", []string{"backend/a/sub/**"})
	d, _ := createScoped(t, r, parent.ID, "D", nil) // unscoped: no check

	od, _ := cBody["overlap_dependencies"].([]any)
	if len(od) != 1 || od[0].(map[string]any)["task_id"] != a {
		t.Fatalf("overlap_dependencies = %v", od)
	}
	blockers := func(id string) []string {
		rows, _ := q.ListTaskBlockers(ctx, id)
		var out []string
		for _, r := range rows {
			out = append(out, r.TaskID)
		}
		return out
	}
	if got := blockers(c); len(got) != 1 || got[0] != a {
		t.Fatalf("C blockers = %v, want [A]", got)
	}
	if got := blockers(b); len(got) != 0 {
		t.Fatalf("B should have no sibling edge: %v", got)
	}
	if got := blockers(d); len(got) != 0 {
		t.Fatalf("D should have no sibling edge: %v", got)
	}

	// A finished sibling no longer serializes.
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: "done", ID: a}); err != nil {
		t.Fatal(err)
	}
	e, _ := createScoped(t, r, parent.ID, "E", []string{"backend/a/**"})
	for _, id := range blockers(e) {
		if id == a {
			t.Fatalf("terminal sibling should not become a blocker")
		}
	}
}

func TestSubtasks_NewChildResetsIntegration(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	ctx := context.Background()
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	if err := q.SetTaskIntegrationStatus(ctx, gen.SetTaskIntegrationStatusParams{TaskID: parent.ID, IntegrationStatus: "done"}); err != nil {
		t.Fatal(err)
	}
	createScoped(t, r, parent.ID, "A", nil)
	fo, err := q.GetTaskFanout(ctx, parent.ID)
	if err != nil || fo.IntegrationStatus != "" {
		t.Fatalf("integration status = %q (%v)", fo.IntegrationStatus, err)
	}
}

// TestSubtasks_BlockerSatisfiedOnlyAfterMerge: a terminal subtask whose
// merge-back hasn't finished does not satisfy edges (closes the dispatch race).
func TestSubtasks_BlockerSatisfiedOnlyAfterMerge(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	ctx := context.Background()
	parent := mkTask(t, q, wfID, repoID, "parent", "work")
	childID, _ := createScoped(t, r, parent.ID, "A", nil)
	sib := mkTask(t, q, wfID, repoID, "sibling", "work")
	if err := q.CreateTaskDependency(ctx, gen.CreateTaskDependencyParams{TaskID: sib.ID, DependsOnTaskID: childID}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.UpdateTaskLabel(ctx, gen.UpdateTaskLabelParams{Label: "done", ID: childID}); err != nil {
		t.Fatal(err)
	}
	check := func(wantEligible bool, why string) {
		t.Helper()
		pickup, _ := q.ListAgentPickupTasks(ctx)
		if containsTask(pickup, parent.ID) != wantEligible || containsTask(pickup, sib.ID) != wantEligible {
			t.Fatalf("%s: eligible should be %v", why, wantEligible)
		}
		un, _ := q.ListUnsatisfiedBlockersForTasks(ctx, []string{parent.ID, sib.ID})
		if (len(un) == 0) != wantEligible {
			t.Fatalf("%s: unsatisfied blockers = %d", why, len(un))
		}
		cnt, _ := q.ListTaskDependencyCountsForTasks(ctx, []string{parent.ID, sib.ID})
		for _, c := range cnt {
			if (c.BlockedByCount == 0) != wantEligible && c.TaskID != "" && (c.TaskID == parent.ID || c.TaskID == sib.ID) {
				t.Fatalf("%s: blocked_by_count %d for %s", why, c.BlockedByCount, c.TaskID)
			}
		}
	}
	check(false, "terminal, merge_status empty")
	_, _ = q.SetTaskMergeStatus(ctx, gen.SetTaskMergeStatusParams{MergeStatus: "pending", ID: childID})
	check(false, "merge pending")
	_, _ = q.SetTaskMergeStatus(ctx, gen.SetTaskMergeStatusParams{MergeStatus: "merge_conflict", ID: childID})
	check(true, "merge_conflict")
	_, _ = q.SetTaskMergeStatus(ctx, gen.SetTaskMergeStatusParams{MergeStatus: "merged", ID: childID})
	check(true, "merged")
	_, _ = q.SetTaskMergeStatus(ctx, gen.SetTaskMergeStatusParams{MergeStatus: "", ID: childID})
	if _, err := q.SetTaskArchived(ctx, gen.SetTaskArchivedParams{Archived: 1, ID: childID}); err != nil {
		t.Fatal(err)
	}
	check(true, "archived")
}

// --- repo_id / cross-repo subtasks (ADR 0041) ---

func seedSecondRepo(t *testing.T, q *gen.Queries) string {
	t.Helper()
	wfs, _ := q.ListWorkflows(context.Background())
	other := wfs[len(wfs)-1].ID
	id := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{ID: id, Name: "other", Path: t.TempDir(), WorkflowID: &other}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return id
}

// activeRunWithFlags gives the parent an in-flight run whose config has the
// given subtasks / cross-repo flags.
func activeRunWithFlags(t *testing.T, q *gen.Queries, parentID string, subtasks, crossRepo int64) {
	t.Helper()
	ctx := context.Background()
	pc, err := q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: uuid.NewString(), Name: "p-" + uuid.NewString()[:6], Provider: "claude", Model: "sonnet", Env: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "c-" + uuid.NewString()[:6], ProviderConfigID: pc.ID,
		Labels: `["plan"]`, MaxTokens: 8192, TimeoutSecs: 600, MaxTurns: 50,
		EnabledPlugins: "[]", EnabledMcpServers: "[]", CommandAllowlist: "[]", CommandDenylist: "[]",
		MaxRetries: 3, RetryBackoffSecs: 30, ResumeSessions: 1, SubtasksEnabled: subtasks, MaxSubtasks: 2,
		CrossRepoSubtasksEnabled: crossRepo,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := uuid.NewString()
	if _, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: runID, TaskID: parentID, AgentConfigID: &cfg.ID}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{CurrentAgentRunID: &runID, ActiveAgentRunID: &runID, ID: parentID}); err != nil {
		t.Fatal(err)
	}
}

func TestSubtasks_RepoIDOmittedUnchanged(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "description": "x"})
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var c struct {
		RepoID string `json:"repo_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	if c.RepoID != repoID {
		t.Fatalf("child repo_id=%q want parent's %q", c.RepoID, repoID)
	}
}

func TestSubtasks_UnknownRepoRejected(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "description": "x", "repo_id": "nope"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "repo_id does not reference an existing repo") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if n, _ := q.CountSubtasks(context.Background(), &parent.ID); n != 0 {
		t.Fatalf("subtasks created: %d", n)
	}
}

func TestSubtasks_CrossRepoFlagOffRejected(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	other := seedSecondRepo(t, q)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	activeRunWithFlags(t, q, parent.ID, 1, 0)

	w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "description": "x", "repo_id": other})
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "this agent config is not permitted to create cross-repo subtasks") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if n, _ := q.CountSubtasks(context.Background(), &parent.ID); n != 0 {
		t.Fatalf("subtasks created: %d", n)
	}
	// Explicit repo_id equal to the parent's repo is a plain home child.
	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "description": "x", "repo_id": repoID}); w.Code != http.StatusCreated {
		t.Fatalf("same-repo repo_id should be allowed, got %d %s", w.Code, w.Body.String())
	}
}

func TestSubtasks_CrossRepoFlagOn(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	other := seedSecondRepo(t, q)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	activeRunWithFlags(t, q, parent.ID, 1, 1)

	w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "description": "x", "repo_id": other})
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var c struct {
		ID, RepoID, Label, WorkflowID string
	}
	var raw map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	c.ID, _ = raw["id"].(string)
	c.RepoID, _ = raw["repo_id"].(string)
	c.Label, _ = raw["label"].(string)
	c.WorkflowID, _ = raw["workflow_id"].(string)
	if c.RepoID != other {
		t.Fatalf("child repo=%q want %q", c.RepoID, other)
	}
	if c.WorkflowID != parent.WorkflowID {
		t.Fatalf("child workflow=%q want parent's %q", c.WorkflowID, parent.WorkflowID)
	}
	if c.Label != "not_ready" {
		t.Fatalf("label=%q", c.Label)
	}
	_, derr := q.GetTaskDependency(context.Background(), gen.GetTaskDependencyParams{TaskID: parent.ID, DependsOnTaskID: c.ID})
	if derr != nil {
		t.Fatalf("expected the parent→child edge: %v", derr)
	}
	// Cap (2) still applies to foreign children.
	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c2", "description": "x", "repo_id": other}); w.Code != http.StatusCreated {
		t.Fatalf("second: %d", w.Code)
	}
	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c3", "description": "x", "repo_id": other}); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("third should hit cap, got %d", w.Code)
	}
}

func TestSubtasks_RepoIDDepthLimit(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	other := seedSecondRepo(t, q)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	_, body := createScoped(t, r, parent.ID, "A", nil)
	if w := postSubtask(t, r, body["id"].(string), map[string]any{"title": "g", "description": "x", "repo_id": other}); w.Code != http.StatusBadRequest {
		t.Fatalf("grandchild with repo_id should hit depth limit, got %d", w.Code)
	}
}

func TestSubtasks_OverlapOnlySameRepo(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	other := seedSecondRepo(t, q)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")

	post := func(repo string) map[string]any {
		b := map[string]any{"title": "c", "description": "x", "file_scope": []string{"src/**"}}
		if repo != "" {
			b["repo_id"] = repo
		}
		w := postSubtask(t, r, parent.ID, b)
		if w.Code != http.StatusCreated {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	post("") // home sibling with src/**
	foreign := post(other)
	if od, _ := foreign["overlap_dependencies"].([]any); len(od) != 0 {
		t.Fatalf("foreign child must not serialize behind a home sibling: %v", od)
	}
	home2 := post("")
	if od, _ := home2["overlap_dependencies"].([]any); len(od) != 1 {
		t.Fatalf("same-repo sibling should still overlap-serialize (and skip the foreign one): %v", od)
	}
}

// A repo the task's creator named is allowed for cross-repo create_subtask
// even when the agent config's flag is off (ADR 0041 stage 4).
func TestSubtasks_NamedRepoAllowedWithFlagOff(t *testing.T) {
	r, _, q, wfID, repoID := setupSubtaskRouter(t)
	named := seedSecondRepo(t, q)
	unnamed := seedSecondRepo(t, q)
	parent := mkTask(t, q, wfID, repoID, "parent", "plan")
	activeRunWithFlags(t, q, parent.ID, 1, 0)
	if err := q.AddTaskRelatedRepo(context.Background(), gen.AddTaskRelatedRepoParams{TaskID: parent.ID, RepoID: named}); err != nil {
		t.Fatal(err)
	}
	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "repo_id": named}); w.Code != http.StatusCreated {
		t.Fatalf("named repo: %d %s", w.Code, w.Body.String())
	}
	if w := postSubtask(t, r, parent.ID, map[string]any{"title": "c", "repo_id": unnamed}); w.Code != http.StatusForbidden {
		t.Fatalf("unnamed repo: %d %s", w.Code, w.Body.String())
	}
}
