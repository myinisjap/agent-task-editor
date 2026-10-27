package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type expResp struct {
	ID              string         `json:"id"`
	Status          string         `json:"status"`
	SharePercent    int64          `json:"share_percent"`
	BaseVersion     *int64         `json:"base_version"`
	PromotedVersion *int64         `json:"promoted_version"`
	Candidate       map[string]any `json:"candidate_snapshot"`
	Results         map[string]any `json:"results"`
}

func candidateBody(pc string, prompt string, share int) map[string]any {
	return map[string]any{
		"candidate": map[string]any{
			"system_prompt": prompt, "provider_config_id": pc, "effort": "", "permission_mode": "",
			"max_turns": 50, "max_tokens": 8192, "timeout_secs": 600,
		},
		"share_percent": share, "note": "try it",
	}
}

func decodeExp(t *testing.T, w *httptest.ResponseRecorder) expResp {
	t.Helper()
	var e expResp
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode: %v %s", err, w.Body)
	}
	return e
}

func startExp(t *testing.T, r http.Handler, id, pc, prompt string) expResp {
	t.Helper()
	w := postJSON(t, r, "/agents/"+id+"/experiments", candidateBody(pc, prompt, 50))
	if w.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	return decodeExp(t, w)
}

func TestAgentExperiments_StartValidation(t *testing.T) {
	r, q := setupAgentsRouter(t)
	pc := mkProviderConfig(t, q)
	id := createAgentV(t, r, pc)
	url := "/agents/" + id + "/experiments"

	for _, share := range []int{0, 91} {
		if w := postJSON(t, r, url, candidateBody(pc, "p2", share)); w.Code != 400 {
			t.Errorf("share %d: %d", share, w.Code)
		}
	}
	if w := postJSON(t, r, url, candidateBody("missing-provider", "p2", 50)); w.Code != 400 {
		t.Errorf("unknown provider: %d %s", w.Code, w.Body)
	}
	same := candidateBody(pc, "p1", 50)
	same["candidate"] = listVersions(t, r, id)[0].Snapshot
	if w := postJSON(t, r, url, same); w.Code != 400 {
		t.Errorf("identical candidate: %d %s", w.Code, w.Body)
	}
	if w := postJSON(t, r, "/agents/nope/experiments", candidateBody(pc, "p2", 50)); w.Code != 404 {
		t.Errorf("unknown agent: %d", w.Code)
	}
	e := startExp(t, r, id, pc, "p2")
	if e.Status != "running" || e.SharePercent != 50 || e.BaseVersion == nil || *e.BaseVersion != 1 || e.Candidate["system_prompt"] != "p2" {
		t.Fatalf("started: %+v", e)
	}
	if w := postJSON(t, r, url, candidateBody(pc, "p3", 50)); w.Code != 409 {
		t.Errorf("second start: %d", w.Code)
	}
	// Starting does not touch the live config or its version history.
	if vs := listVersions(t, r, id); len(vs) != 1 {
		t.Fatalf("versions = %d", len(vs))
	}
}

func TestAgentExperiments_StopThenRestart(t *testing.T) {
	r, q := setupAgentsRouter(t)
	pc := mkProviderConfig(t, q)
	id := createAgentV(t, r, pc)
	e := startExp(t, r, id, pc, "p2")
	stop := "/agents/" + id + "/experiments/" + e.ID + "/stop"
	w := postJSON(t, r, stop, map[string]any{})
	if w.Code != 200 || decodeExp(t, w).Status != "stopped" {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	if w := postJSON(t, r, stop, map[string]any{}); w.Code != 409 {
		t.Fatalf("second stop: %d", w.Code)
	}
	if w := postJSON(t, r, "/agents/"+id+"/experiments/"+e.ID+"/promote", map[string]any{}); w.Code != 409 {
		t.Fatalf("promote stopped: %d", w.Code)
	}
	startExp(t, r, id, pc, "p3")
	lw := httptest.NewRecorder()
	r.ServeHTTP(lw, httptest.NewRequest(http.MethodGet, "/agents/"+id+"/experiments", nil))
	var list []expResp
	_ = json.Unmarshal(lw.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("list = %d", len(list))
	}
	if w := httptest.NewRecorder(); true {
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/agents/"+id+"/experiments/"+e.ID, nil))
		if g := decodeExp(t, w); w.Code != 200 || g.Results == nil {
			t.Fatalf("get: %d %s", w.Code, w.Body)
		}
	}
	other := createAgentV(t, r, pc)
	gw := httptest.NewRecorder()
	r.ServeHTTP(gw, httptest.NewRequest(http.MethodGet, "/agents/"+other+"/experiments/"+e.ID, nil))
	if gw.Code != 404 {
		t.Fatalf("foreign experiment: %d", gw.Code)
	}
}

func TestAgentExperiments_RunningSummaryOnGet(t *testing.T) {
	r, q := setupAgentsRouter(t)
	pc := mkProviderConfig(t, q)
	id := createAgentV(t, r, pc)
	get := func() map[string]any {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/agents/"+id, nil))
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	if v, ok := get()["running_experiment"]; !ok || v != nil {
		t.Fatalf("expected null running_experiment, got %v (present=%v)", v, ok)
	}
	e := startExp(t, r, id, pc, "p2")
	re, _ := get()["running_experiment"].(map[string]any)
	if re == nil || re["id"] != e.ID || re["share_percent"] != float64(50) {
		t.Fatalf("running_experiment = %v", re)
	}
	postJSON(t, r, "/agents/"+id+"/experiments/"+e.ID+"/stop", map[string]any{})
	if get()["running_experiment"] != nil {
		t.Fatal("expected null after stop")
	}
}

func TestAgentExperiments_Promote(t *testing.T) {
	r, q := setupAgentsRouter(t)
	ctx := context.Background()
	pc := mkProviderConfig(t, q)
	id := createAgentV(t, r, pc)
	e := startExp(t, r, id, pc, "p2")

	// A candidate-arm run (version NULL) on a task assigned to the experiment.
	wfs, _ := q.ListWorkflows(ctx)
	wfID := wfs[0].ID
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		t.Fatal(err)
	}
	task, err := q.CreateTask(ctx, gen.CreateTaskParams{ID: uuid.NewString(), Title: "t", WorkflowID: wfID, RepoID: repoID, Label: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertAgentExperimentAssignment(ctx, gen.InsertAgentExperimentAssignmentParams{ExperimentID: e.ID, TaskID: task.ID, Arm: "candidate"}); err != nil {
		t.Fatal(err)
	}
	run, err := q.CreateAgentRun(ctx, gen.CreateAgentRunParams{ID: uuid.NewString(), TaskID: task.ID, AgentConfigID: &id})
	if err != nil {
		t.Fatal(err)
	}
	arm := "candidate"
	if err := q.SetAgentRunExperiment(ctx, gen.SetAgentRunExperimentParams{ExperimentID: &e.ID, ExperimentArm: &arm, ID: run.ID}); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, r, "/agents/"+id+"/experiments/"+e.ID+"/promote", map[string]any{"note": "ship it"})
	if w.Code != 200 {
		t.Fatalf("promote: %d %s", w.Code, w.Body)
	}
	var out struct {
		Experiment expResp `json:"experiment"`
		Config     struct {
			SystemPrompt   string `json:"system_prompt"`
			CurrentVersion *int64 `json:"current_version"`
		} `json:"config"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Experiment.Status != "promoted" || out.Experiment.PromotedVersion == nil || *out.Experiment.PromotedVersion != 2 {
		t.Fatalf("experiment: %+v", out.Experiment)
	}
	if out.Config.SystemPrompt != "p2" || out.Config.CurrentVersion == nil || *out.Config.CurrentVersion != 2 {
		t.Fatalf("config: %+v", out.Config)
	}
	vs := listVersions(t, r, id)
	if len(vs) != 2 || vs[0].Note == nil || *vs[0].Note != "ship it" || vs[0].Snapshot["system_prompt"] != "p2" {
		t.Fatalf("versions: %+v", vs)
	}
	got, err := q.GetAgentRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentConfigVersion == nil || *got.AgentConfigVersion != 2 {
		t.Fatalf("candidate run version = %v, want 2", got.AgentConfigVersion)
	}
	if w := postJSON(t, r, "/agents/"+id+"/experiments/"+e.ID+"/promote", map[string]any{}); w.Code != 409 {
		t.Fatalf("second promote: %d", w.Code)
	}
}
