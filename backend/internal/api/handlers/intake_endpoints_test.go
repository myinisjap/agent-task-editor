package handlers_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/intakehook"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type nopPub struct{}

func (nopPub) Publish(string, any) {}

type intakeEnv struct {
	r      http.Handler
	q      *gen.Queries
	h      *handlers.IntakeEndpointsHandler
	repoID string
	now    time.Time
}

func setupIntake(t *testing.T) *intakeEnv {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	wfs, err := q.ListWorkflows(context.Background())
	if err != nil || len(wfs) == 0 {
		t.Fatalf("seeded workflow: %v", err)
	}
	repoID := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{ID: repoID, Name: "r", Path: t.TempDir(), WorkflowID: &wfs[0].ID}); err != nil {
		t.Fatal(err)
	}
	h := handlers.NewIntakeEndpointsHandler(q, intakehook.NewService(db.SQL(), q, nopPub{}))
	e := &intakeEnv{q: q, h: h, repoID: repoID, now: time.Now()}
	h.SetClock(func() time.Time { return e.now })
	r := chi.NewRouter()
	r.Get("/intake-endpoints", h.List)
	r.Post("/intake-endpoints", h.Create)
	r.Get("/intake-endpoints/{id}", h.Get)
	r.Put("/intake-endpoints/{id}", h.Update)
	r.Delete("/intake-endpoints/{id}", h.Delete)
	r.Post("/intake-endpoints/{id}/rotate-secret", h.RotateSecret)
	r.Post("/intake-endpoints/{id}/test", h.Test)
	r.Post("/intake/{endpoint_id}", h.Receive)
	e.r = r
	return e
}

func (e *intakeEnv) do(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

type epResp struct {
	ID        string `json:"id"`
	Secret    string `json:"secret"`
	SecretSet bool   `json:"secret_set"`
	URLPath   string `json:"url_path"`
}

func (e *intakeEnv) create(t *testing.T, extra string) epResp {
	t.Helper()
	body := `{"name":"ci","repo_id":"` + e.repoID + `","title_template":"{{alert.title}}","dedupe_key_template":"{{alert.id}}"` + extra + `}`
	w := e.do(http.MethodPost, "/intake-endpoints", body, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var ep epResp
	if err := json.Unmarshal(w.Body.Bytes(), &ep); err != nil {
		t.Fatal(err)
	}
	return ep
}

func sig(secret, body string) map[string]string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return map[string]string{"X-ATE-Signature-256": "sha256=" + hex.EncodeToString(m.Sum(nil))}
}

func TestIntakeEndpoints_CRUDSecretOnlyOnCreateAndRotate(t *testing.T) {
	e := setupIntake(t)
	ep := e.create(t, "")
	if !strings.HasPrefix(ep.Secret, "whsec_") || !ep.SecretSet || ep.URLPath != "/api/v1/intake/"+ep.ID {
		t.Fatalf("create resp: %+v", ep)
	}
	for _, path := range []string{"/intake-endpoints", "/intake-endpoints/" + ep.ID} {
		w := e.do(http.MethodGet, path, "", nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), ep.Secret) || strings.Contains(w.Body.String(), `"secret":`) {
			t.Errorf("%s leaks secret or failed: %d %s", path, w.Code, w.Body.String())
		}
	}
	// update keeps the secret and flips enabled
	upd := `{"name":"ci2","repo_id":"` + e.repoID + `","title_template":"t","enabled":false}`
	w := e.do(http.MethodPut, "/intake-endpoints/"+ep.ID, upd, nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"secret":`) || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	stored, _ := e.q.GetIntakeEndpoint(context.Background(), ep.ID)
	if stored.Secret != ep.Secret {
		t.Error("update changed secret")
	}
	// rotate
	w = e.do(http.MethodPost, "/intake-endpoints/"+ep.ID+"/rotate-secret", `{}`, nil)
	var rot struct{ Secret string }
	_ = json.Unmarshal(w.Body.Bytes(), &rot)
	if w.Code != 200 || rot.Secret == "" || rot.Secret == ep.Secret {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	if w := e.do(http.MethodPost, "/intake-endpoints/nope/rotate-secret", `{}`, nil); w.Code != 404 {
		t.Errorf("rotate missing: %d", w.Code)
	}
	if w := e.do(http.MethodDelete, "/intake-endpoints/"+ep.ID, "", nil); w.Code != 204 {
		t.Errorf("delete: %d", w.Code)
	}
	if w := e.do(http.MethodGet, "/intake-endpoints/"+ep.ID, "", nil); w.Code != 404 {
		t.Errorf("get deleted: %d", w.Code)
	}
}

func TestIntakeEndpoints_Validation(t *testing.T) {
	e := setupIntake(t)
	big := strings.Repeat("a", 5000)
	cases := map[string]string{
		"no name":       `{"name":"","repo_id":"` + e.repoID + `","title_template":"t"}`,
		"no title":      `{"name":"n","repo_id":"` + e.repoID + `","title_template":""}`,
		"bad repo":      `{"name":"n","repo_id":"nope","title_template":"t"}`,
		"bad workflow":  `{"name":"n","repo_id":"` + e.repoID + `","workflow_id":"nope","title_template":"t"}`,
		"bad label":     `{"name":"n","repo_id":"` + e.repoID + `","target_label":"nope","title_template":"t"}`,
		"template long": `{"name":"n","repo_id":"` + e.repoID + `","title_template":"t","description_template":"` + big + `"}`,
		"bad json":      `{`,
	}
	for name, body := range cases {
		if w := e.do(http.MethodPost, "/intake-endpoints", body, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := e.do(http.MethodPut, "/intake-endpoints/nope", `{}`, nil); w.Code != 404 {
		t.Errorf("update missing: %d", w.Code)
	}
}

func TestIntakeReceive_CreatesTask(t *testing.T) {
	e := setupIntake(t)
	ep := e.create(t, "")
	body := `{"alert":{"title":"CI red","id":"1"}}`
	w := e.do(http.MethodPost, "/intake/"+ep.ID, body, sig(ep.Secret, body))
	if w.Code != http.StatusCreated {
		t.Fatalf("receive: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	task, err := e.q.GetTask(context.Background(), res.TaskID)
	if err != nil || task.Source != "webhook" || task.Title != "CI red" || task.Label != "not_ready" {
		t.Fatalf("task=%+v err=%v", task, err)
	}

	// dedupe -> 200 + source comment
	body2 := `{"alert":{"title":"CI red again","id":"1"}}`
	w = e.do(http.MethodPost, "/intake/"+ep.ID, body2, sig(ep.Secret, body2))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), res.TaskID) {
		t.Fatalf("dedupe: %d %s", w.Code, w.Body.String())
	}
	cs, _ := e.q.ListTaskSourceComments(context.Background(), res.TaskID)
	if len(cs) != 1 {
		t.Errorf("comments = %d", len(cs))
	}
}

func TestIntakeReceive_Rejections(t *testing.T) {
	e := setupIntake(t)
	ep := e.create(t, "")
	body := `{"alert":{"title":"x"}}`
	url := "/intake/" + ep.ID

	if w := e.do(http.MethodPost, "/intake/nope", body, sig(ep.Secret, body)); w.Code != 404 {
		t.Errorf("unknown endpoint: %d", w.Code)
	}
	bad := map[string]map[string]string{
		"missing":   nil,
		"wrong":     sig("other", body),
		"no prefix": {"X-ATE-Signature-256": strings.TrimPrefix(sig(ep.Secret, body)["X-ATE-Signature-256"], "sha256=")},
		"not hex":   {"X-ATE-Signature-256": "sha256=zzzz"},
	}
	for name, h := range bad {
		if w := e.do(http.MethodPost, url, body, h); w.Code != 401 {
			t.Errorf("%s signature: expected 401, got %d", name, w.Code)
		}
	}
	// signature is over the raw body
	if w := e.do(http.MethodPost, url, body+" ", sig(ep.Secret, body)); w.Code != 401 {
		t.Errorf("tampered body: %d", w.Code)
	}

	// too large (signature irrelevant: size is checked while reading)
	huge := `{"a":"` + strings.Repeat("x", 300<<10) + `"}`
	if w := e.do(http.MethodPost, url, huge, sig(ep.Secret, huge)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("huge: %d", w.Code)
	}

	// invalid JSON / non-object, validly signed
	for _, b := range []string{`{`, `[1,2]`, `"s"`} {
		if w := e.do(http.MethodPost, url, b, sig(ep.Secret, b)); w.Code != 400 {
			t.Errorf("body %q: expected 400, got %d", b, w.Code)
		}
	}

	// disabled (signed) -> 403; unsigned stays 401
	upd := `{"name":"ci","repo_id":"` + e.repoID + `","title_template":"t","enabled":false}`
	if w := e.do(http.MethodPut, "/intake-endpoints/"+ep.ID, upd, nil); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := e.do(http.MethodPost, url, body, sig(ep.Secret, body)); w.Code != http.StatusForbidden {
		t.Errorf("disabled: %d", w.Code)
	}
	if w := e.do(http.MethodPost, url, body, nil); w.Code != 401 {
		t.Errorf("disabled unsigned: %d", w.Code)
	}
}

func TestIntakeReceive_RateLimit(t *testing.T) {
	e := setupIntake(t)
	ep := e.create(t, "")
	url := "/intake/" + ep.ID
	post := func(i int) *httptest.ResponseRecorder {
		b := `{"alert":{"title":"t","id":"` + strings.Repeat("k", 1) + string(rune('a'+i%26)) + `"}}`
		return e.do(http.MethodPost, url, b, sig(ep.Secret, b))
	}
	for i := 0; i < 30; i++ {
		if w := post(i); w.Code >= 400 {
			t.Fatalf("request %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	w := post(31)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("31st: %d retry-after=%q", w.Code, w.Header().Get("Retry-After"))
	}
	// unauthenticated requests do not consume the budget
	e.now = e.now.Add(61 * time.Second)
	if w := e.do(http.MethodPost, url, `{}`, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := post(1); w.Code >= 400 {
		t.Errorf("after window reset: %d", w.Code)
	}
}

func TestIntakeEndpoints_TestPreviewCreatesNothing(t *testing.T) {
	e := setupIntake(t)
	ep := e.create(t, "")
	req, _ := json.Marshal(map[string]any{
		"payload":        map[string]any{"alert": map[string]any{"title": "Boom", "id": "9"}},
		"title_template": "Preview: {{alert.title}}",
	})
	w := e.do(http.MethodPost, "/intake-endpoints/"+ep.ID+"/test", string(req), nil)
	if w.Code != 200 {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["title"] != "Preview: Boom" || out["dedupe_key"] != "9" || out["label"] != "not_ready" {
		t.Errorf("out = %v", out)
	}
	if tasks, _ := e.q.ListTasks(context.Background()); len(tasks) != 0 {
		t.Errorf("preview created %d tasks", len(tasks))
	}
	if w := e.do(http.MethodPost, "/intake-endpoints/"+ep.ID+"/test", `{}`, nil); w.Code != 400 {
		t.Errorf("no payload: %d", w.Code)
	}
}
