package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

func TestCreatePR_ReviewSourceTaskRejectedBeforePush(t *testing.T) {
	r, q, wfID, repoID := setupTaskRouter(t)
	task, err := q.CreateSourcedTask(context.Background(), gen.CreateSourcedTaskParams{
		ID: uuid.NewString(), Title: "PR review", Type: "chore", Label: "work", RepoID: repoID,
		WorkflowID: wfID, Attachments: "[]", Source: "github_pr", SourceRef: "o/r#9",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = q.SetTaskWorktree(context.Background(), gen.SetTaskWorktreeParams{Branch: "pr-review-9-x", BaseRef: "origin/main", ID: task.ID})
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/pr", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "review-only") {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
}

func newReposRouterWithPatch(t *testing.T) (http.Handler, *gen.Queries, string) {
	t.Helper()
	base := t.TempDir()
	db := openTestDB(t)
	q := gen.New(db.SQL())
	h := handlers.NewReposHandler(q, base, nil)
	router := chi.NewRouter()
	router.Post("/repos", h.Create)
	router.Patch("/repos/{id}", h.Update)
	dir := filepath.Join(base, "o", "r")
	initBareGitRepo(t, dir)
	return router, q, dir
}

func TestReposPRReview_DefaultsPatchPreservationAndValidation(t *testing.T) {
	router, q, dir := newReposRouterWithPatch(t)
	wf, _ := q.CreateWorkflow(t.Context(), gen.CreateWorkflowParams{ID: "wf-pr", Name: "prwf"})
	for _, l := range []struct {
		name          string
		ignore, final int64
	}{{"review", 0, 0}, {"gate", 1, 0}, {"done", 0, 1}} {
		if _, err := q.CreateWorkflowLabel(t.Context(), gen.CreateWorkflowLabelParams{
			ID: uuid.NewString(), WorkflowID: wf.ID, Name: l.name, DisplayName: l.name, Color: "#000",
			AgentIgnore: l.ignore, IsTerminal: l.final, VerifyMode: "inherit",
		}); err != nil {
			t.Fatal(err)
		}
	}

	w := postJSON(t, router, "/repos", map[string]any{"name": "o/r", "path": dir, "remote_url": "https://github.com/o/r"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var repo map[string]any
	_ = json.NewDecoder(w.Body).Decode(&repo)
	id := repo["id"].(string)
	if repo["pr_review_enabled"] != false || repo["pr_review_skip_drafts"] != true {
		t.Fatalf("defaults: %v", repo)
	}
	if a, _ := json.Marshal(repo["pr_review_author_assocs"]); string(a) != `["OWNER","MEMBER","COLLABORATOR"]` {
		t.Fatalf("assocs: %s", a)
	}

	w = patchJSON(t, router, "/repos/"+id, map[string]any{
		"pr_review_enabled": true, "pr_review_label": " agent-review ", "pr_review_author_assocs": []string{"owner", "contributor"},
		"pr_review_workflow_id": wf.ID, "pr_review_target_label": "review", "pr_review_skip_drafts": false,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	// ADR 0036: a PATCH that omits the fields must preserve them.
	w = patchJSON(t, router, "/repos/"+id, map[string]any{"name": "renamed"})
	if w.Code != http.StatusOK {
		t.Fatalf("patch2: %d %s", w.Code, w.Body)
	}
	repo = map[string]any{}
	_ = json.NewDecoder(w.Body).Decode(&repo)
	if repo["pr_review_enabled"] != true || repo["pr_review_label"] != "agent-review" || repo["pr_review_skip_drafts"] != false ||
		repo["pr_review_workflow_id"] != wf.ID || repo["pr_review_target_label"] != "review" {
		t.Fatalf("not preserved: %v", repo)
	}
	if a, _ := json.Marshal(repo["pr_review_author_assocs"]); string(a) != `["OWNER","CONTRIBUTOR"]` {
		t.Fatalf("assocs: %s", a)
	}

	for name, body := range map[string]map[string]any{
		"bad assoc":         {"pr_review_author_assocs": []string{"STRANGER"}},
		"unknown workflow":  {"pr_review_workflow_id": "nope"},
		"terminal label":    {"pr_review_target_label": "done"},
		"agent_ignore":      {"pr_review_target_label": "gate"},
		"label not in flow": {"pr_review_target_label": "missing"},
	} {
		if w := patchJSON(t, router, "/repos/"+id, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %s", name, w.Code, w.Body)
		}
	}
}

func TestReposPRReview_EnableRequiresRemote(t *testing.T) {
	router, _, dir := newReposRouterWithPatch(t)
	w := postJSON(t, router, "/repos", map[string]any{"name": "local", "path": dir, "pr_review_enabled": true})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
}
