package handlers_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy"
	"github.com/myinisjap/agent-task-editor/backend/internal/autonomy/propose"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type stubModel struct{ text string }

func (m stubModel) Complete(context.Context, string) (propose.Completion, error) {
	return propose.Completion{Text: m.text}, nil
}

func newProposalsEnv(t *testing.T, text string) (*rulesEnv, *propose.Service) {
	t.Helper()
	e := newRulesEnv(t)
	svc := propose.New(e.db.SQL(), e.q, func(gen.ProviderConfig) propose.Model { return stubModel{text: text} })
	svc.Facts = func(_ context.Context, task gen.Task, _ time.Time) (autonomy.Facts, bool) {
		return autonomy.Facts{TaskType: task.Type}, false
	}
	ph := handlers.NewAutonomyProposalsHandler(e.q, svc)
	r := chi.NewRouter()
	r.Mount("/", e.rr)
	r.Get("/autonomy/rule-proposals", ph.List)
	r.Post("/autonomy/rule-proposals/{id}/accept", ph.Accept)
	r.Post("/autonomy/rule-proposals/{id}/dismiss", ph.Dismiss)
	r.Post("/workflows/{id}/labels/{label}/autonomy/proposals/run", ph.StartRun)
	r.Get("/workflows/{id}/labels/{label}/autonomy/proposal-runs", ph.ListRuns)
	e.rr = r
	return e, svc
}

func TestAutonomySettings_ProposalFields(t *testing.T) {
	e := newRulesEnv(t)
	if w := e.do("PUT", "/settings/autonomy", `{"step_down_escapes":2,"step_down_window_days":14,"proposal_provider_config_id":"missing"}`); w.Code != 400 {
		t.Fatalf("unknown provider: %d", w.Code)
	}
	if w := e.do("PUT", "/settings/autonomy", `{"step_down_escapes":2,"step_down_window_days":14,"proposal_interval_hours":-1}`); w.Code != 400 {
		t.Fatalf("bad interval: %d", w.Code)
	}
	pcID := uuid.NewString()
	if _, err := e.q.CreateProviderConfig(context.Background(), gen.CreateProviderConfigParams{ID: pcID, Name: "p", Provider: "claude", Model: "m", Env: "{}"}); err != nil {
		t.Fatal(err)
	}
	w := e.do("PUT", "/settings/autonomy", `{"step_down_escapes":2,"step_down_window_days":14,"proposal_provider_config_id":"`+pcID+`","proposal_interval_hours":0}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"proposal_provider_config_id":"`+pcID+`"`) || !strings.Contains(w.Body.String(), `"proposal_interval_hours":0`) {
		t.Fatalf("set: %d %s", w.Code, w.Body)
	}
	// Absent proposal fields keep the stored values.
	w = e.do("PUT", "/settings/autonomy", `{"step_down_escapes":3,"step_down_window_days":14}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), pcID) || !strings.Contains(w.Body.String(), `"step_down_escapes":3`) {
		t.Fatalf("keep: %d %s", w.Code, w.Body)
	}
	w = e.do("PUT", "/settings/autonomy", `{"step_down_escapes":3,"step_down_window_days":14,"proposal_provider_config_id":null}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"proposal_provider_config_id":null`) {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
}

func TestRuleProposals_RunAcceptDismiss(t *testing.T) {
	text := "<<<ATE_PROPOSALS\n" + `{"rules":[{"task_type":"chore","rationale":"chores"},{"task_type":"bug","rationale":"bugs"}],"flag_ideas":[]}` + "\nATE_PROPOSALS>>>"
	e, svc := newProposalsEnv(t, text)
	ctx := context.Background()
	path := "/workflows/" + e.wfID + "/labels/review/autonomy/proposals/run"
	if w := e.do("POST", path, ""); w.Code != 400 || !strings.Contains(w.Body.String(), "no provider config") {
		t.Fatalf("no provider: %d %s", w.Code, w.Body)
	}
	pcID := uuid.NewString()
	if _, err := e.q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: pcID, Name: "p", Provider: "claude", Model: "m", Env: "{}"}); err != nil {
		t.Fatal(err)
	}
	if w := e.do("PUT", "/settings/autonomy", `{"step_down_escapes":2,"step_down_window_days":14,"proposal_provider_config_id":"`+pcID+`"}`); w.Code != 200 {
		t.Fatalf("settings: %d", w.Code)
	}
	if w := e.do("POST", "/workflows/"+e.wfID+"/labels/work/autonomy/proposals/run", ""); w.Code != 400 {
		t.Fatalf("not a gate: %d", w.Code)
	}
	if w := e.do("POST", "/workflows/"+e.wfID+"/labels/nope/autonomy/proposals/run", ""); w.Code != 404 {
		t.Fatalf("missing label: %d", w.Code)
	}
	for _, typ := range []string{"chore", "chore", "bug"} {
		task, err := e.q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: typ, WorkflowID: e.wfID, RepoID: e.repoID, Label: "work", Type: typ})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.db.SQL().Exec(`INSERT INTO task_label_history (id, task_id, from_label, to_label, trigger) VALUES (?, ?, 'review', 'work', 'human')`, uuid.NewString(), task.ID); err != nil {
			t.Fatal(err)
		}
	}
	// The background run; RunGate gives the same result synchronously.
	if _, err := svc.RunGate(ctx, e.wfID, "review", propose.TriggerManual, "josh"); err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/workflows/"+e.wfID+"/labels/review/autonomy/proposal-runs", "")
	var runs []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &runs)
	if w.Code != 200 || len(runs) != 1 || runs[0]["status"] != "succeeded" || runs[0]["proposal_count"] != float64(2) {
		t.Fatalf("runs: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/workflows/"+e.wfID+"/labels/review/autonomy/proposal-runs?limit=0", ""); w.Code != 400 {
		t.Fatalf("bad limit: %d", w.Code)
	}
	w = e.do("GET", "/autonomy/rule-proposals?workflow_id="+e.wfID+"&label=review", "")
	var ps []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &ps)
	if w.Code != 200 || len(ps) != 2 || ps[0]["task_type"] != "chore" || ps[0]["caught"] != float64(2) || ps[0]["summary"] == "" {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/autonomy/rule-proposals?status=bogus", ""); w.Code != 400 {
		t.Fatalf("bad status: %d", w.Code)
	}
	chore, bug := ps[0]["id"].(string), ps[1]["id"].(string)
	w = e.do("POST", "/autonomy/rule-proposals/"+chore+"/accept", "")
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"status":"accepted"`) || !strings.Contains(w.Body.String(), `"task_type":"chore"`) {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/autonomy/rule-proposals/"+chore+"/accept", ""); w.Code != 409 {
		t.Fatalf("re-accept: %d", w.Code)
	}
	if w := e.do("POST", "/autonomy/rule-proposals/"+bug+"/dismiss", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"dismissed"`) {
		t.Fatalf("dismiss: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/autonomy/rule-proposals/missing/dismiss", ""); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
	w = e.do("GET", "/autonomy/rule-proposals?workflow_id="+e.wfID+"&label=review", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("open after decisions: %s", w.Body)
	}
	w = e.do("GET", "/autonomy/ask-rules?workflow_id="+e.wfID+"&label=review", "")
	if !strings.Contains(w.Body.String(), `"task_type":"chore"`) {
		t.Fatalf("rule not created: %s", w.Body)
	}
}
