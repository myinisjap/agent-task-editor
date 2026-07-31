package gitea

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
)

// newTestServer starts an httptest server and returns a *Gitea wired to talk
// to it directly (bypassing GITEA_HOST/GITEA_BASE_URL env vars), plus a
// handle to register per-path responses.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*Gitea, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	g := &Gitea{
		hosts:      []string{"git.example.com"},
		baseURL:    srv.URL,
		token:      "test-token",
		httpClient: srv.Client(),
	}
	return g, srv
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func TestParseRepoName(t *testing.T) {
	g := &Gitea{hosts: []string{"git.example.com", "gitea.internal:3000"}}
	cases := []struct {
		name     string
		url      string
		wantName string
		wantOK   bool
	}{
		{"https", "https://git.example.com/owner/repo", "owner/repo", true},
		{"https with .git", "https://git.example.com/owner/repo.git", "owner/repo", true},
		{"ssh scp-like", "git@git.example.com:owner/repo.git", "owner/repo", true},
		{"ssh:// form", "ssh://git@git.example.com/owner/repo.git", "owner/repo", true},
		{"ssh:// with port", "ssh://git@gitea.internal:3000/owner/repo.git", "owner/repo", true},
		{"wrong host", "https://github.com/owner/repo", "", false},
		{"github ssh", "git@github.com:owner/repo.git", "", false},
		{"malformed", "https://git.example.com/owner", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, ok := g.ParseRepoName(tc.url)
			if ok != tc.wantOK || name != tc.wantName {
				t.Errorf("ParseRepoName(%q) = (%q, %v), want (%q, %v)", tc.url, name, ok, tc.wantName, tc.wantOK)
			}
		})
	}
}

func TestGiteaSatisfiesForgeInterface(t *testing.T) {
	var _ forge.Forge = (*Gitea)(nil)
}

func TestPRForBranch_Open(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/owner/repo/pulls" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeJSON(t, w, 200, []map[string]any{
			{
				"number":    5,
				"state":     "open",
				"merged":    false,
				"html_url":  "https://git.example.com/owner/repo/pulls/5",
				"head":      map[string]any{"ref": "feature-branch", "sha": "abc123"},
				"base":      map[string]any{"ref": "main"},
				"mergeable": true,
			},
		})
	})

	state, prURL, prNumber, err := g.PRForBranch(t.Context(), "owner/repo", "feature-branch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "pr_open" || prNumber != 5 || prURL != "https://git.example.com/owner/repo/pulls/5" {
		t.Errorf("got (%q, %q, %d)", state, prURL, prNumber)
	}
}

func TestPRForBranch_Merged(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, []map[string]any{
			{
				"number": 7, "state": "closed", "merged": true,
				"html_url": "https://git.example.com/owner/repo/pulls/7",
				"head":     map[string]any{"ref": "feature-branch", "sha": "deadbeef"},
				"base":     map[string]any{"ref": "main"},
			},
		})
	})
	state, _, prNumber, err := g.PRForBranch(t.Context(), "owner/repo", "feature-branch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "pr_merged" || prNumber != 7 {
		t.Errorf("got (%q, %d), want pr_merged/7", state, prNumber)
	}
}

func TestPRForBranch_ClosedNotMerged(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, []map[string]any{
			{
				"number": 9, "state": "closed", "merged": false,
				"html_url": "https://git.example.com/owner/repo/pulls/9",
				"head":     map[string]any{"ref": "feature-branch"},
				"base":     map[string]any{"ref": "main"},
			},
		})
	})
	state, _, _, err := g.PRForBranch(t.Context(), "owner/repo", "feature-branch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "pr_closed" {
		t.Errorf("state = %q, want pr_closed", state)
	}
}

func TestPRForBranch_NoPR_BranchExists(t *testing.T) {
	calls := 0
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "/pulls") {
			writeJSON(t, w, 200, []map[string]any{})
			return
		}
		if strings.Contains(r.URL.Path, "/branches/") {
			writeJSON(t, w, 200, map[string]any{"name": "feature-branch"})
			return
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
	})
	state, prURL, prNumber, err := g.PRForBranch(t.Context(), "owner/repo", "feature-branch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "pushed" || prURL != "" || prNumber != 0 {
		t.Errorf("got (%q, %q, %d), want (pushed, \"\", 0)", state, prURL, prNumber)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (pulls list + branch check), got %d", calls)
	}
}

func TestPRForBranch_NoPR_BranchNotExists(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls") {
			writeJSON(t, w, 200, []map[string]any{})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	})
	state, _, prNumber, err := g.PRForBranch(t.Context(), "owner/repo", "ghost-branch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "" || prNumber != 0 {
		t.Errorf("got (%q, %d), want (\"\", 0)", state, prNumber)
	}
}

func TestCreatePR_ExistingPRShortCircuit(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected only a GET (existing-PR check), got %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, 200, []map[string]any{
			{
				"number": 3, "state": "open", "merged": false,
				"html_url": "https://git.example.com/owner/repo/pulls/3",
				"head":     map[string]any{"ref": "feature-branch"},
				"base":     map[string]any{"ref": "main"},
			},
		})
	})
	state, prURL, err := g.CreatePR(t.Context(), "owner/repo", "feature-branch", "main", "title", "body")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "pr_open" || prURL != "https://git.example.com/owner/repo/pulls/3" {
		t.Errorf("got (%q, %q)", state, prURL)
	}
}

func TestCreatePR_CreatesNew(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls"):
			writeJSON(t, w, 200, []map[string]any{}) // no existing PR
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["head"] != "feature-branch" || body["base"] != "main" {
				t.Errorf("unexpected create body: %+v", body)
			}
			writeJSON(t, w, 201, map[string]any{
				"number": 11, "state": "open", "merged": false,
				"html_url": "https://git.example.com/owner/repo/pulls/11",
				"head":     map[string]any{"ref": "feature-branch"},
				"base":     map[string]any{"ref": "main"},
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	state, prURL, err := g.CreatePR(t.Context(), "owner/repo", "feature-branch", "main", "my title", "my body")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != "pr_open" || prURL != "https://git.example.com/owner/repo/pulls/11" {
		t.Errorf("got (%q, %q)", state, prURL)
	}
}

func TestPRHead_Mergeable(t *testing.T) {
	cases := []struct {
		name      string
		mergeable any
		want      forge.Mergeability
	}{
		{"true", true, forge.MergeableClean},
		{"false", false, forge.MergeableConflicting},
		{"null", nil, forge.MergeableUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, 200, []map[string]any{
					{
						"number": 1, "state": "open",
						"head":      map[string]any{"ref": "feature-branch", "sha": "abc"},
						"base":      map[string]any{"ref": "main"},
						"mergeable": tc.mergeable,
					},
				})
			})
			head, err := g.PRHead(t.Context(), "owner/repo", "feature-branch")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if head.Mergeable != tc.want {
				t.Errorf("Mergeable = %v, want %v", head.Mergeable, tc.want)
			}
		})
	}
}

func TestPRHead_NoPR(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, []map[string]any{})
	})
	head, err := g.PRHead(t.Context(), "owner/repo", "ghost-branch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if head != (forge.PRHead{}) {
		t.Errorf("expected zero PRHead, got %+v", head)
	}
}

func TestPRReviews_MapsRequestChangesToChangesRequested(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, []map[string]any{
			{"id": 1, "state": "REQUEST_CHANGES", "body": "please fix", "submitted_at": "2026-01-01T00:00:00Z", "user": map[string]any{"login": "alice"}},
			{"id": 2, "state": "APPROVED", "body": "lgtm", "submitted_at": "2026-01-02T00:00:00Z", "user": map[string]any{"login": "bob"}},
		})
	})
	reviews, err := g.PRReviews(t.Context(), "owner/repo", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("expected 2 reviews, got %d", len(reviews))
	}
	if reviews[0].State != "CHANGES_REQUESTED" || reviews[0].Author != "alice" {
		t.Errorf("review[0] = %+v", reviews[0])
	}
	if reviews[1].State != "APPROVED" {
		t.Errorf("review[1] = %+v", reviews[1])
	}
}

func TestPRReviewComments_MapsSideFromSignedLine(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, []map[string]any{
			{"id": 100, "path": "main.go", "line": 42, "diff_hunk": "@@ -1,2 +1,2 @@", "commit_id": "abc", "body": "fix this", "created_at": "2026-01-01T00:00:00Z", "user": map[string]any{"login": "alice"}},
			{"id": 101, "path": "main.go", "line": -7, "diff_hunk": "@@ -1,2 +1,2 @@", "commit_id": "abc", "body": "old side", "created_at": "2026-01-01T00:00:00Z", "user": map[string]any{"login": "alice"}},
		})
	})
	comments, err := g.PRReviewComments(t.Context(), "owner/repo", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].Side != "RIGHT" || comments[0].Line != 42 {
		t.Errorf("comment[0] = %+v", comments[0])
	}
	if comments[1].Side != "LEFT" || comments[1].Line != 7 {
		t.Errorf("comment[1] = %+v", comments[1])
	}
}

func TestFailedChecks_FiltersToFailuresAndErrors(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/5"):
			writeJSON(t, w, 200, map[string]any{
				"number": 5, "state": "open",
				"head": map[string]any{"ref": "feature-branch", "sha": "abc123"},
				"base": map[string]any{"ref": "main"},
			})
		case strings.Contains(r.URL.Path, "/commits/abc123/statuses"):
			writeJSON(t, w, 200, []map[string]any{
				{"status": "success", "context": "build"},
				{"status": "failure", "context": "test", "target_url": "https://ci.example.com/1"},
				{"status": "pending", "context": "lint"},
				{"status": "error", "context": "deploy", "target_url": "https://ci.example.com/2"},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	checks, err := g.FailedChecks(t.Context(), "owner/repo", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 failed checks, got %d: %+v", len(checks), checks)
	}
	names := []string{checks[0].Name, checks[1].Name}
	if names[0] != "test" || names[1] != "deploy" {
		t.Errorf("unexpected check names: %v", names)
	}
}

func TestListOpenIssues_Paginated(t *testing.T) {
	page := 0
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		page++
		q := r.URL.Query()
		if q.Get("type") != "issues" || q.Get("state") != "open" {
			t.Errorf("unexpected query: %v", q)
		}
		switch q.Get("page") {
		case "1":
			issues := make([]map[string]any, 50)
			for i := range issues {
				issues[i] = map[string]any{"number": i + 1, "title": fmt.Sprintf("issue %d", i+1), "html_url": "u"}
			}
			writeJSON(t, w, 200, issues)
		case "2":
			writeJSON(t, w, 200, []map[string]any{
				{"number": 51, "title": "last one", "html_url": "u"},
			})
		default:
			t.Fatalf("unexpected page %s", q.Get("page"))
		}
	})
	issues, err := g.ListOpenIssues(t.Context(), "owner/repo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 51 {
		t.Fatalf("expected 51 issues (full un-truncated fetch across pages), got %d", len(issues))
	}
	if page != 2 {
		t.Errorf("expected 2 page requests (a full 50-item page, then a short page that stops pagination), got %d", page)
	}
}

func TestListOpenIssues_SkipsPullRequests(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, []map[string]any{
			{"number": 1, "title": "a real issue", "html_url": "u"},
			{"number": 2, "title": "a PR", "html_url": "u", "pull_request": map[string]any{"url": "x"}},
		})
	})
	issues, err := g.ListOpenIssues(t.Context(), "owner/repo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 1 {
		t.Errorf("expected only the non-PR issue, got %+v", issues)
	}
}

func TestGetIssueComments_TrustClassification(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/comments"):
			writeJSON(t, w, 200, []map[string]any{
				{"id": 1, "body": "hi", "created_at": "2026-01-01T00:00:00Z", "user": map[string]any{"login": "collab-alice"}},
				{"id": 2, "body": "spam", "created_at": "2026-01-01T00:00:00Z", "user": map[string]any{"login": "rando-bob"}},
			})
		case strings.Contains(r.URL.Path, "/collaborators/collab-alice"):
			w.WriteHeader(204)
		case strings.Contains(r.URL.Path, "/collaborators/rando-bob"):
			http.Error(w, "not found", http.StatusNotFound)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})
	comments, err := g.GetIssueComments(t.Context(), "owner/repo", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].AuthorAssociation != "COLLABORATOR" {
		t.Errorf("comments[0].AuthorAssociation = %q, want COLLABORATOR", comments[0].AuthorAssociation)
	}
	if comments[1].AuthorAssociation != "NONE" {
		t.Errorf("comments[1].AuthorAssociation = %q, want NONE", comments[1].AuthorAssociation)
	}
}

func TestAddIssueLabel(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/labels"):
			writeJSON(t, w, 200, []map[string]any{
				{"id": 42, "name": "agent-in-progress"},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues/5/labels"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			labels, _ := body["labels"].([]any)
			if len(labels) != 1 || labels[0] != float64(42) {
				t.Errorf("unexpected label ids: %v", labels)
			}
			w.WriteHeader(200)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	})
	if err := g.AddIssueLabel(t.Context(), "owner/repo", 5, "agent-in-progress"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAddIssueLabel_UnknownLabel(t *testing.T) {
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, []map[string]any{})
	})
	if err := g.AddIssueLabel(t.Context(), "owner/repo", 5, "does-not-exist"); err == nil {
		t.Fatal("expected an error for a label that doesn't exist on the repo")
	}
}

func TestCommentOnIssue(t *testing.T) {
	var gotBody map[string]any
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
	})
	if err := g.CommentOnIssue(t.Context(), "owner/repo", 5, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["body"] != "hello" {
		t.Errorf("gotBody = %+v", gotBody)
	}
}

func TestCloseIssueWithComment_CommentsThenCloses(t *testing.T) {
	var calls []string
	g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(200)
		case http.MethodPatch:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["state"] != "closed" {
				t.Errorf("expected state=closed, got %+v", body)
			}
			w.WriteHeader(200)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})
	if err := g.CloseIssueWithComment(t.Context(), "owner/repo", 5, "done"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "POST") || !strings.HasPrefix(calls[1], "PATCH") {
		t.Errorf("expected POST comment then PATCH close, got %v", calls)
	}
}

func TestAuthStatus(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		g := &Gitea{}
		authed, note := g.AuthStatus()
		if authed {
			t.Error("expected unauthenticated with no token")
		}
		if note == "" {
			t.Error("expected a note explaining why")
		}
	})

	t.Run("valid token", func(t *testing.T) {
		g, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "token test-token" {
				t.Errorf("missing/wrong Authorization header: %q", r.Header.Get("Authorization"))
			}
			writeJSON(t, w, 200, map[string]any{"login": "ci-bot"})
		})
		authed, _ := g.AuthStatus()
		if !authed {
			t.Error("expected authenticated with a valid token + reachable /user")
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		g := &Gitea{hosts: []string{"git.example.com"}, baseURL: "http://127.0.0.1:1", token: "tok", httpClient: http.DefaultClient}
		authed, note := g.AuthStatus()
		if authed {
			t.Error("expected unauthenticated when the server is unreachable")
		}
		if note == "" {
			t.Error("expected a note")
		}
	})
}

func TestCompareURL(t *testing.T) {
	g := &Gitea{baseURL: "https://git.example.com"}
	url := g.CompareURL("owner/repo", "main", "feature-branch", "My Title", "My Body")
	want := "https://git.example.com/owner/repo/compare/main...feature-branch?"
	if !strings.HasPrefix(url, want) {
		t.Errorf("CompareURL = %q, want prefix %q", url, want)
	}
	if !strings.Contains(url, "title=My+Title") || !strings.Contains(url, "body=My+Body") {
		t.Errorf("CompareURL missing expected query params: %q", url)
	}
}

func TestNew_NoHostConfigured(t *testing.T) {
	t.Setenv("GITEA_HOST", "")
	t.Setenv("GITEA_TOKEN", "")
	t.Setenv("GITEA_BASE_URL", "")
	_, ok := New()
	if ok {
		t.Error("expected New to report ok=false with no GITEA_HOST configured")
	}
}

func TestNew_ConfiguredFromEnv(t *testing.T) {
	t.Setenv("GITEA_HOST", "git.example.com, gitea.internal:3000")
	t.Setenv("GITEA_TOKEN", "shh")
	t.Setenv("GITEA_BASE_URL", "")
	g, ok := New()
	if !ok {
		t.Fatal("expected New to succeed with GITEA_HOST set")
	}
	if g.baseURL != "https://git.example.com" {
		t.Errorf("baseURL = %q, want https://git.example.com (derived from first host)", g.baseURL)
	}
	if len(g.hosts) != 2 || g.hosts[0] != "git.example.com" || g.hosts[1] != "gitea.internal:3000" {
		t.Errorf("hosts = %v", g.hosts)
	}
	if g.token != "shh" {
		t.Errorf("token = %q", g.token)
	}
}

func TestNew_BaseURLOverride(t *testing.T) {
	t.Setenv("GITEA_HOST", "git.example.com")
	t.Setenv("GITEA_TOKEN", "")
	t.Setenv("GITEA_BASE_URL", "http://internal-gitea:3000/")
	g, ok := New()
	if !ok {
		t.Fatal("expected New to succeed")
	}
	if g.baseURL != "http://internal-gitea:3000" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", g.baseURL)
	}
}
