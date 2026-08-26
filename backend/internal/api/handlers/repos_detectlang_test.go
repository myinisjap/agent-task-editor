package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// stubLLMDetector is a hermetic agent.LLMDetector for tests — it never shells
// out to a real CLI. Calls records every invocation so tests can assert the
// LLM path was (or wasn't) reached.
type stubLLMDetector struct {
	guesses []agent.LanguageGuess
	err     error
	calls   int
}

func (s *stubLLMDetector) DetectLanguages(_ context.Context, _ agent.LLMDetectInput) ([]agent.LanguageGuess, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.guesses, nil
}

// createTestRepo inserts a repo row pointing at dir, returning its id.
func createTestRepo(t *testing.T, q *gen.Queries, dir string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := q.CreateRepo(context.Background(), gen.CreateRepoParams{
		ID:   id,
		Name: "repo",
		Path: dir,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	return id
}

func setupDetectLanguagesRouter(t *testing.T, q *gen.Queries, detector agent.LLMDetector) (http.Handler, *handlers.ReposHandler) {
	t.Helper()
	h := handlers.NewReposHandler(q, "", nil, nil)
	if detector != nil {
		h.SetLLMDetector(detector)
	} else {
		h.SetLLMDetector(nil)
	}
	router := chi.NewRouter()
	router.Post("/repos/{id}/detect-languages", h.DetectLanguages)
	return router, h
}

func postDetectLanguages(t *testing.T, router http.Handler, repoID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/repos/"+repoID+"/detect-languages", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

type detectLanguagesResponse struct {
	Suggestions []struct {
		ID        string `json:"id"`
		Version   string `json:"version"`
		Source    string `json:"source"`
		Ambiguous bool   `json:"ambiguous"`
	} `json:"suggestions"`
	UsedLLM bool `json:"used_llm"`
}

// TestDetectLanguages_ScanOnly_NeverInvokesLLM covers a clean, unambiguous
// scan result: the endpoint must return immediately with used_llm=false and
// never call the LLM stub at all — spending money/latency on a call that
// can only produce a worse answer than go.mod already gave is the wrong
// trade (see PLAN5.md's D2 task detail).
func TestDetectLanguages_ScanOnly_NeverInvokesLLM(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repoID := createTestRepo(t, q, dir)

	stub := &stubLLMDetector{}
	router, _ := setupDetectLanguagesRouter(t, q, stub)

	w := postDetectLanguages(t, router, repoID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp detectLanguagesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UsedLLM {
		t.Errorf("used_llm = true, want false for a clean scan result")
	}
	if stub.calls != 0 {
		t.Errorf("LLM stub invoked %d times, want 0 for a clean scan result", stub.calls)
	}
	if len(resp.Suggestions) != 1 || resp.Suggestions[0].ID != "go" || resp.Suggestions[0].Version != "1.26" {
		t.Errorf("unexpected suggestions: %+v", resp.Suggestions)
	}
}

// TestDetectLanguages_EmptyScan_InvokesLLM covers the "scan found nothing"
// gap: the LLM stub must be invoked exactly once and its validated guess
// returned with used_llm=true.
func TestDetectLanguages_EmptyScan_InvokesLLM(t *testing.T) {
	dir := t.TempDir() // no manifests at all
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repoID := createTestRepo(t, q, dir)

	stub := &stubLLMDetector{guesses: []agent.LanguageGuess{{ID: "python", Version: "3.12"}}}
	router, _ := setupDetectLanguagesRouter(t, q, stub)

	w := postDetectLanguages(t, router, repoID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp detectLanguagesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stub.calls != 1 {
		t.Errorf("LLM stub invoked %d times, want 1 for an empty scan", stub.calls)
	}
	if !resp.UsedLLM {
		t.Errorf("used_llm = false, want true")
	}
	if len(resp.Suggestions) != 1 || resp.Suggestions[0].ID != "python" || resp.Suggestions[0].Version != "3.12" {
		t.Errorf("unexpected suggestions: %+v", resp.Suggestions)
	}
}

// TestDetectLanguages_AmbiguousScan_InvokesLLM covers the "scan found a
// range, not a pinned version" gap (a package.json with engines.node set to
// a range) — the LLM fallback must still be invoked even though the scan
// found *something*.
func TestDetectLanguages_AmbiguousScan_InvokesLLM(t *testing.T) {
	dir := t.TempDir()
	pkg := `{"engines": {"node": ">=18 <21"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repoID := createTestRepo(t, q, dir)

	stub := &stubLLMDetector{guesses: []agent.LanguageGuess{{ID: "node", Version: "20"}}}
	router, _ := setupDetectLanguagesRouter(t, q, stub)

	w := postDetectLanguages(t, router, repoID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if stub.calls != 1 {
		t.Errorf("LLM stub invoked %d times, want 1 for an ambiguous scan result", stub.calls)
	}
	var resp detectLanguagesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.UsedLLM {
		t.Errorf("used_llm = false, want true")
	}
}

// TestDetectLanguages_HostileLLMOutput_Rejected is the non-negotiable
// security property from PLAN5.md: model-generated output is untrusted and
// must pass agent.ParseRuntimeLanguages before it can reach a response.
// Covers an unknown id disguised as a flag, a version carrying a shell
// injection payload, and a plausible-looking but non-allowlisted id — none
// of these may be stored or returned; all must degrade to the scan result.
func TestDetectLanguages_HostileLLMOutput_Rejected(t *testing.T) {
	cases := []struct {
		name    string
		guesses []agent.LanguageGuess
	}{
		{"flag-as-id", []agent.LanguageGuess{{ID: "--privileged", Version: "1"}}},
		{"shell-injection-version", []agent.LanguageGuess{{ID: "go", Version: "; rm -rf /"}}},
		{"unknown-language-id", []agent.LanguageGuess{{ID: "cobol", Version: "1985"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir() // empty scan -> LLM path reached
			db := openTestDB(t)
			q := gen.New(db.SQL())
			repoID := createTestRepo(t, q, dir)

			stub := &stubLLMDetector{guesses: tc.guesses}
			router, _ := setupDetectLanguagesRouter(t, q, stub)

			w := postDetectLanguages(t, router, repoID)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 (hostile output must degrade, not error), got %d: %s", w.Code, w.Body.String())
			}
			var resp detectLanguagesResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.UsedLLM {
				t.Errorf("used_llm = true, want false — hostile output must not count as a used LLM suggestion")
			}
			if len(resp.Suggestions) != 0 {
				t.Errorf("suggestions = %+v, want empty — hostile output must be dropped, not surfaced", resp.Suggestions)
			}
			for _, s := range resp.Suggestions {
				if s.ID == tc.guesses[0].ID {
					t.Errorf("hostile id %q leaked into suggestions", tc.guesses[0].ID)
				}
			}
		})
	}
}

// TestDetectLanguages_MalformedLLMOutput_DegradesToScan covers CLI output
// that isn't the expected {id,version}[] shape at all — the LLMDetector
// itself is expected to surface this as an error (see
// ClaudeLanguageDetector.DetectLanguages / parseDetectLangOutput), which
// must degrade to the scan result rather than error the endpoint.
func TestDetectLanguages_MalformedLLMOutput_DegradesToScan(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repoID := createTestRepo(t, q, dir)

	stub := &stubLLMDetector{err: fmt.Errorf("parse language guesses: unexpected end of JSON input")}
	router, _ := setupDetectLanguagesRouter(t, q, stub)

	w := postDetectLanguages(t, router, repoID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp detectLanguagesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UsedLLM {
		t.Errorf("used_llm = true, want false when the LLM call failed to parse")
	}
	if len(resp.Suggestions) != 0 {
		t.Errorf("suggestions = %+v, want empty (empty scan, failed LLM)", resp.Suggestions)
	}
}

// TestDetectLanguages_LLMError_DegradesToScan_StillReturns200 covers a
// timeout/no-provider-configured/CLI-invocation-failure case: the endpoint
// must still return 200 with the scan's own result, never a 5xx.
func TestDetectLanguages_LLMError_DegradesToScan_StillReturns200(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"x\"\n"), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repoID := createTestRepo(t, q, dir)

	stub := &stubLLMDetector{err: fmt.Errorf("claude detect-languages: context deadline exceeded")}
	router, _ := setupDetectLanguagesRouter(t, q, stub)

	w := postDetectLanguages(t, router, repoID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even on LLM timeout, got %d: %s", w.Code, w.Body.String())
	}
	var resp detectLanguagesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UsedLLM {
		t.Errorf("used_llm = true, want false")
	}
	// Cargo.toml present with no rust-version is still a scan suggestion
	// (presence-without-version, ambiguous) even though the LLM call failed.
	if len(resp.Suggestions) != 1 || resp.Suggestions[0].ID != "rust" {
		t.Errorf("unexpected suggestions after LLM failure: %+v", resp.Suggestions)
	}
}

// TestDetectLanguages_NilDetector_NoProviderConfigured covers "no LLM
// provider configured" — a nil LLMDetector must be skipped silently rather
// than panicking or erroring.
func TestDetectLanguages_NilDetector_NoProviderConfigured(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repoID := createTestRepo(t, q, dir)

	router, _ := setupDetectLanguagesRouter(t, q, nil)

	w := postDetectLanguages(t, router, repoID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp detectLanguagesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UsedLLM {
		t.Errorf("used_llm = true, want false with no detector configured")
	}
}

// TestDetectLanguages_NeverPersists is the plan's other non-negotiable: this
// endpoint must never write repos.runtime_languages, even when the LLM
// fallback returns a valid, confident suggestion. Only a subsequent PATCH
// from the user may persist anything.
func TestDetectLanguages_NeverPersists(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	repoID := createTestRepo(t, q, dir)

	before, err := q.GetRepo(context.Background(), repoID)
	if err != nil {
		t.Fatalf("get repo before: %v", err)
	}
	if before.RuntimeLanguages != "" {
		t.Fatalf("test setup invariant violated: runtime_languages should start empty, got %q", before.RuntimeLanguages)
	}

	stub := &stubLLMDetector{guesses: []agent.LanguageGuess{{ID: "go", Version: "1.26"}}}
	router, _ := setupDetectLanguagesRouter(t, q, stub)

	w := postDetectLanguages(t, router, repoID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	after, err := q.GetRepo(context.Background(), repoID)
	if err != nil {
		t.Fatalf("get repo after: %v", err)
	}
	if after.RuntimeLanguages != before.RuntimeLanguages {
		t.Errorf("runtime_languages changed after calling detect-languages: before=%q after=%q", before.RuntimeLanguages, after.RuntimeLanguages)
	}
}

// TestDetectLanguages_NotFound verifies the 404 for an unknown repo id.
func TestDetectLanguages_NotFound(t *testing.T) {
	db := openTestDB(t)
	q := gen.New(db.SQL())
	router, _ := setupDetectLanguagesRouter(t, q, &stubLLMDetector{})

	w := postDetectLanguages(t, router, uuid.NewString())
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
