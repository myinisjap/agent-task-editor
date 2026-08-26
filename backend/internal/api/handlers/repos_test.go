package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// setupReposRouter returns a chi router wired with the repos routes and the
// underlying gen.Queries so individual tests can pre-seed the database.
func setupReposRouter(t *testing.T, repoBaseDir string) (http.Handler, *gen.Queries) {
	t.Helper()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, repoBaseDir, nil, "")

	r := chi.NewRouter()
	r.Post("/repos", h.Create)
	r.Get("/repos", h.List)
	r.Get("/repos/{id}", h.Get)
	r.Delete("/repos/{id}", h.Delete)
	r.Get("/repos/{id}/tree", h.Tree)
	r.Get("/repos/{id}/devcontainer", h.Devcontainer)
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
	h := handlers.NewReposHandler(q, base, nil, "")
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
	h := handlers.NewReposHandler(q, base, nil, "")
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

// TestReposUpdate_IssueWritebackLabelRoundTrip verifies issue_writeback_label
// round-trips through create + PATCH, survives an unrelated PATCH, and that
// omitting it on create defaults to "" (which writeback.go falls back from).
func TestReposUpdate_IssueWritebackLabelRoundTrip(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil, "")
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)

	repoDir := filepath.Join(base, "myorg", "myrepo2")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name":                  "myorg/myrepo2",
		"path":                  repoDir,
		"remote_url":            "https://github.com/myorg/myrepo2",
		"issue_writeback_label": "wip",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)
	if got := repo["issue_writeback_label"]; got != "wip" {
		t.Errorf("issue_writeback_label after create = %v, want %q", got, "wip")
	}

	// A PATCH that doesn't mention the field must not reset it.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed2"})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_writeback_label"]; got != "wip" {
		t.Errorf("issue_writeback_label after unrelated patch = %v, want %q", got, "wip")
	}

	// PATCH can change it to a different label.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"issue_writeback_label": "in progress"})
	if w.Code != http.StatusOK {
		t.Fatalf("patch 2: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_writeback_label"]; got != "in progress" {
		t.Errorf("issue_writeback_label after patch = %v, want %q", got, "in progress")
	}

	// PATCH can clear it back to "" (default).
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"issue_writeback_label": ""})
	if w.Code != http.StatusOK {
		t.Fatalf("patch 3: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_writeback_label"]; got != "" {
		t.Errorf("issue_writeback_label after clearing = %v, want empty", got)
	}
}

// TestReposCreate_PrReviewAutoTransitionRequiresRemote verifies that enabling
// PR-review auto-transition without a GitHub remote_url is rejected, mirroring
// TestReposCreate_IssueWritebackRequiresRemote.
func TestReposCreate_PrReviewAutoTransitionRequiresRemote(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	// No remote_url → 400.
	w := postJSON(t, router, "/repos", map[string]any{
		"name":                              "myorg/myrepo",
		"path":                              repoDir,
		"pr_review_auto_transition_enabled": true,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 without remote_url, got %d: %s", w.Code, w.Body.String())
	}

	// Remote URL present → fine.
	w = postJSON(t, router, "/repos", map[string]any{
		"name":                              "myorg/myrepo",
		"path":                              repoDir,
		"remote_url":                        "https://github.com/myorg/myrepo",
		"pr_review_auto_transition_enabled": true,
	})
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201 with remote_url, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["pr_review_auto_transition_enabled"]; got != float64(1) {
		t.Errorf("pr_review_auto_transition_enabled = %v, want 1", got)
	}
}

// TestReposUpdate_PrReviewAutoTransitionRoundTrip enables PR-review
// auto-transition via PATCH and checks the setting persists (and survives a
// PATCH that doesn't mention it), mirroring TestReposUpdate_IssueWritebackRoundTrip.
func TestReposUpdate_PrReviewAutoTransitionRoundTrip(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil, "")
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
	if got := repo["pr_review_auto_transition_enabled"]; got != float64(0) {
		t.Errorf("pr_review_auto_transition_enabled default = %v, want 0", got)
	}

	// Enable PR-review auto-transition.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{
		"pr_review_auto_transition_enabled": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["pr_review_auto_transition_enabled"]; got != float64(1) {
		t.Errorf("pr_review_auto_transition_enabled = %v, want 1", got)
	}

	// A PATCH that doesn't mention the field must not reset it.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("patch 2: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["pr_review_auto_transition_enabled"]; got != float64(1) {
		t.Errorf("pr_review_auto_transition_enabled after unrelated patch = %v, want 1", got)
	}

	// Disabling clears the flag.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"pr_review_auto_transition_enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("patch 3: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["pr_review_auto_transition_enabled"]; got != float64(0) {
		t.Errorf("pr_review_auto_transition_enabled after disable = %v, want 0", got)
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

// TestReposList_Pagination verifies limit/after cursor pagination walks the
// full repo list without dupes or gaps.
func TestReposList_Pagination(t *testing.T) {
	router, q := setupReposRouter(t, "")
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{
			ID:   uuid.NewString(),
			Name: "repo",
			Path: t.TempDir(),
		}); err != nil {
			t.Fatalf("create repo: %v", err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		qs := "?limit=2"
		if cursor != "" {
			qs += "&after=" + cursor
		}
		req := httptest.NewRequest(http.MethodGet, "/repos"+qs, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET repos%s: expected 200, got %d: %s", qs, w.Code, w.Body)
		}
		var page []gen.Repo
		if err := json.NewDecoder(w.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page) > 2 {
			t.Fatalf("page returned %d repos, expected <= 2", len(page))
		}
		for _, repo := range page {
			if seen[repo.ID] {
				t.Fatalf("repo %s returned on more than one page", repo.ID)
			}
			seen[repo.ID] = true
		}
		next := w.Header().Get("X-Next-Cursor")
		if next == "" {
			break
		}
		cursor = next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 5 {
		t.Fatalf("expected to page through all 5 repos, saw %d", len(seen))
	}
}

// ---------------------------------------------------------------------------
// Issue sync update policy / gone action / gone label / comment sync
// (issue #264 phase 4) — see the CRITICAL hazard note in
// api/handlers/repos.go: these four columns must round-trip real values on
// both Create and Update, and an unrelated PATCH must never reset them.
// ---------------------------------------------------------------------------

// TestReposCreate_IssueSyncPolicyFieldsRoundTrip verifies the four new fields
// accept explicit values on create and default correctly when omitted.
func TestReposCreate_IssueSyncPolicyFieldsRoundTrip(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	// Omitted → documented defaults ('gate' / 'flag'), not Go zero values.
	w := postJSON(t, router, "/repos", map[string]any{
		"name": "myorg/myrepo",
		"path": repoDir,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_sync_update_policy"]; got != "gate" {
		t.Errorf("issue_sync_update_policy default = %v, want %q", got, "gate")
	}
	if got := repo["issue_sync_gone_action"]; got != "flag" {
		t.Errorf("issue_sync_gone_action default = %v, want %q", got, "flag")
	}
	if got := repo["issue_sync_gone_label"]; got != "" {
		t.Errorf("issue_sync_gone_label default = %v, want empty", got)
	}
	if got := repo["issue_comment_sync_enabled"]; got != float64(0) {
		t.Errorf("issue_comment_sync_enabled default = %v, want 0", got)
	}

	// Explicit values round-trip.
	repoDir2 := filepath.Join(base, "myorg", "myrepo2")
	initBareGitRepo(t, repoDir2)
	w = postJSON(t, router, "/repos", map[string]any{
		"name":                       "myorg/myrepo2",
		"path":                       repoDir2,
		"issue_sync_update_policy":   "always",
		"issue_sync_gone_action":     "move",
		"issue_sync_gone_label":      "triage",
		"issue_comment_sync_enabled": true,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_sync_update_policy"]; got != "always" {
		t.Errorf("issue_sync_update_policy = %v, want %q", got, "always")
	}
	if got := repo["issue_sync_gone_action"]; got != "move" {
		t.Errorf("issue_sync_gone_action = %v, want %q", got, "move")
	}
	if got := repo["issue_sync_gone_label"]; got != "triage" {
		t.Errorf("issue_sync_gone_label = %v, want %q", got, "triage")
	}
	if got := repo["issue_comment_sync_enabled"]; got != float64(1) {
		t.Errorf("issue_comment_sync_enabled = %v, want 1", got)
	}
}

// TestReposCreate_InvalidIssueSyncUpdatePolicyRejected verifies an
// unrecognized issue_sync_update_policy value is a 400.
func TestReposCreate_InvalidIssueSyncUpdatePolicyRejected(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name":                     "myorg/myrepo",
		"path":                     repoDir,
		"issue_sync_update_policy": "sometimes",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid issue_sync_update_policy, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReposCreate_InvalidIssueSyncGoneActionRejected verifies an unrecognized
// issue_sync_gone_action value is a 400.
func TestReposCreate_InvalidIssueSyncGoneActionRejected(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name":                   "myorg/myrepo",
		"path":                   repoDir,
		"issue_sync_gone_action": "delete",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid issue_sync_gone_action, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReposCreate_GoneActionMoveRequiresLabel verifies issue_sync_gone_action
// "move" without issue_sync_gone_label is rejected with 400.
func TestReposCreate_GoneActionMoveRequiresLabel(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name":                   "myorg/myrepo",
		"path":                   repoDir,
		"issue_sync_gone_action": "move",
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for move without a label, got %d: %s", w.Code, w.Body.String())
	}

	// With a label, it's accepted.
	w = postJSON(t, router, "/repos", map[string]any{
		"name":                   "myorg/myrepo",
		"path":                   repoDir,
		"issue_sync_gone_action": "move",
		"issue_sync_gone_label":  "triage",
	})
	if w.Code != http.StatusCreated {
		t.Errorf("expected 201 for move with a label, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReposUpdate_IssueSyncPolicyFieldsRoundTripAndSurviveUnrelatedPatch is
// the hazard-#1 regression test: an unrelated PATCH (here, a rename) must
// preserve the four issue-sync-policy fields, not reset them to "" / 0.
// Mirrors TestReposUpdate_PrReviewAutoTransitionRoundTrip.
func TestReposUpdate_IssueSyncPolicyFieldsRoundTripAndSurviveUnrelatedPatch(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil, "")
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name": "myorg/myrepo",
		"path": repoDir,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)
	if got := repo["issue_sync_update_policy"]; got != "gate" {
		t.Fatalf("create default issue_sync_update_policy = %v, want %q", got, "gate")
	}
	if got := repo["issue_sync_gone_action"]; got != "flag" {
		t.Fatalf("create default issue_sync_gone_action = %v, want %q", got, "flag")
	}

	// Configure non-default values.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{
		"issue_sync_update_policy":   "always",
		"issue_sync_gone_action":     "move",
		"issue_sync_gone_label":      "triage",
		"issue_comment_sync_enabled": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["issue_sync_update_policy"]; got != "always" {
		t.Errorf("issue_sync_update_policy = %v, want %q", got, "always")
	}
	if got := repo["issue_sync_gone_action"]; got != "move" {
		t.Errorf("issue_sync_gone_action = %v, want %q", got, "move")
	}
	if got := repo["issue_sync_gone_label"]; got != "triage" {
		t.Errorf("issue_sync_gone_label = %v, want %q", got, "triage")
	}
	if got := repo["issue_comment_sync_enabled"]; got != float64(1) {
		t.Errorf("issue_comment_sync_enabled = %v, want 1", got)
	}

	// THE HAZARD: an unrelated PATCH (renaming the repo) must not reset these
	// fields back to "" / 0. Before the fix, gen.UpdateRepoParams was built
	// with a named-field struct literal that omitted these four fields
	// entirely, so every PATCH silently wiped them.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("unrelated patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["name"]; got != "renamed" {
		t.Fatalf("expected name to actually change to %q, got %v", "renamed", got)
	}
	if got := repo["issue_sync_update_policy"]; got != "always" {
		t.Errorf("issue_sync_update_policy after unrelated patch = %v, want %q (preserved)", got, "always")
	}
	if got := repo["issue_sync_gone_action"]; got != "move" {
		t.Errorf("issue_sync_gone_action after unrelated patch = %v, want %q (preserved)", got, "move")
	}
	if got := repo["issue_sync_gone_label"]; got != "triage" {
		t.Errorf("issue_sync_gone_label after unrelated patch = %v, want %q (preserved)", got, "triage")
	}
	if got := repo["issue_comment_sync_enabled"]; got != float64(1) {
		t.Errorf("issue_comment_sync_enabled after unrelated patch = %v, want 1 (preserved)", got)
	}
}

// TestReposUpdate_InvalidIssueSyncPolicyFieldsRejected verifies PATCH
// validates the two enum fields and the move/label requirement the same way
// Create does.
func TestReposUpdate_InvalidIssueSyncPolicyFieldsRejected(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil, "")
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{"name": "myorg/myrepo", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)

	w = patchJSON(t, router, "/repos/"+id, map[string]any{"issue_sync_update_policy": "sometimes"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid issue_sync_update_policy, got %d: %s", w.Code, w.Body.String())
	}

	w = patchJSON(t, router, "/repos/"+id, map[string]any{"issue_sync_gone_action": "delete"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid issue_sync_gone_action, got %d: %s", w.Code, w.Body.String())
	}

	w = patchJSON(t, router, "/repos/"+id, map[string]any{"issue_sync_gone_action": "move"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for move without a label, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// max_concurrent_runs (per-repo concurrency cap — issue #255)
// ---------------------------------------------------------------------------

// TestReposCreate_MaxConcurrentRunsValidation covers the create-time
// validation for the optional per-repo concurrency cap: omitted is fine
// (nil, "no cap"), a positive integer is fine, and 0/negative are rejected
// rather than silently treated as unlimited (see resolveMaxConcurrentRuns).
func TestReposCreate_MaxConcurrentRunsValidation(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	// Omitted → nil, accepted.
	repoDir := filepath.Join(base, "omitted")
	initBareGitRepo(t, repoDir)
	w := postJSON(t, router, "/repos", map[string]any{"name": "omitted", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("omitted: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if repo["max_concurrent_runs"] != nil {
		t.Errorf("omitted max_concurrent_runs = %v, want nil", repo["max_concurrent_runs"])
	}

	// Positive integer → accepted and persisted.
	repoDir2 := filepath.Join(base, "capped")
	initBareGitRepo(t, repoDir2)
	w = postJSON(t, router, "/repos", map[string]any{"name": "capped", "path": repoDir2, "max_concurrent_runs": 3})
	if w.Code != http.StatusCreated {
		t.Fatalf("positive: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["max_concurrent_runs"]; got != float64(3) {
		t.Errorf("max_concurrent_runs = %v, want 3", got)
	}

	// Zero → rejected.
	repoDir3 := filepath.Join(base, "zero")
	initBareGitRepo(t, repoDir3)
	w = postJSON(t, router, "/repos", map[string]any{"name": "zero", "path": repoDir3, "max_concurrent_runs": 0})
	if w.Code != http.StatusBadRequest {
		t.Errorf("zero: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Negative → rejected.
	repoDir4 := filepath.Join(base, "negative")
	initBareGitRepo(t, repoDir4)
	w = postJSON(t, router, "/repos", map[string]any{"name": "negative", "path": repoDir4, "max_concurrent_runs": -1})
	if w.Code != http.StatusBadRequest {
		t.Errorf("negative: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReposUpdate_MaxConcurrentRunsOmittedVsNullVsSet verifies the PATCH
// tri-state semantics for max_concurrent_runs: a PATCH that omits the field
// entirely leaves the stored value untouched; a PATCH with an explicit
// number sets/replaces it; a PATCH with an explicit `null` clears it back to
// nil ("use the global default").
func TestReposUpdate_MaxConcurrentRunsOmittedVsNullVsSet(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil, "")
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)

	repoDir := filepath.Join(base, "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{"name": "myrepo", "path": repoDir, "max_concurrent_runs": 2})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)
	if got := repo["max_concurrent_runs"]; got != float64(2) {
		t.Fatalf("initial max_concurrent_runs = %v, want 2", got)
	}

	// Omitted field on an unrelated PATCH must not touch the stored value.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("unrelated patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["max_concurrent_runs"]; got != float64(2) {
		t.Errorf("max_concurrent_runs after unrelated patch = %v, want unchanged 2", got)
	}

	// A new positive value replaces the existing cap.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"max_concurrent_runs": 5})
	if w.Code != http.StatusOK {
		t.Fatalf("set patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["max_concurrent_runs"]; got != float64(5) {
		t.Errorf("max_concurrent_runs after set = %v, want 5", got)
	}

	// Explicit null clears it back to nil.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"max_concurrent_runs": nil})
	if w.Code != http.StatusOK {
		t.Fatalf("clear patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if repo["max_concurrent_runs"] != nil {
		t.Errorf("max_concurrent_runs after clear = %v, want nil", repo["max_concurrent_runs"])
	}

	// Zero/negative are rejected on update too.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"max_concurrent_runs": 0})
	if w.Code != http.StatusBadRequest {
		t.Errorf("zero on update: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"max_concurrent_runs": -2})
	if w.Code != http.StatusBadRequest {
		t.Errorf("negative on update: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------- Get / Delete / Tree ----------

func TestReposGet_OK(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)
	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{"name": "myorg/myrepo", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/repos/"+id, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if got["id"] != id || got["name"] != "myorg/myrepo" {
		t.Errorf("expected repo myorg/myrepo with id %s, got %+v", id, got)
	}
}

func TestReposGet_Unknown(t *testing.T) {
	router, _ := setupReposRouter(t, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/repos/"+uuid.NewString(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestReposDelete_OK(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)
	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{"name": "myorg/myrepo", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)

	req := httptest.NewRequest(http.MethodDelete, "/repos/"+id, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/repos/"+id, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected deleted repo to 404 on Get, got %d", w.Code)
	}
}

// TestReposDelete_NotFound verifies deleting an id that doesn't exist
// returns 404 rather than a false-positive 204.
func TestReposDelete_NotFound(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	req := httptest.NewRequest(http.MethodDelete, "/repos/ghost", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReposDelete_ConflictWhenTasksReference verifies deleting a repo that
// still has tasks pointing at it returns 409 with the referencing task
// count instead of a raw 500 "FOREIGN KEY constraint failed" from the DB.
func TestReposDelete_ConflictWhenTasksReference(t *testing.T) {
	base := t.TempDir()
	router, q := setupReposRouter(t, base)
	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{"name": "myorg/myrepo", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	repoID, _ := created["id"].(string)

	ctx := context.Background()
	wfs, err := q.ListWorkflows(ctx)
	if err != nil || len(wfs) == 0 {
		t.Fatalf("list workflows: %v", err)
	}
	if _, err := q.CreateTask(ctx, gen.CreateTaskParams{
		ID: uuid.NewString(), Title: "t", WorkflowID: wfs[0].ID, RepoID: repoID, Label: "work",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/repos/"+repoID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// The repo must still exist — the delete must not have partially applied.
	req = httptest.NewRequest(http.MethodGet, "/repos/"+repoID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected repo to still exist after conflicting delete, got %d", w.Code)
	}
}

func TestReposTree_OK(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)
	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)
	if out, err := exec.Command("git", "-C", repoDir, "config", "user.email", "t@example.com").CombinedOutput(); err != nil {
		t.Fatalf("git config user.email: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "config", "user.name", "test").CombinedOutput(); err != nil {
		t.Fatalf("git config user.name: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "commit", "-m", "add readme").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	w := postJSON(t, router, "/repos", map[string]any{"name": "myorg/myrepo", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/repos/"+id+"/tree", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Ref   string   `json:"ref"`
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode tree response: %v", err)
	}
	if got.Ref != "HEAD" {
		t.Errorf("expected default ref HEAD, got %q", got.Ref)
	}
	found := false
	for _, f := range got.Files {
		if f == "README.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected README.md in tree files, got %v", got.Files)
	}
}

func TestReposTree_Unknown(t *testing.T) {
	router, _ := setupReposRouter(t, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/repos/"+uuid.NewString()+"/tree", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestReposTree_InvalidRef(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)
	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{"name": "myorg/myrepo", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/repos/"+id+"/tree?ref="+url.QueryEscape("--no-index"), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestReposCreate_AsyncCloneFailure_MarksRepoError drives startAsyncClone's
// full lifecycle by omitting `path` and pointing `remote_url` at an
// unreachable local address (127.0.0.1 on a closed port), so `git clone`
// fails fast without any real network access. The handler must return 201
// immediately (clone runs in the background) with clone_status "cloning",
// then the background goroutine marks it "error" with a message once the
// clone fails.
func TestReposCreate_AsyncCloneFailure_MarksRepoError(t *testing.T) {
	base := t.TempDir()
	router, q := setupReposRouter(t, base)

	w := postJSON(t, router, "/repos", map[string]any{
		"name":       "acme/unreachable",
		"remote_url": "https://127.0.0.1:1/acme/unreachable.git",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created map[string]any
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if got := created["clone_status"]; got != "cloning" {
		t.Fatalf("expected clone_status 'cloning' immediately after create, got %v", got)
	}
	id, _ := created["id"].(string)

	deadline := time.Now().Add(20 * time.Second)
	var repo gen.Repo
	for {
		var err error
		repo, err = q.GetRepo(context.Background(), id)
		if err != nil {
			t.Fatalf("get repo: %v", err)
		}
		if repo.CloneStatus == "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for clone_status to become 'error', last status: %q", repo.CloneStatus)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if repo.CloneError == "" {
		t.Errorf("expected a non-empty clone_error message")
	}
}

// ---------------------------------------------------------------------------
// devcontainer_json (T6 API surface — see runtime-images.md's round-2 plan)
// ---------------------------------------------------------------------------

// TestReposCreate_DevcontainerJsonRoundTrip verifies a valid devcontainer_json
// JSON object round-trips through Create, and empty string (the default) is
// accepted as "not configured".
func TestReposCreate_DevcontainerJsonRoundTrip(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	// Omitted -> empty string default.
	repoDir := filepath.Join(base, "omitted")
	initBareGitRepo(t, repoDir)
	w := postJSON(t, router, "/repos", map[string]any{"name": "omitted", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("omitted: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["devcontainer_json"]; got != "" {
		t.Errorf("omitted devcontainer_json = %v, want empty", got)
	}

	// A valid JSON object round-trips.
	repoDir2 := filepath.Join(base, "withdc")
	initBareGitRepo(t, repoDir2)
	dc := `{"image":"golang:1.26"}`
	w = postJSON(t, router, "/repos", map[string]any{"name": "withdc", "path": repoDir2, "devcontainer_json": dc})
	if w.Code != http.StatusCreated {
		t.Fatalf("withdc: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["devcontainer_json"]; got != dc {
		t.Errorf("devcontainer_json = %v, want %q", got, dc)
	}
}

// TestReposCreate_DevcontainerJsonMalformedRejected verifies malformed JSON
// and non-object JSON (an array, a bare string) are both rejected with 400
// naming the parse problem, per the trust-boundary requirement: a bad blob
// must fail at save time, not at dispatch time inside a container.
func TestReposCreate_DevcontainerJsonMalformedRejected(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	cases := []struct {
		name string
		raw  string
	}{
		{"unterminated object", `{"image": "golang:1.26"`},
		{"bare string", `"golang:1.26"`},
		{"json array", `["golang:1.26"]`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := filepath.Join(base, fmt.Sprintf("bad%d", i))
			initBareGitRepo(t, repoDir)
			w := postJSON(t, router, "/repos", map[string]any{
				"name":              fmt.Sprintf("bad%d", i),
				"path":              repoDir,
				"devcontainer_json": tc.raw,
			})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "devcontainer_json") {
				t.Errorf("expected error to name devcontainer_json, got: %s", w.Body.String())
			}
		})
	}
}

// TestReposUpdate_DevcontainerJsonOmittedPreservesStoredValue is the
// regression this change most needs to guard: a PATCH that omits
// devcontainer_json entirely must preserve the stored value (not silently
// wipe it), mirroring runtime_image's *string preserve-if-omitted pattern.
// A PATCH that provides a new value replaces it; malformed JSON on update is
// rejected the same way as on create.
func TestReposUpdate_DevcontainerJsonOmittedPreservesStoredValue(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil, "")
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	dc := `{"image":"golang:1.26"}`
	w := postJSON(t, router, "/repos", map[string]any{
		"name":              "myorg/myrepo",
		"path":              repoDir,
		"devcontainer_json": dc,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)
	if got := repo["devcontainer_json"]; got != dc {
		t.Fatalf("initial devcontainer_json = %v, want %q", got, dc)
	}

	// A PATCH that doesn't mention the field must not reset it.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("unrelated patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["devcontainer_json"]; got != dc {
		t.Errorf("devcontainer_json after unrelated patch = %v, want unchanged %q", got, dc)
	}

	// A new value replaces the existing one.
	dc2 := `{"image":"python:3.12"}`
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"devcontainer_json": dc2})
	if w.Code != http.StatusOK {
		t.Fatalf("set patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["devcontainer_json"]; got != dc2 {
		t.Errorf("devcontainer_json after set = %v, want %q", got, dc2)
	}

	// Explicit empty string clears it back to "not configured".
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"devcontainer_json": ""})
	if w.Code != http.StatusOK {
		t.Fatalf("clear patch: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if got := repo["devcontainer_json"]; got != "" {
		t.Errorf("devcontainer_json after clear = %v, want empty", got)
	}

	// Malformed JSON on update is rejected the same way as on create.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"devcontainer_json": "not json"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed on update: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GET /repos/{id}/devcontainer (effective-config endpoint)
// ---------------------------------------------------------------------------

// TestReposDevcontainer_SourceNone verifies a repo with neither runtime_image
// nor devcontainer_json nor a repo-committed devcontainer.json reports source
// "none" with an empty effective_json.
func TestReposDevcontainer_SourceNone(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{"name": "myorg/myrepo", "path": repoDir})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/repos/"+id+"/devcontainer", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if got := resp["source"]; got != "none" {
		t.Errorf("source = %v, want %q", got, "none")
	}
	if got := resp["effective_json"]; got != "" {
		t.Errorf("effective_json = %v, want empty", got)
	}
	if got := resp["repo_file_present"]; got != false {
		t.Errorf("repo_file_present = %v, want false", got)
	}
}

// TestReposDevcontainer_SourceDB verifies a repo with only devcontainer_json
// set (no repo-committed file, no runtime_image) reports source "db" and a
// non-empty effective_json (the mount/hardening contract merged in).
func TestReposDevcontainer_SourceDB(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	dc := `{"image":"golang:1.26"}`
	w := postJSON(t, router, "/repos", map[string]any{
		"name":              "myorg/myrepo",
		"path":              repoDir,
		"devcontainer_json": dc,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/repos/"+id+"/devcontainer", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if got := resp["source"]; got != "db" {
		t.Errorf("source = %v, want %q", got, "db")
	}
	if got, _ := resp["effective_json"].(string); got == "" {
		t.Errorf("expected non-empty effective_json, got empty")
	}
	if got := resp["repo_file_present"]; got != false {
		t.Errorf("repo_file_present = %v, want false", got)
	}
}

// TestReposDevcontainer_SourceRepoFileWinsOverDB verifies a repo with both a
// committed .devcontainer/devcontainer.json AND a DB-stored devcontainer_json
// reports source "repo_file" (repo file wins) and repo_file_present true,
// while the DB-stored config is not the one reflected in effective_json.
func TestReposDevcontainer_SourceRepoFileWinsOverDB(t *testing.T) {
	base := t.TempDir()
	router, _ := setupReposRouter(t, base)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	// Commit a .devcontainer/devcontainer.json in the repo checkout.
	dcDir := filepath.Join(repoDir, ".devcontainer")
	if err := os.MkdirAll(dcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoFileJSON := `{"image":"from-repo-file"}`
	if err := os.WriteFile(filepath.Join(dcDir, "devcontainer.json"), []byte(repoFileJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	dbJSON := `{"image":"from-db"}`
	w := postJSON(t, router, "/repos", map[string]any{
		"name":              "myorg/myrepo",
		"path":              repoDir,
		"devcontainer_json": dbJSON,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/repos/"+id+"/devcontainer", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if got := resp["source"]; got != "repo_file" {
		t.Errorf("source = %v, want %q", got, "repo_file")
	}
	if got := resp["repo_file_present"]; got != true {
		t.Errorf("repo_file_present = %v, want true", got)
	}
	effective, _ := resp["effective_json"].(string)
	if !strings.Contains(effective, "from-repo-file") {
		t.Errorf("effective_json should reflect the repo file's content, got: %s", effective)
	}
	if strings.Contains(effective, "from-db") {
		t.Errorf("effective_json should NOT reflect the DB-stored config when a repo file wins, got: %s", effective)
	}
}

// TestReposDevcontainer_SourceImageRef verifies a repo with runtime_image set
// reports source "image_ref" with empty effective_json — an explicit image
// ref always wins and skips the devcontainer build path entirely, even when
// devcontainer_json is also configured.
func TestReposDevcontainer_SourceImageRef(t *testing.T) {
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil, "")
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)
	router.Get("/repos/{id}/devcontainer", h.Devcontainer)

	repoDir := filepath.Join(base, "myorg", "myrepo")
	initBareGitRepo(t, repoDir)

	w := postJSON(t, router, "/repos", map[string]any{
		"name":              "myorg/myrepo",
		"path":              repoDir,
		"runtime_image":     "ghcr.io/me/img:1",
		"devcontainer_json": `{"image":"ignored"}`,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)

	req := httptest.NewRequest(http.MethodGet, "/repos/"+id+"/devcontainer", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if got := resp["source"]; got != "image_ref" {
		t.Errorf("source = %v, want %q", got, "image_ref")
	}
	if got := resp["effective_json"]; got != "" {
		t.Errorf("effective_json = %v, want empty when runtime_image wins", got)
	}
}

// TestReposDevcontainer_NotFound verifies GET .../devcontainer for an
// unknown repo id returns 404, mirroring TestReposGet_Unknown.
func TestReposDevcontainer_NotFound(t *testing.T) {
	router, _ := setupReposRouter(t, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/repos/"+uuid.NewString()+"/devcontainer", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
