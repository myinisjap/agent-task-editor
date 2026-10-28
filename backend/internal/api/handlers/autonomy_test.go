package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

func newAutonomyTask(t *testing.T) (http.Handler, *gen.Queries, gen.Task) {
	r, q, wfID, repoID := setupTaskRouter(t)
	task, err := q.CreateTask(context.Background(), gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "review"})
	if err != nil {
		t.Fatal(err)
	}
	return r, q, task
}

func TestAutonomyDecision_GetAndLabelScope(t *testing.T) {
	r, q, task := newAutonomyTask(t)
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/autonomy-decision", nil))
		return w
	}
	if w := get(); w.Code != http.StatusNotFound {
		t.Fatalf("no row: %d", w.Code)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/tasks/nope/autonomy-decision", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown task: %d", w.Code)
	}
	ctx := context.Background()
	mk := func(label, decision, reasons string) {
		if err := q.CreateAutonomyDecision(ctx, gen.CreateAutonomyDecisionParams{
			ID: uuid.NewString(), TaskID: task.ID, WorkflowID: task.WorkflowID, Label: label,
			Mode: "auto", Decision: decision, Reasons: reasons,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("work", "approve", `[{"code":"auto_policy","detail":"x"}]`) // previous label: ignored
	if w := get(); w.Code != http.StatusNotFound {
		t.Fatalf("other-label row leaked: %d", w.Code)
	}
	mk("review", "ask", `[{"code":"always_ask","detail":"d"}]`)
	w = get()
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
	var got struct {
		Decision string `json:"decision"`
		Label    string `json:"label"`
		RunID    *string
		Reasons  []struct{ Code, Detail string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Decision != "ask" || got.Label != "review" || len(got.Reasons) != 1 || got.Reasons[0].Code != "always_ask" {
		t.Fatalf("body: %s", w.Body)
	}
}

func TestAlwaysAsk_Toggle(t *testing.T) {
	r, _, task := newAutonomyTask(t)
	patch := func(id string, v bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "/tasks/"+id+"/always-ask", jsonBody(t, map[string]bool{"always_ask": v}))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	w := patch(task.ID, true)
	var resp struct {
		AlwaysAsk bool `json:"always_ask"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || !resp.AlwaysAsk {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	w = patch(task.ID, false)
	resp.AlwaysAsk = true
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || resp.AlwaysAsk {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	if w := patch("nope", true); w.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", w.Code)
	}
}

const autonomyWF = `
name: auto-wf
labels:
  - name: work
    color: "#111"
    sort_order: 0
  - name: review
    color: "#222"
    sort_order: 1
%s
  - name: done
    color: "#333"
    sort_order: 2
    is_terminal: true
transitions:
  - {from: work, to: review, trigger: agent, path: success}
  - {from: review, to: done, trigger: human, path: success}
  - {from: review, to: work, trigger: human, path: failure}
`

func importAutonomy(r http.Handler, block string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/workflows/import", strings.NewReader(strings.Replace(autonomyWF, "%s", block, 1)))
	req.Header.Set("Content-Type", "application/yaml")
	r.ServeHTTP(w, req)
	return w
}

func TestWorkflowAutonomy_YAMLRoundTrip(t *testing.T) {
	r, _ := setupWorkflowRouter(t)
	w := importAutonomy(r, "    autonomy:\n      mode: on_flags\n      ask_on: [migration, ci_config]\n      hold: 10m\n      max_cost_usd: 5")
	if w.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	var wf struct {
		ID     string `json:"id"`
		Labels []struct {
			Name     string `json:"name"`
			Autonomy *struct {
				Mode        string   `json:"mode"`
				AskOn       []string `json:"ask_on"`
				HoldSeconds int      `json:"hold_seconds"`
				MaxCostUSD  *float64 `json:"max_cost_usd"`
			} `json:"autonomy"`
		} `json:"labels"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &wf)
	for _, l := range wf.Labels {
		if l.Name == "review" {
			if l.Autonomy == nil || l.Autonomy.Mode != "on_flags" || l.Autonomy.HoldSeconds != 600 || *l.Autonomy.MaxCostUSD != 5 || len(l.Autonomy.AskOn) != 2 {
				t.Fatalf("review autonomy = %+v", l.Autonomy)
			}
		} else if l.Autonomy != nil {
			t.Fatalf("%s should have null autonomy", l.Name)
		}
	}
	if strings.Contains(w.Body.String(), "autonomy_mode") {
		t.Fatalf("raw columns leaked: %s", w.Body)
	}

	export := func() string {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/workflows/"+wf.ID+"/export.yaml", nil))
		return w.Body.String()
	}
	first := export()
	for _, want := range []string{"autonomy:", "mode: on_flags", "hold: 10m", "max_cost_usd: 5", "- migration"} {
		if !strings.Contains(first, want) {
			t.Fatalf("export missing %q:\n%s", want, first)
		}
	}
	if strings.Count(first, "autonomy:") != 1 {
		t.Fatalf("only the gate should export autonomy:\n%s", first)
	}
	// PUT the export back unchanged: block must survive.
	put := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/workflows/"+wf.ID+"/yaml", strings.NewReader(first))
	req.Header.Set("Content-Type", "application/yaml")
	r.ServeHTTP(put, req)
	if put.Code != http.StatusOK {
		t.Fatalf("put: %d %s", put.Code, put.Body)
	}
	if second := export(); second != first {
		t.Fatalf("round trip differs:\n%s\n---\n%s", first, second)
	}

}

func TestWorkflowAutonomy_AbsentAndAsk(t *testing.T) {
	r, _ := setupWorkflowRouter(t)
	w := importAutonomy(r, "")
	if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), `"mode":`) {
		t.Fatalf("absent: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"autonomy":null`) {
		t.Fatalf("expected null autonomy: %s", w.Body)
	}
}

func TestWorkflowAutonomy_Errors(t *testing.T) {
	r, _ := setupWorkflowRouter(t)
	cases := map[string]struct{ block, want string }{
		"bad mode":   {"    autonomy:\n      mode: yolo", "invalid autonomy mode"},
		"bad flag":   {"    autonomy:\n      mode: auto\n      ask_on: [nope]", `unknown ask_on flag`},
		"missing":    {"    autonomy:\n      hold: 5m", "autonomy.mode is required"},
		"neg hold":   {"    autonomy:\n      mode: auto\n      hold: -5m", "negative"},
		"neg cost":   {"    autonomy:\n      mode: auto\n      max_cost_usd: -1", "max_cost_usd"},
		"bad hold":   {"    autonomy:\n      mode: auto\n      hold: soon", "invalid hold"},
		"not a gate": {"    agent_ignore: true\n    autonomy:\n      mode: auto", "only allowed on human-gate"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := importAutonomy(r, tc.block)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("got %d %s, want 400 containing %q", w.Code, w.Body, tc.want)
			}
		})
	}
	// autonomy on a non-gate label (agent exits)
	body := strings.Replace(autonomyWF, "%s", "", 1)
	body = strings.Replace(body, "  - name: work\n    color: \"#111\"\n    sort_order: 0\n", "  - name: work\n    color: \"#111\"\n    sort_order: 0\n    autonomy:\n      mode: auto\n", 1)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/workflows/import", strings.NewReader(body))
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `label \"work\"`) {
		t.Fatalf("work: %d %s", w.Code, w.Body)
	}
	// explicit ask is allowed anywhere
	body = strings.Replace(body, "mode: auto", "mode: ask", 1)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/workflows/import", strings.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("explicit ask: %d %s", w.Code, w.Body)
	}
}

func TestWorkflowAutonomy_JSONErrors(t *testing.T) {
	r, q := setupWorkflowRouter(t)
	wfs, _ := q.ListWorkflows(context.Background())
	put := func(autonomy string) *httptest.ResponseRecorder {
		body := `{"labels":[{"name":"work","color":"#111","sort_order":0},{"name":"review","color":"#222","sort_order":1,"autonomy":` + autonomy + `},{"name":"done","color":"#333","sort_order":2,"is_terminal":true}],
"transitions":[{"from_label":"work","to_label":"review","trigger_type":"agent"},{"from_label":"review","to_label":"done","trigger_type":"human"}]}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/workflows/"+wfs[0].ID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	for _, bad := range []string{`{"mode":"x"}`, `{"mode":"auto","ask_on":["nope"]}`, `{"mode":"auto","hold_seconds":-1}`, `{"ask_on":[]}`} {
		if w := put(bad); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", bad, w.Code, w.Body)
		}
	}
	if w := put(`{"mode":"auto","ask_on":["migration"],"hold_seconds":30,"max_cost_usd":2.5}`); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"hold_seconds":30`) {
		t.Fatalf("ok: %d %s", w.Code, w.Body)
	}
	if w := put(`null`); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"mode":"auto"`) {
		t.Fatalf("null: %d %s", w.Code, w.Body)
	}
}

type recordingPub struct{ events []string }

func (p *recordingPub) Publish(t string, _ any) { p.events = append(p.events, t) }

type autonomyEnv struct {
	r      http.Handler
	q      *gen.Queries
	pub    *recordingPub
	engine *workflow.Engine
	wfID   string
	repoID string
	db     *storage.DB
}

func newAutonomyEnv(t *testing.T) *autonomyEnv {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	pub := &recordingPub{}
	engine := workflow.New(db.SQL(), pub)
	wfs, _ := q.ListWorkflows(context.Background())
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfs[0].ID}); err != nil {
		t.Fatal(err)
	}
	h := handlers.NewTasksHandler(q, engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil, nil)
	h.SetAutonomyPublisher(pub)
	r := chi.NewRouter()
	r.Post("/tasks/{id}/autonomy/hold", h.HoldAutonomy)
	r.Post("/tasks/{id}/autonomy/undo", h.UndoAutonomy)
	return &autonomyEnv{r: r, q: q, pub: pub, engine: engine, wfID: wfs[0].ID, repoID: repoID, db: db}
}

func (e *autonomyEnv) post(path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
	return w
}

func (e *autonomyEnv) task(t *testing.T, label string) gen.Task {
	t.Helper()
	task, err := e.q.CreateTask(context.Background(), gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: e.wfID, RepoID: e.repoID, Label: label})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (e *autonomyEnv) pending(t *testing.T, task gen.Task) string {
	t.Helper()
	out, hold, due, note := "pending", int64(600), time.Now().Add(time.Hour).UTC(), "Auto-approved: x"
	id := uuid.NewString()
	if err := e.q.CreateAutonomyDecision(context.Background(), gen.CreateAutonomyDecisionParams{
		ID: id, TaskID: task.ID, WorkflowID: task.WorkflowID, Label: task.Label, Mode: "auto", Decision: "approve",
		Reasons: "[]", Outcome: &out, HoldSeconds: &hold, DueAt: &due, Note: &note,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestHoldAutonomy(t *testing.T) {
	e := newAutonomyEnv(t)
	if w := e.post("/tasks/nope/autonomy/hold"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", w.Code)
	}
	task := e.task(t, "review")
	if w := e.post("/tasks/" + task.ID + "/autonomy/hold"); w.Code != http.StatusConflict {
		t.Fatalf("nothing pending: %d", w.Code)
	}
	id := e.pending(t, task)
	w := e.post("/tasks/" + task.ID + "/autonomy/hold")
	if w.Code != http.StatusOK {
		t.Fatalf("hold: %d %s", w.Code, w.Body)
	}
	d, _ := e.q.GetAutonomyDecision(context.Background(), id)
	if d.Outcome == nil || *d.Outcome != "held" {
		t.Fatalf("outcome = %v", d.Outcome)
	}
	found := false
	for _, ev := range e.pub.events {
		if ev == "task.autonomy_held" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no held event: %v", e.pub.events)
	}
	if w := e.post("/tasks/" + task.ID + "/autonomy/hold"); w.Code != http.StatusConflict {
		t.Fatalf("second hold: %d", w.Code)
	}
}

func TestUndoAutonomy(t *testing.T) {
	e := newAutonomyEnv(t)
	ctx := context.Background()
	task := e.task(t, "review")
	id := e.pending(t, task)
	// Approve as the policy would.
	if _, err := e.q.ClaimAutonomyApproval(ctx, gen.ClaimAutonomyApprovalParams{ID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.engine.Approve(ctx, task.ID, workflow.ApproveOptions{Actor: "policy", Note: "Auto-approved: x", ExpectedFrom: "review"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.SQL().Exec("INSERT INTO task_git (task_id, pr_url) VALUES (?, 'https://example.test/pr/1') ON CONFLICT(task_id) DO UPDATE SET pr_url = excluded.pr_url", task.ID); err != nil {
		t.Fatal(err)
	}
	w := e.post("/tasks/" + task.ID + "/autonomy/undo")
	if w.Code != http.StatusOK {
		t.Fatalf("undo: %d %s", w.Code, w.Body)
	}
	var body struct {
		PRLeftOpen bool   `json:"pr_left_open"`
		Message    string `json:"message"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !body.PRLeftOpen || !strings.Contains(body.Message, "https://example.test/pr/1") {
		t.Fatalf("body = %+v", body)
	}
	after, _ := e.q.GetTask(ctx, task.ID)
	if after.Label != "review" {
		t.Fatalf("label = %s", after.Label)
	}
	hist, _ := e.q.ListTaskLabelHistory(ctx, task.ID)
	last := hist[len(hist)-1]
	if last.Note == nil || *last.Note != "Undo auto-approval" || last.ToLabel != "review" {
		t.Fatalf("history = %+v", last)
	}
	esc, _ := e.q.ListAutonomyEscapesForTask(ctx, task.ID)
	if len(esc) != 1 || esc[0].Source != "undo" || esc[0].Label != "review" {
		t.Fatalf("escapes = %+v", esc)
	}
	d, _ := e.q.GetAutonomyDecision(ctx, id)
	if d.Outcome == nil || *d.Outcome != "undone" {
		t.Fatalf("outcome = %v", d.Outcome)
	}
	// Now on the gate: the last gate exit is still policy's, but already there.
	if w := e.post("/tasks/" + task.ID + "/autonomy/undo"); w.Code != http.StatusConflict {
		t.Fatalf("repeat undo: %d", w.Code)
	}
}

func TestUndoAutonomy_Refusals(t *testing.T) {
	e := newAutonomyEnv(t)
	ctx := context.Background()
	if w := e.post("/tasks/nope/autonomy/undo"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", w.Code)
	}
	// Last gate exit by a human.
	human := e.task(t, "review")
	if _, err := e.engine.Approve(ctx, human.ID, workflow.ApproveOptions{Actor: "alice"}); err != nil {
		t.Fatal(err)
	}
	if w := e.post("/tasks/" + human.ID + "/autonomy/undo"); w.Code != http.StatusConflict {
		t.Fatalf("human exit: %d", w.Code)
	}
	// Policy exit, then a live run.
	live := e.task(t, "review")
	if _, err := e.engine.Approve(ctx, live.ID, workflow.ApproveOptions{Actor: "policy"}); err != nil {
		t.Fatal(err)
	}
	run, err := e.q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: live.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.q.SetTaskActiveRun(ctx, gen.SetTaskActiveRunParams{CurrentAgentRunID: &run.ID, ActiveAgentRunID: &run.ID, ID: live.ID}); err != nil {
		t.Fatal(err)
	}
	if w := e.post("/tasks/" + live.ID + "/autonomy/undo"); w.Code != http.StatusConflict {
		t.Fatalf("live run: %d %s", w.Code, w.Body)
	}
}

func TestUndoAutonomy_TerminalMerged(t *testing.T) {
	e := newAutonomyEnv(t)
	ctx := context.Background()
	task := e.task(t, "review")
	target, err := e.engine.Approve(ctx, task.ID, workflow.ApproveOptions{Actor: "policy"})
	if err != nil {
		t.Fatal(err)
	}
	// Drive the task to a terminal label marked merged.
	if _, err := e.db.SQL().Exec("UPDATE workflow_labels SET is_terminal = 1 WHERE workflow_id = ? AND name = ?", e.wfID, target); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.SQL().Exec("INSERT INTO task_git (task_id, git_state) VALUES (?, 'pr_merged') ON CONFLICT(task_id) DO UPDATE SET git_state = excluded.git_state", task.ID); err != nil {
		t.Fatal(err)
	}
	if w := e.post("/tasks/" + task.ID + "/autonomy/undo"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "merged") {
		t.Fatalf("merged: %d %s", w.Code, w.Body)
	}
}

func TestNeedsMe_AutoApprovingAndPolicyApprovals(t *testing.T) {
	e := newAutonomyEnv(t)
	ctx := context.Background()
	pend := e.task(t, "review")
	e.pending(t, pend)
	done := e.task(t, "review")
	id := e.pending(t, done)
	if _, err := e.q.ClaimAutonomyApproval(ctx, gen.ClaimAutonomyApprovalParams{OutcomeAt: ptrTime(time.Now().UTC()), ID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.engine.Approve(ctx, done.ID, workflow.ApproveOptions{Actor: "policy", Note: "n"}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handlers.NewNeedsMeHandler(e.q).List(w, httptest.NewRequest(http.MethodGet, "/needs-me", nil))
	var body struct {
		Items []struct {
			TaskID string  `json:"task_id"`
			Reason string  `json:"reason"`
			DueAt  *string `json:"due_at"`
		} `json:"items"`
		Approved []struct {
			TaskID   string `json:"task_id"`
			Undoable bool   `json:"undoable"`
			Changed  *struct {
				Files int `json:"files"`
			} `json:"changed"` // optional: only present when a brief cache is wired
		} `json:"approved_by_policy"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].TaskID != pend.ID || body.Items[0].Reason != "auto_approving" || body.Items[0].DueAt == nil {
		t.Fatalf("items = %+v", body.Items)
	}
	if len(body.Approved) != 1 || body.Approved[0].TaskID != done.ID || !body.Approved[0].Undoable {
		t.Fatalf("approved = %+v", body.Approved)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
