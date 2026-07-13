package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// setupReposRouter returns a chi router wired with the repos routes and the
// underlying gen.Queries so individual tests can pre-seed the database.
func setupReposRouter(t *testing.T, repoBaseDir string) (http.Handler, *gen.Queries) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, repoBaseDir, nil)

	r := chi.NewRouter()
	r.Post("/repos", h.Create)
	r.Get("/repos", h.List)
	r.Get("/repos/{id}", h.Get)
	r.Delete("/repos/{id}", h.Delete)
	return r, q
}

// initBareGitRepo creates a minimal git repo at dir for tests that need a
// real on-disk repository (avoids actually cloning over the network).
func initBareGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Write a dummy commit so the repo is non-empty (some git commands need it).
	if out, err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
		// Non-fatal – rev-parse --git-dir works even without commits.
		_ = out
	}
}

// postJSON is a small helper that sends a JSON POST to the given router.
func postJSON(t *testing.T, router http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// putJSON is a small helper that sends a JSON PUT to the given router.
func putJSON(t *testing.T, router http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// parseGitHubName is package-private, so we test it indirectly via the Create
// handler's auto-name behaviour.  These tests exercise every URL format that
// should (and should not) trigger auto-fill.
// ---------------------------------------------------------------------------

// TestReposCreate_AutoNameFromGitHubHTTPS tests that submitting a GitHub HTTPS
// URL with no explicit name auto-derives "org/repo" and stores it.
func TestReposCreate_AutoNameFromGitHubHTTPS(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	// Pre-create a local git repo that the handler can verify.
	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		// name intentionally omitted — should be auto-derived
		"path":       repoDir,
		"remote_url": "https://github.com/myorg/myrepo",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	if err := json.NewDecoder(w.Body).Decode(&repo); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := repo["name"]; got != "myorg/myrepo" {
		t.Errorf("expected name 'myorg/myrepo', got %q", got)
	}
}

// TestReposCreate_AutoNameFromGitHubHTTPSdotGit ensures .git suffix is stripped.
func TestReposCreate_AutoNameFromGitHubHTTPSdotGit(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "acme", "widget")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"path":       repoDir,
		"remote_url": "https://github.com/acme/widget.git",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["name"]; got != "acme/widget" {
		t.Errorf("expected name 'acme/widget', got %q", got)
	}
}

// TestReposCreate_AutoNameFromGitHubSSH exercises the git@ SSH format.
func TestReposCreate_AutoNameFromGitHubSSH(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "corp", "backend")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"path":       repoDir,
		"remote_url": "git@github.com:corp/backend.git",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["name"]; got != "corp/backend" {
		t.Errorf("expected name 'corp/backend', got %q", got)
	}
}

// TestReposCreate_AutoNameFromGitHubSSHNoGit exercises SSH without .git suffix.
func TestReposCreate_AutoNameFromGitHubSSHNoGit(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "dev", "frontend")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"path":       repoDir,
		"remote_url": "git@github.com:dev/frontend",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["name"]; got != "dev/frontend" {
		t.Errorf("expected name 'dev/frontend', got %q", got)
	}
}

// TestReposCreate_NonGitHubURLRequiresName verifies that a non-GitHub URL does
// not auto-derive a name and the request fails (400) when name is empty.
func TestReposCreate_NonGitHubURLRequiresName(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		// name omitted; non-GitHub URL can't auto-derive org/repo name
		"path":       repoDir,
		"remote_url": "https://gitlab.com/org/repo",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-GitHub URL without name, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReposCreate_ExplicitNameNotOverwritten ensures a user-supplied name is
// never replaced by the auto-derived name.
func TestReposCreate_ExplicitNameNotOverwritten(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name":       "custom-name",
		"path":       repoDir,
		"remote_url": "https://github.com/myorg/myrepo",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["name"]; got != "custom-name" {
		t.Errorf("expected name 'custom-name' (not overwritten), got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Auto-clone path validation tests
// ---------------------------------------------------------------------------

// TestReposCreate_MissingPathAndRemoteURL verifies the handler returns 400
// when neither path nor remote_url is supplied.
func TestReposCreate_MissingPathAndRemoteURL(t *testing.T) {
	router, _ := setupReposRouter(t, "")
	w := postJSON(t, router, "/repos", map[string]any{
		"name": "some-repo",
		// no path, no remote_url
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "path or remote_url is required") {
		t.Errorf("unexpected error body: %s", w.Body.String())
	}
}

// TestReposCreate_AutoCloneRequiresBaseDir checks the handler returns 400
// when auto-clone is needed but repoBaseDir is not configured.
func TestReposCreate_AutoCloneRequiresBaseDir(t *testing.T) {
	// repoBaseDir is empty string → not configured.
	router, _ := setupReposRouter(t, "")
	w := postJSON(t, router, "/repos", map[string]any{
		"remote_url": "https://github.com/org/repo",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "repo_base_dir must be configured") {
		t.Errorf("unexpected error body: %s", w.Body.String())
	}
}

// TestReposCreate_PathTraversalViaNamRejected ensures that a crafted name
// containing ".." cannot escape repoBaseDir. The check must fire BEFORE
// MkdirAll or git clone, so no directories should be created.
func TestReposCreate_PathTraversalViaNamRejected(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	// Choose a target outside base that must not be created.
	outside := filepath.Join(base, "escaped")

	w := postJSON(t, router, "/repos", map[string]any{
		"name":       "../escaped",
		"remote_url": "https://github.com/org/repo",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "outside the allowed base directory") {
		t.Errorf("unexpected error body: %s", w.Body.String())
	}
	// Critical: the directory must NOT have been created on disk.
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("path traversal: directory %q was created on disk before the check fired", outside)
	}
}

// TestReposCreate_PathTraversalViaURLSegmentRejected exercises the fallback
// path where name is empty and the URL's last segment would escape base.
func TestReposCreate_PathTraversalViaURLSegmentRejected(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	// URL whose last segment (after stripping .git) resolves to "../leaked"
	// via filepath.Join cleaning.
	w := postJSON(t, router, "/repos", map[string]any{
		// No name — handler falls back to last URL segment.
		// We use a non-GitHub URL so name derivation stays empty.
		"name":       "../../leaked",
		"remote_url": "https://github.com/org/repo",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal via URL segment, got %d: %s", w.Code, w.Body.String())
	}
	// The leaked directory must not exist.
	leaked := filepath.Join(filepath.Dir(base), "leaked")
	if _, err := os.Stat(leaked); err == nil {
		t.Errorf("path traversal: directory %q was created on disk", leaked)
	}
}

// TestReposCreate_BadSchemeRejected validates that non-https/non-git@ URLs
// are rejected with a 400 before any side effects occur.
func TestReposCreate_BadSchemeRejected(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	cases := []string{
		"file:///etc/passwd",
		"ftp://example.com/repo",
		"ssh://github.com/org/repo",
	}
	for _, url := range cases {
		w := postJSON(t, router, "/repos", map[string]any{
			"name":       "test-repo",
			"remote_url": url,
		})
		if w.Code != http.StatusBadRequest {
			t.Errorf("URL %q: expected 400, got %d: %s", url, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "https://") {
			t.Errorf("URL %q: unexpected error body: %s", url, w.Body.String())
		}
	}
}

// TestReposCreate_PathOutsideBaseDirRejected verifies that supplying a local
// path that is outside repoBaseDir is rejected when repoBaseDir is configured.
func TestReposCreate_PathOutsideBaseDirRejected(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir() // a completely separate temp dir
	router, _ := setupReposRouter(t, base)

	// Create a valid git repo at the outside path.
	initBareGitRepo(t, outside)

	w := postJSON(t, router, "/repos", map[string]any{
		"name": "outside-repo",
		"path": outside,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path outside base dir, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "outside the allowed base directory") {
		t.Errorf("unexpected error body: %s", w.Body.String())
	}
}

// TestReposCreate_NotAGitRepo ensures a real directory that is not a git repo
// is rejected with a helpful error.
func TestReposCreate_NotAGitRepo(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	notARepo := filepath.Join(base, "notarepo")
	if err := os.MkdirAll(notARepo, 0o755); err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, router, "/repos", map[string]any{
		"name": "notarepo",
		"path": notARepo,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-git directory, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not a git repository") {
		t.Errorf("unexpected error body: %s", w.Body.String())
	}
}

// TestReposCreate_HappyPathWithExplicitPath tests the simple case: a valid
// local git repo with an explicit path and a name is successfully created.
func TestReposCreate_HappyPathWithExplicitPath(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name": "myrepo",
		"path": repoDir,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	if err := json.NewDecoder(w.Body).Decode(&repo); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if repo["name"] != "myrepo" {
		t.Errorf("unexpected name %q", repo["name"])
	}
	if repo["path"] != repoDir {
		t.Errorf("unexpected path %q", repo["path"])
	}
}

// ---------------------------------------------------------------------------
// Issue sync settings
// ---------------------------------------------------------------------------

// patchJSON is a small helper that sends a JSON PATCH to the given router.
func patchJSON(t *testing.T, router http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestReposCreate_IssueSyncRequiresRemoteAndWorkflow verifies that enabling
// issue sync without a remote URL or without a workflow is rejected.
func TestReposCreate_IssueSyncRequiresRemoteAndWorkflow(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	// No remote_url → 400.
	w := postJSON(t, router, "/repos", map[string]any{
		"name":               "myorg/myrepo",
		"path":               repoDir,
		"issue_sync_enabled": true,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 without remote_url, got %d: %s", w.Code, w.Body.String())
	}

	// Remote but no workflow → 400.
	w = postJSON(t, router, "/repos", map[string]any{
		"name":               "myorg/myrepo",
		"path":               repoDir,
		"remote_url":         "https://github.com/myorg/myrepo",
		"issue_sync_enabled": true,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 without workflow, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "workflow") {
		t.Errorf("unexpected error body: %s", w.Body.String())
	}
}

// TestReposUpdate_IssueSyncRoundTrip enables issue sync via PATCH and checks
// the settings persist (and survive a PATCH that doesn't mention them).
func TestReposUpdate_IssueSyncRoundTrip(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil)
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	// Need a workflow to point at.
	wf, err := q.CreateWorkflow(t.Context(), gen.CreateWorkflowParams{
		ID: "wf-1", Name: "wf", Description: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	w := postJSON(t, router, "/repos", map[string]any{
		"name":       "myorg/myrepo",
		"path":       repoDir,
		"remote_url": "https://github.com/myorg/myrepo",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)

	// Enable issue sync.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{
		"workflow_id":        wf.ID,
		"issue_sync_enabled": true,
		"issue_sync_label":   " agent-ok ",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_sync_enabled"]; got != float64(1) {
		t.Errorf("issue_sync_enabled = %v, want 1", got)
	}
	if got := repo["issue_sync_label"]; got != "agent-ok" {
		t.Errorf("issue_sync_label = %q, want trimmed 'agent-ok'", got)
	}

	// A PATCH that doesn't mention the fields must not reset them.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("patch 2: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_sync_enabled"]; got != float64(1) {
		t.Errorf("issue_sync_enabled after unrelated patch = %v, want 1", got)
	}
	if got := repo["issue_sync_label"]; got != "agent-ok" {
		t.Errorf("issue_sync_label after unrelated patch = %q, want 'agent-ok'", got)
	}

	// Disabling requires no remote/workflow and clears the flag.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"issue_sync_enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("patch 3: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_sync_enabled"]; got != float64(0) {
		t.Errorf("issue_sync_enabled after disable = %v, want 0", got)
	}
}

// TestReposCreate_IssueWritebackRequiresRemote verifies that enabling issue
// write-back without a GitHub remote_url is rejected. Unlike issue sync, it
// does NOT require a workflow (write-back doesn't create tasks).
func TestReposCreate_IssueWritebackRequiresRemote(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	// No remote_url → 400.
	w := postJSON(t, router, "/repos", map[string]any{
		"name":                    "myorg/myrepo",
		"path":                    repoDir,
		"issue_writeback_enabled": true,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 without remote_url, got %d: %s", w.Code, w.Body.String())
	}

	// Remote URL present, no workflow → still fine (write-back doesn't need one).
	w = postJSON(t, router, "/repos", map[string]any{
		"name":                    "myorg/myrepo",
		"path":                    repoDir,
		"remote_url":              "https://github.com/myorg/myrepo",
		"issue_writeback_enabled": true,
	})
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201 with remote_url but no workflow, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReposUpdate_IssueWritebackRoundTrip enables issue write-back via PATCH
// and checks the setting persists (and survives a PATCH that doesn't mention
// it), mirroring TestReposUpdate_IssueSyncRoundTrip.
func TestReposUpdate_IssueWritebackRoundTrip(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil)
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name":       "myorg/myrepo",
		"path":       repoDir,
		"remote_url": "https://github.com/myorg/myrepo",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)

	// Enable issue write-back.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{
		"issue_writeback_enabled": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_writeback_enabled"]; got != float64(1) {
		t.Errorf("issue_writeback_enabled = %v, want 1", got)
	}

	// A PATCH that doesn't mention the field must not reset it.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("patch 2: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_writeback_enabled"]; got != float64(1) {
		t.Errorf("issue_writeback_enabled after unrelated patch = %v, want 1", got)
	}

	// Disabling clears the flag.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"issue_writeback_enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("patch 3: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_writeback_enabled"]; got != float64(0) {
		t.Errorf("issue_writeback_enabled after disable = %v, want 0", got)
	}
}

// TestReposList_Empty verifies a 200 with an empty array when no repos exist.
func TestReposList_Empty(t *testing.T) {
	router, _ := setupReposRouter(t, "")
	req := httptest.NewRequest(http.MethodGet, "/repos", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var repos []any
	if err := json.NewDecoder(w.Body).Decode(&repos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("expected empty list, got %d items", len(repos))
	}
}
