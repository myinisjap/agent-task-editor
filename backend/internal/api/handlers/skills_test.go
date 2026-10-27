package handlers_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/skills"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const skillMD = "---\nname: lint-fixer\ndescription: Fixes lint.\n---\nSteps\n"

type skillsEnv struct {
	r   http.Handler
	q   *gen.Queries
	dir string
}

func setupSkills(t *testing.T) skillsEnv {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	dir := t.TempDir()
	h := handlers.NewSkillsHandler(q, db.SQL(), skills.Store{Dir: dir})
	r := chi.NewRouter()
	r.Get("/skills", h.List)
	r.Post("/skills", h.Create)
	r.Get("/skills/{id}", h.Get)
	r.Patch("/skills/{id}", h.Update)
	r.Delete("/skills/{id}", h.Delete)
	r.Get("/agents/{id}/skills", h.ListForAgent)
	r.Put("/agents/{id}/skills", h.SetForAgent)
	r.Get("/repos/{id}/skills", h.ListForRepo)
	r.Put("/repos/{id}/skills", h.SetForRepo)
	return skillsEnv{r: r, q: q, dir: dir}
}

func (e skillsEnv) do(t *testing.T, method, path, ctype string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func (e skillsEnv) createJSON(t *testing.T, md string) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"skill_md": md})
	w := e.do(t, "POST", "/skills", "application/json", b)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestSkills_CreateGetUpdateDelete(t *testing.T) {
	e := setupSkills(t)
	s := e.createJSON(t, skillMD)
	id := s["id"].(string)
	if s["name"] != "lint-fixer" || s["description"] != "Fixes lint." || s["enabled"] != true {
		t.Fatalf("bad create: %v", s)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "lint-fixer", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	// Duplicate name -> 409.
	b, _ := json.Marshal(map[string]string{"skill_md": skillMD})
	if w := e.do(t, "POST", "/skills", "application/json", b); w.Code != http.StatusConflict {
		t.Errorf("dup: %d", w.Code)
	}
	// Invalid frontmatter -> 400.
	b, _ = json.Marshal(map[string]string{"skill_md": "no frontmatter"})
	if w := e.do(t, "POST", "/skills", "application/json", b); w.Code != http.StatusBadRequest {
		t.Errorf("bad fm: %d", w.Code)
	}

	w := e.do(t, "GET", "/skills/"+id, "", nil)
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got["skill_md"] != skillMD {
		t.Errorf("get: %d %v", w.Code, got)
	}

	// Disable + replace content.
	b, _ = json.Marshal(map[string]any{"enabled": false, "skill_md": strings.Replace(skillMD, "Fixes lint.", "New desc.", 1)})
	w = e.do(t, "PATCH", "/skills/"+id, "application/json", b)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got["enabled"] != false || got["description"] != "New desc." {
		t.Errorf("patch: %d %s", w.Code, w.Body)
	}
	// Rename via content is rejected.
	b, _ = json.Marshal(map[string]any{"skill_md": strings.Replace(skillMD, "lint-fixer", "other", 1)})
	if w := e.do(t, "PATCH", "/skills/"+id, "application/json", b); w.Code != http.StatusBadRequest {
		t.Errorf("rename: %d", w.Code)
	}

	if w := e.do(t, "DELETE", "/skills/"+id, "", nil); w.Code != http.StatusNoContent {
		t.Errorf("delete: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "lint-fixer")); !os.IsNotExist(err) {
		t.Error("folder not removed")
	}
	if w := e.do(t, "GET", "/skills/"+id, "", nil); w.Code != http.StatusNotFound {
		t.Errorf("get after delete: %d", w.Code)
	}
}

func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range entries {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestSkills_CreateFromZip(t *testing.T) {
	e := setupSkills(t)
	z := zipBytes(t, map[string]string{"lint-fixer/SKILL.md": skillMD, "lint-fixer/scripts/x.sh": "echo"})
	if w := e.do(t, "POST", "/skills", "application/zip", z); w.Code != http.StatusCreated {
		t.Fatalf("zip create: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "lint-fixer", "scripts", "x.sh")); err != nil {
		t.Error(err)
	}
	evil := zipBytes(t, map[string]string{"SKILL.md": strings.Replace(skillMD, "lint-fixer", "evil", 1), "../escape": "x"})
	if w := e.do(t, "POST", "/skills", "application/zip", evil); w.Code != http.StatusBadRequest {
		t.Errorf("traversal: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "..", "escape")); err == nil {
		t.Error("escaped")
	}
}

func TestSkills_AttachToAgentAndRepo(t *testing.T) {
	e := setupSkills(t)
	ctx := t.Context()
	s := e.createJSON(t, skillMD)
	id := s["id"].(string)

	repoID := uuid.NewString()
	if _, err := e.q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "r", Path: "/tmp/r-skills"}); err != nil {
		t.Fatal(err)
	}
	pc, _ := e.q.CreateProviderConfig(ctx, gen.CreateProviderConfigParams{ID: uuid.NewString(), Name: "p", Provider: "codex_cli", Model: "m", Env: `{}`})
	ac, err := e.q.CreateAgentConfig(ctx, gen.CreateAgentConfigParams{ID: uuid.NewString(), Name: "a", ProviderConfigID: pc.ID, Labels: `[]`, MaxRetries: 1, RetryBackoffSecs: 1})
	if err != nil {
		t.Fatal(err)
	}

	for _, base := range []string{"/agents/" + ac.ID + "/skills", "/repos/" + repoID + "/skills"} {
		b, _ := json.Marshal(map[string]any{"skill_ids": []string{id}})
		w := e.do(t, "PUT", base, "application/json", b)
		var list []map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &list)
		if w.Code != 200 || len(list) != 1 || list[0]["id"] != id {
			t.Fatalf("PUT %s: %d %s", base, w.Code, w.Body)
		}
		b, _ = json.Marshal(map[string]any{"skill_ids": []string{"nope"}})
		if w := e.do(t, "PUT", base, "application/json", b); w.Code != http.StatusBadRequest {
			t.Errorf("unknown id: %d", w.Code)
		}
		w = e.do(t, "GET", "/skills", "", nil)
		var all []map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &all)
		key, want := "agent_config_ids", ac.ID
		if strings.HasPrefix(base, "/repos/") {
			key, want = "repo_ids", repoID
		}
		if ids, _ := all[0][key].([]any); len(ids) != 1 || ids[0] != want {
			t.Errorf("GET /skills %s = %v", key, all[0][key])
		}
		// Detach by replacing with an empty set.
		b, _ = json.Marshal(map[string]any{"skill_ids": []string{}})
		e.do(t, "PUT", base, "application/json", b)
		w = e.do(t, "GET", base, "", nil)
		if strings.TrimSpace(w.Body.String()) != "[]" {
			t.Errorf("after detach: %s", w.Body)
		}
	}
	if w := e.do(t, "GET", "/agents/missing/skills", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("missing agent: %d", w.Code)
	}
}
