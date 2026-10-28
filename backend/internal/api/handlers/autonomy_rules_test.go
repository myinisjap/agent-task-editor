package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/escape"
	"github.com/myinisjap/agent-task-editor/backend/internal/reviewbrief"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/workflow"
)

type rulesEnv struct {
	*autonomyEnv
	rr http.Handler
}

func newRulesEnv(t *testing.T) *rulesEnv {
	t.Helper()
	e := newAutonomyEnv(t)
	h := handlers.NewTasksHandler(e.q, e.engine, t.TempDir(), &fakeCanceller{found: map[string]bool{}}, nil, nil)
	h.SetAutonomyPublisher(e.pub)
	h.SetAutonomyDeps(e.db.SQL(), escape.New(e.q, e.pub), reviewbrief.NewCache())
	rh := handlers.NewAutonomyRulesHandler(e.q)
	sh := handlers.NewAutonomySettingsHandler(e.q)
	r := chi.NewRouter()
	r.Get("/tasks/{id}/autonomy/suggestions", h.AutonomySuggestions)
	r.Post("/tasks/{id}/autonomy/should-have-asked", h.ShouldHaveAsked)
	r.Get("/autonomy/ask-rules", rh.List)
	r.Post("/autonomy/ask-rules", rh.Create)
	r.Patch("/autonomy/ask-rules/{id}", rh.Update)
	r.Delete("/autonomy/ask-rules/{id}", rh.Delete)
	r.Get("/workflows/{id}/labels/{label}/autonomy/history", rh.History)
	r.Get("/settings/autonomy", sh.Get)
	r.Put("/settings/autonomy", sh.Update)
	return &rulesEnv{autonomyEnv: e, rr: r}
}

func (e *rulesEnv) do(method, path string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	w := httptest.NewRecorder()
	e.rr.ServeHTTP(w, httptest.NewRequest(method, path, rd))
	return w
}

// approved makes a task look policy-approved: an approved decision at label.
func (e *rulesEnv) approved(t *testing.T, task gen.Task, label string) string {
	t.Helper()
	id := uuid.NewString()
	out, hold := "approved", int64(0)
	if err := e.q.CreateAutonomyDecision(context.Background(), gen.CreateAutonomyDecisionParams{
		ID: id, TaskID: task.ID, WorkflowID: task.WorkflowID, Label: label, Mode: "auto", Decision: "approve",
		Reasons: "[]", Outcome: &out, HoldSeconds: &hold,
	}); err != nil {
		t.Fatal(err)
	}
	e.exec(t, `UPDATE autonomy_decisions SET outcome_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return id
}

func (e *rulesEnv) setMode(t *testing.T, label, mode string) {
	t.Helper()
	if _, err := e.db.SQL().Exec(`UPDATE workflow_labels SET autonomy_mode=?, autonomy_ask_on='[]' WHERE workflow_id=? AND name=?`, mode, e.wfID, label); err != nil {
		t.Fatal(err)
	}
}

func (e *rulesEnv) mode(t *testing.T, label string) string {
	t.Helper()
	var m string
	if err := e.db.SQL().QueryRow(`SELECT autonomy_mode FROM workflow_labels WHERE workflow_id=? AND name=?`, e.wfID, label).Scan(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAskRules_CRUD(t *testing.T) {
	e := newRulesEnv(t)
	create := func(body map[string]any) *httptest.ResponseRecorder {
		body["workflow_id"] = e.wfID
		return e.do("POST", "/autonomy/ask-rules", body)
	}
	for name, body := range map[string]map[string]any{
		"no condition": {},
		"bad glob":     {"path_globs": []string{strings.Repeat("a", 300)}},
		"unknown lbl":  {"task_type": "bug", "label": "nope"},
		"unknown repo": {"task_type": "bug", "repo_id": "nope"},
		"min zero":     {"min_changed_lines": 0},
	} {
		if w := create(body); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := e.do("POST", "/autonomy/ask-rules", map[string]any{"workflow_id": "nope", "task_type": "bug"}); w.Code != 400 {
		t.Fatalf("unknown wf: %d", w.Code)
	}
	w := create(map[string]any{"path_globs": []string{"src/db/**", "*.sql"}, "label": "review", "repo_id": e.repoID})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var a struct {
		ID, Summary string
		PathGlobs   []string `json:"path_globs"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	if a.ID == "" || len(a.PathGlobs) != 2 || a.Summary == "" {
		t.Fatalf("rule = %+v", a)
	}
	// An all-gates / all-repos rule and one for another label.
	if w := create(map[string]any{"task_type": "bug"}); w.Code != 201 {
		t.Fatal(w.Body)
	}
	if w := create(map[string]any{"min_changed_lines": 100, "label": "work"}); w.Code != 201 {
		t.Fatal(w.Body)
	}
	count := func(q string) int {
		w := e.do("GET", "/autonomy/ask-rules"+q, "")
		var out []map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return len(out)
	}
	if n := count(""); n != 3 {
		t.Fatalf("all = %d", n)
	}
	if n := count("?workflow_id=" + e.wfID + "&label=review"); n != 2 { // review rule + all-gates rule
		t.Fatalf("label filter = %d", n)
	}
	if n := count("?repo_id=" + e.repoID); n != 3 { // repo rule + two all-repos rules
		t.Fatalf("repo filter = %d", n)
	}
	if n := count("?workflow_id=other"); n != 0 {
		t.Fatalf("other wf = %d", n)
	}

	// PATCH: null clears a field; clearing every condition is rejected.
	if w := e.do("PATCH", "/autonomy/ask-rules/"+a.ID, `{"label":null,"repo_id":null,"task_type":"bug"}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"label":null`) || !strings.Contains(w.Body.String(), `"task_type":"bug"`) {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	if w := e.do("PATCH", "/autonomy/ask-rules/"+a.ID, `{"path_globs":[],"task_type":null}`); w.Code != 400 {
		t.Fatalf("patch to empty: %d %s", w.Code, w.Body)
	}
	if w := e.do("PATCH", "/autonomy/ask-rules/nope", `{"task_type":"x"}`); w.Code != 404 {
		t.Fatalf("patch 404: %d", w.Code)
	}
	if w := e.do("DELETE", "/autonomy/ask-rules/"+a.ID, ""); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := e.do("DELETE", "/autonomy/ask-rules/"+a.ID, ""); w.Code != 404 {
		t.Fatalf("delete again: %d", w.Code)
	}
}

func TestAutonomySettings(t *testing.T) {
	e := newRulesEnv(t)
	w := e.do("GET", "/settings/autonomy", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"step_down_escapes":2`) || !strings.Contains(w.Body.String(), `"step_down_window_days":14`) {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{}`, `{"step_down_escapes":-1,"step_down_window_days":14}`, `{"step_down_escapes":101,"step_down_window_days":14}`,
		`{"step_down_escapes":2,"step_down_window_days":0}`, `{"step_down_escapes":2,"step_down_window_days":366}`} {
		if w := e.do("PUT", "/settings/autonomy", body); w.Code != 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
	if w := e.do("PUT", "/settings/autonomy", `{"step_down_escapes":0,"step_down_window_days":30}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"step_down_window_days":30`) {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
}

func TestSuggestions_NoDiff(t *testing.T) {
	e := newRulesEnv(t)
	task := e.task(t, "review")
	e.exec(t, `UPDATE tasks SET type='bug' WHERE id=?`, task.ID)
	if w := e.do("GET", "/tasks/nope/autonomy/suggestions", ""); w.Code != 404 {
		t.Fatalf("404: %d", w.Code)
	}
	w := e.do("GET", "/tasks/"+task.ID+"/autonomy/suggestions", "")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var s struct {
		DiffAvailable  bool              `json:"diff_available"`
		PolicyApproved bool              `json:"policy_approved"`
		PathGlobs      []json.RawMessage `json:"path_globs"`
		TaskType       *string           `json:"task_type"`
		Min            *int              `json:"min_changed_lines"`
		Label          string            `json:"label"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &s)
	if s.DiffAvailable || s.PolicyApproved || s.PathGlobs == nil || len(s.PathGlobs) != 0 || s.Min != nil || s.TaskType == nil || *s.TaskType != "bug" || s.Label != "review" {
		t.Fatalf("suggestions = %s", w.Body)
	}
	e.approved(t, task, "review")
	w = e.do("GET", "/tasks/"+task.ID+"/autonomy/suggestions", "")
	if !strings.Contains(w.Body.String(), `"policy_approved":true`) {
		t.Fatalf("approved: %s", w.Body)
	}
}

func (e *rulesEnv) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.db.SQL().Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

type sha struct {
	Escape struct {
		ID, Source string
		DecisionID *string `json:"decision_id"`
		RuleID     *string `json:"rule_id"`
		Note       *string
	} `json:"escape"`
	Rule *struct {
		ID        string
		Label     *string  `json:"label"`
		RepoID    *string  `json:"repo_id"`
		PathGlobs []string `json:"path_globs"`
	} `json:"rule"`
	Lesson *struct {
		ID, Body, Status string
		PathGlobs        []string `json:"path_globs"`
		RepoID           *string  `json:"repo_id"`
	} `json:"lesson"`
	SentBack bool   `json:"sent_back"`
	Err      string `json:"send_back_error"`
	StepDown *struct {
		FromMode string `json:"from_mode"`
		ToMode   string `json:"to_mode"`
	} `json:"step_down"`
}

func TestShouldHaveAsked_Refusals(t *testing.T) {
	e := newRulesEnv(t)
	task := e.task(t, "done")
	url := "/tasks/" + task.ID + "/autonomy/should-have-asked"
	if w := e.do("POST", "/tasks/nope/autonomy/should-have-asked", `{}`); w.Code != 404 {
		t.Fatalf("404: %d", w.Code)
	}
	if w := e.do("POST", url, `{}`); w.Code != 409 {
		t.Fatalf("no decision: %d %s", w.Code, w.Body)
	}
	e.approved(t, task, "review")
	for name, body := range map[string]string{
		"bad scope":  `{"label_scope":"x"}`,
		"bad rule":   `{"rule":{}}`,
		"bad glob":   `{"rule":{"path_globs":["` + strings.Repeat("a", 300) + `"]}}`,
		"bad lesson": `{"lesson":{"body":""}}`,
		"bad kind":   `{"lesson":{"body":"x","kind":"zzz"}}`,
	} {
		if w := e.do("POST", url, body); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	var n int
	_ = e.db.SQL().QueryRow(`SELECT COUNT(*) FROM autonomy_escapes`).Scan(&n)
	if n != 0 {
		t.Fatalf("validation failures wrote %d escapes", n)
	}
	// Archived.
	e.exec(t, `UPDATE tasks SET archived=1 WHERE id=?`, task.ID)
	if w := e.do("POST", url, `{}`); w.Code != 409 {
		t.Fatalf("archived: %d", w.Code)
	}
}

func TestShouldHaveAsked_Full(t *testing.T) {
	e := newRulesEnv(t)
	ctx := context.Background()
	task := e.task(t, "done")
	did := e.approved(t, task, "review")
	e.setMode(t, "review", "auto")
	w := e.do("POST", "/tasks/"+task.ID+"/autonomy/should-have-asked",
		`{"rule":{"path_globs":["src/db/**"],"task_type":"bug"},"scope_repo":true,"send_back_note":"too risky","lesson":{"body":"Ask before touching db"}}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var r sha
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	if r.Escape.Source != "should_have_asked" || r.Escape.DecisionID == nil || *r.Escape.DecisionID != did || r.Escape.RuleID == nil {
		t.Fatalf("escape = %+v", r.Escape)
	}
	if r.Rule == nil || r.Rule.Label == nil || *r.Rule.Label != "review" || r.Rule.RepoID == nil || *r.Rule.RepoID != e.repoID {
		t.Fatalf("rule = %+v", r.Rule)
	}
	if r.Lesson == nil || r.Lesson.Status != "active" || len(r.Lesson.PathGlobs) != 1 || r.Lesson.PathGlobs[0] != "src/db/**" || r.Lesson.RepoID == nil {
		t.Fatalf("lesson = %+v", r.Lesson)
	}
	// Done task: not sent back, and that is not an error.
	if r.SentBack || r.Err != "" {
		t.Fatalf("done task sent back: %+v", r)
	}
	if after, _ := e.q.GetTask(ctx, task.ID); after.Label != "done" {
		t.Fatalf("label = %s", after.Label)
	}

	// All gates, any repo; no lesson; sent back from review via the failure path.
	t2 := e.task(t, "review")
	e.approved(t, t2, "review")
	w = e.do("POST", "/tasks/"+t2.ID+"/autonomy/should-have-asked", `{"rule":{"min_changed_lines":50},"label_scope":"all_gates","send_back_note":"redo it"}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r = sha{}
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	if !r.SentBack || r.Rule == nil || r.Rule.Label != nil || r.Rule.RepoID != nil || r.Lesson != nil {
		t.Fatalf("resp = %s", w.Body)
	}
	after, _ := e.q.GetTask(ctx, t2.ID)
	if after.Label == "review" {
		t.Fatalf("task not sent back")
	}
	// Exactly one escape for the task: the automatic rejected_later is not added.
	esc, _ := e.q.ListAutonomyEscapesForTask(ctx, t2.ID)
	if len(esc) != 1 {
		t.Fatalf("escapes = %+v", esc)
	}
	// Two distinct approvals → step-down auto → on_flags.
	if r.StepDown == nil || r.StepDown.FromMode != "auto" || r.StepDown.ToMode != "on_flags" || e.mode(t, "review") != "on_flags" {
		t.Fatalf("step_down = %+v mode=%s", r.StepDown, e.mode(t, "review"))
	}
	var ch int
	_ = e.db.SQL().QueryRow(`SELECT COUNT(*) FROM autonomy_changes WHERE workflow_id=? AND label='review' AND actor='policy'`, e.wfID).Scan(&ch)
	if ch != 1 {
		t.Fatalf("changes = %d", ch)
	}
	// History endpoint.
	hw := e.do("GET", "/workflows/"+e.wfID+"/labels/review/autonomy/history", "")
	var hist struct {
		CurrentMode string `json:"current_mode"`
		Changes     []any  `json:"changes"`
		Escapes     []struct {
			Counted bool   `json:"counted"`
			Title   string `json:"task_title"`
		} `json:"escapes"`
		StepDown struct {
			Counted   int `json:"escapes_counted"`
			Threshold int `json:"threshold"`
		} `json:"step_down"`
	}
	_ = json.Unmarshal(hw.Body.Bytes(), &hist)
	if hw.Code != 200 || hist.CurrentMode != "on_flags" || len(hist.Changes) != 1 || len(hist.Escapes) != 2 || hist.StepDown.Counted != 0 || hist.StepDown.Threshold != 2 {
		t.Fatalf("history: %d %s", hw.Code, hw.Body)
	}
	for _, x := range hist.Escapes {
		if x.Counted || x.Title == "" {
			t.Fatalf("escape = %+v", x)
		}
	}
	if w := e.do("GET", "/workflows/nope/labels/review/autonomy/history", ""); w.Code != 404 {
		t.Fatalf("history 404: %d", w.Code)
	}
}

func TestShouldHaveAsked_LiveRun(t *testing.T) {
	e := newRulesEnv(t)
	task := e.task(t, "review")
	e.approved(t, task, "review")
	runID := uuid.NewString()
	e.exec(t, `INSERT INTO agent_runs (id, task_id, status) VALUES (?, ?, 'running')`, runID, task.ID)
	e.exec(t, `UPDATE tasks SET active_agent_run_id=? WHERE id=?`, runID, task.ID)
	w := e.do("POST", "/tasks/"+task.ID+"/autonomy/should-have-asked", `{"rule":{"task_type":"bug"},"send_back_note":"x"}`)
	if w.Code != 409 {
		t.Fatalf("live run: %d %s", w.Code, w.Body)
	}
	var n int
	_ = e.db.SQL().QueryRow(`SELECT (SELECT COUNT(*) FROM autonomy_escapes)+(SELECT COUNT(*) FROM autonomy_ask_rules)`).Scan(&n)
	if n != 0 {
		t.Fatalf("writes happened: %d", n)
	}
}

func TestWorkflowUpdate_WritesAutonomyChanges(t *testing.T) {
	r, q := setupWorkflowRouter(t)
	ctx := context.Background()
	put := func(id, block string) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/workflows/"+id+"/yaml", strings.NewReader(strings.Replace(autonomyWF, "%s", block, 1)))
		req.Header.Set("Content-Type", "application/yaml")
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("put: %d %s", w.Code, w.Body)
		}
	}
	w := importAutonomy(r, "")
	var wf struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &wf)
	count := func() []gen.AutonomyChange {
		cs, err := q.ListAutonomyChanges(ctx, gen.ListAutonomyChangesParams{WorkflowID: wf.ID, Label: "review", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	put(wf.ID, "") // unchanged
	if n := len(count()); n != 0 {
		t.Fatalf("unchanged save wrote %d", n)
	}
	put(wf.ID, "    autonomy:\n      mode: on_flags\n      ask_on: [migration]\n      hold: 10m")
	cs := count()
	if len(cs) != 1 || cs[0].FromMode != "ask" || cs[0].ToMode != "on_flags" || !strings.Contains(cs[0].Reason, "ask → on_flags") {
		t.Fatalf("changes = %+v", cs)
	}
	put(wf.ID, "    autonomy:\n      mode: on_flags\n      ask_on: [migration]\n      hold: 10m")
	if n := len(count()); n != 1 {
		t.Fatalf("resave wrote %d", n)
	}
	put(wf.ID, "    autonomy:\n      mode: on_flags\n      ask_on: [migration]\n      hold: 20m")
	cs = count()
	if len(cs) != 2 || cs[0].FromMode != "on_flags" || cs[0].ToMode != "on_flags" || !strings.Contains(cs[0].Reason, "hold") {
		t.Fatalf("hold change = %+v", cs)
	}
}

var _ = workflow.TriggerHuman
