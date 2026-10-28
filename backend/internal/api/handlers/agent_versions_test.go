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

type versionResp struct {
	Version  int64          `json:"version"`
	Snapshot map[string]any `json:"snapshot"`
	ActorID  *string        `json:"actor_id"`
	Note     *string        `json:"note"`
}

// agentBody is a full update body (PUT replaces name/prompt/labels/limits).
func agentBody(pc, name, prompt string, extra map[string]any) map[string]any {
	b := map[string]any{
		"name": name, "provider_config_id": pc, "system_prompt": prompt,
		"labels": "[]", "max_tokens": 8192, "timeout_secs": 600, "max_turns": 50,
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func listVersions(t *testing.T, r http.Handler, id string) []versionResp {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/agents/"+id+"/versions", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list versions: %d %s", w.Code, w.Body)
	}
	var out []versionResp
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func createAgentV(t *testing.T, r http.Handler, pc string) string {
	t.Helper()
	w := postJSON(t, r, "/agents", agentBody(pc, "a", "p1", nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var c agentConfigResponse
	_ = json.Unmarshal(w.Body.Bytes(), &c)
	return c.ID
}

func TestAgentVersions_CreateAndBehaviouralUpdates(t *testing.T) {
	r, q := setupAgentsRouter(t)
	pc := mkProviderConfig(t, q)
	id := createAgentV(t, r, pc)

	vs := listVersions(t, r, id)
	if len(vs) != 1 || vs[0].Version != 1 || vs[0].Snapshot["system_prompt"] != "p1" {
		t.Fatalf("after create: %+v", vs)
	}

	// Prompt change + note -> v2.
	w := putJSON(t, r, "/agents/"+id, agentBody(pc, "a", "p2", map[string]any{"note": "new prompt"}))
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	var cfg struct {
		CurrentVersion *int64 `json:"current_version"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &cfg)
	if cfg.CurrentVersion == nil || *cfg.CurrentVersion != 2 {
		t.Fatalf("current_version = %v", cfg.CurrentVersion)
	}
	vs = listVersions(t, r, id)
	if len(vs) != 2 || vs[0].Version != 2 || vs[0].Note == nil || *vs[0].Note != "new prompt" {
		t.Fatalf("after prompt change: %+v", vs)
	}

	// Identity-only changes and identical re-sends write nothing.
	for _, body := range []map[string]any{
		agentBody(pc, "a", "p2", nil),
		agentBody(pc, "renamed", "p2", map[string]any{"labels": `["x"]`, "priority": 5, "enabled": false}),
	} {
		if w := putJSON(t, r, "/agents/"+id, body); w.Code != http.StatusOK {
			t.Fatalf("put: %d %s", w.Code, w.Body)
		}
	}
	if n := len(listVersions(t, r, id)); n != 2 {
		t.Fatalf("expected 2 versions, got %d", n)
	}

	// Each behavioural field writes a version.
	pc2 := mkProviderConfig(t, q)
	changes := []map[string]any{
		{"effort": "high"},
		{"max_turns": 80},
		{"fallback_provider_config_ids": []string{pc2}},
		{"escalation_ladder": []map[string]any{{"provider_config_id": pc2}}},
		{"command_denylist": `["rm"]`},
	}
	want := 2
	for _, ch := range changes {
		b := agentBody(pc, "renamed", "p2", ch)
		if w := putJSON(t, r, "/agents/"+id, b); w.Code != http.StatusOK {
			t.Fatalf("put %v: %d %s", ch, w.Code, w.Body)
		}
		want++
		if n := len(listVersions(t, r, id)); n != want {
			t.Fatalf("after %v: %d versions, want %d", ch, n, want)
		}
	}
}

func TestAgentVersions_GetAndErrors(t *testing.T) {
	r, q := setupAgentsRouter(t)
	id := createAgentV(t, r, mkProviderConfig(t, q))
	get := func(path string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Code
	}
	if c := get("/agents/" + id + "/versions/1"); c != 200 {
		t.Fatalf("got %d", c)
	}
	if c := get("/agents/" + id + "/versions/9"); c != 404 {
		t.Fatalf("got %d", c)
	}
	if c := get("/agents/" + id + "/versions/abc"); c != 400 {
		t.Fatalf("got %d", c)
	}
	if c := get("/agents/nope/versions"); c != 404 {
		t.Fatalf("got %d", c)
	}
}

func TestAgentVersions_Restore(t *testing.T) {
	r, q := setupAgentsRouter(t)
	pc := mkProviderConfig(t, q)
	id := createAgentV(t, r, pc)
	putJSON(t, r, "/agents/"+id, agentBody(pc, "keepname", "p2", map[string]any{"labels": `["x"]`}))

	// Restoring the current version is a no-op.
	if w := postJSON(t, r, "/agents/"+id+"/versions/2/restore", map[string]any{}); w.Code != 200 {
		t.Fatalf("restore current: %d %s", w.Code, w.Body)
	}
	if n := len(listVersions(t, r, id)); n != 2 {
		t.Fatalf("restore of current wrote a version: %d", n)
	}

	w := postJSON(t, r, "/agents/"+id+"/versions/1/restore", map[string]any{})
	if w.Code != 200 {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	var cfg agentConfigResponse
	_ = json.Unmarshal(w.Body.Bytes(), &cfg)
	if cfg.SystemPrompt != "p1" || cfg.Name != "keepname" || cfg.Labels != `["x"]` {
		t.Fatalf("restored cfg: %+v", cfg.AgentConfig)
	}
	vs := listVersions(t, r, id)
	if len(vs) != 3 || vs[0].Note == nil || *vs[0].Note != "Restored from v1" || vs[0].Snapshot["system_prompt"] != "p1" {
		t.Fatalf("versions: %+v", vs)
	}
	if w := postJSON(t, r, "/agents/"+id+"/versions/42/restore", map[string]any{}); w.Code != 404 {
		t.Fatalf("restore missing: %d", w.Code)
	}
}

func TestAgentVersions_RestoreDeletedProvider(t *testing.T) {
	r, q := setupAgentsRouter(t)
	pc := mkProviderConfig(t, q)
	pc2 := mkProviderConfig(t, q)
	id := createAgentV(t, r, pc)
	putJSON(t, r, "/agents/"+id, agentBody(pc2, "a", "p1", nil))
	if err := q.DeleteProviderConfig(context.Background(), pc); err != nil {
		t.Fatal(err)
	}
	if w := postJSON(t, r, "/agents/"+id+"/versions/1/restore", map[string]any{}); w.Code != 400 {
		t.Fatalf("expected 400, got %d %s", w.Code, w.Body)
	}
}

func TestAgentVersions_LazyBaseline(t *testing.T) {
	r, q := setupAgentsRouter(t)
	pc := mkProviderConfig(t, q)
	cfg, err := q.CreateAgentConfig(context.Background(), gen.CreateAgentConfigParams{
		ID: uuid.NewString(), Name: "raw", ProviderConfigID: pc, SystemPrompt: "orig", Labels: "[]",
		MaxTokens: 100, TimeoutSecs: 60, MaxTurns: 5, EnabledPlugins: "[]", EnabledMcpServers: "[]",
		CommandAllowlist: "[]", CommandDenylist: "[]", MaxRetries: 3, RetryBackoffSecs: 30,
		ResumeSessions: 1, MaxSubtasks: 10, FallbackProviderConfigIds: "[]", EscalationLadder: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := putJSON(t, r, "/agents/"+cfg.ID, map[string]any{
		"name": "raw", "provider_config_id": pc, "system_prompt": "changed", "labels": "[]",
		"max_tokens": 100, "timeout_secs": 60, "max_turns": 5,
	})
	if w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	vs := listVersions(t, r, cfg.ID)
	if len(vs) != 2 || vs[1].Snapshot["system_prompt"] != "orig" || vs[0].Snapshot["system_prompt"] != "changed" {
		t.Fatalf("versions: %+v", vs)
	}
}
