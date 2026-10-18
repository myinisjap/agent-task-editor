package ghclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
)

type recorded struct {
	Method, Path, Query, Body, Auth string
}

// fakeAPI starts a server answering via h, points the client at it, and
// records every request.
func fakeAPI(t *testing.T, h func(r recorded, w http.ResponseWriter, req *http.Request)) *[]recorded {
	t.Helper()
	var reqs []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r := recorded{req.Method, req.URL.Path, req.URL.RawQuery, string(b), req.Header.Get("Authorization")}
		reqs = append(reqs, r)
		h(r, w, req)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GH_TOKEN", "tok123")
	return &reqs
}

func jsonOK(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func TestAuthHeaderSent(t *testing.T) {
	reqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) { jsonOK(w, `[]`) })
	if _, err := GetPRReviews(context.Background(), "acme/widgets", 1); err != nil {
		t.Fatal(err)
	}
	if (*reqs)[0].Auth != "Bearer tok123" {
		t.Errorf("auth = %q", (*reqs)[0].Auth)
	}
}

func TestGetPRForBranch(t *testing.T) {
	cases := []struct {
		name, json, state string
		want              string
		num               int
	}{
		{"open", `[{"state":"open","number":1,"html_url":"u1"}]`, "", "pr_open", 1},
		{"merged", `[{"state":"closed","merged_at":"2024-01-01T00:00:00Z","number":2,"html_url":"u2"}]`, "", "pr_merged", 2},
		{"closed", `[{"state":"closed","number":3,"html_url":"u3"}]`, "", "pr_closed", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) { jsonOK(w, tc.json) })
			st, url, n, err := GetPRForBranch(context.Background(), "acme/widgets", "feat-x")
			if err != nil || st != tc.want || n != tc.num || url != fmt.Sprintf("u%d", tc.num) {
				t.Fatalf("got (%q,%q,%d,%v)", st, url, n, err)
			}
			r := (*reqs)[0]
			if r.Path != "/repos/acme/widgets/pulls" || !strings.Contains(r.Query, "head=acme%3Afeat-x") {
				t.Errorf("request = %+v", r)
			}
		})
	}
}

func TestGetPRForBranch_NoPR(t *testing.T) {
	for _, exists := range []bool{true, false} {
		fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
			if strings.HasSuffix(r.Path, "/pulls") {
				jsonOK(w, `[]`)
				return
			}
			if !exists {
				http.Error(w, `{"message":"Branch not found"}`, 404)
				return
			}
			jsonOK(w, `{}`)
		})
		st, _, n, err := GetPRForBranch(context.Background(), "acme/widgets", "feat/x")
		want := ""
		if exists {
			want = "pushed"
		}
		if err != nil || st != want || n != 0 {
			t.Errorf("exists=%v got (%q,%d,%v)", exists, st, n, err)
		}
	}
}

func TestGetPRForBranch_Error(t *testing.T) {
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) })
	if _, _, _, err := GetPRForBranch(context.Background(), "acme/widgets", "b"); err == nil {
		t.Fatal("want error")
	}
}

func TestCreatePR_ExistingShortCircuit(t *testing.T) {
	reqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, `[{"state":"open","number":5,"html_url":"u5"}]`)
	})
	st, u, err := CreatePR(context.Background(), "acme/widgets", "b", "main", "t", "b")
	if err != nil || st != "pr_open" || u != "u5" || len(*reqs) != 1 {
		t.Fatalf("got (%q,%q,%v) reqs=%d", st, u, err, len(*reqs))
	}
}

func TestCreatePR_CreatesNewWithDefaultBase(t *testing.T) {
	reqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		switch {
		case r.Method == "POST":
			w.WriteHeader(201)
			jsonOK(w, `{"number":9,"html_url":"u9"}`)
		case strings.HasSuffix(r.Path, "/pulls"):
			jsonOK(w, `[]`)
		case strings.Contains(r.Path, "/branches/"):
			jsonOK(w, `{}`)
		default:
			jsonOK(w, `{"default_branch":"develop"}`)
		}
	})
	st, u, err := CreatePR(context.Background(), "acme/widgets", "b", "", "T", "B")
	if err != nil || st != "pr_open" || u != "u9" {
		t.Fatalf("got (%q,%q,%v)", st, u, err)
	}
	last := (*reqs)[len(*reqs)-1]
	var p map[string]string
	_ = json.Unmarshal([]byte(last.Body), &p)
	if p["base"] != "develop" || p["head"] != "b" || p["title"] != "T" || p["body"] != "B" {
		t.Errorf("payload = %v", p)
	}
}

func TestCreatePR_RaceAlreadyExists(t *testing.T) {
	listCalls := 0
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		switch {
		case r.Method == "POST":
			w.WriteHeader(422)
			jsonOK(w, `{"message":"Validation Failed","errors":[{"message":"A pull request already exists for acme:b."}]}`)
		case strings.HasSuffix(r.Path, "/pulls"):
			listCalls++
			if listCalls == 1 {
				jsonOK(w, `[]`)
			} else {
				jsonOK(w, `[{"state":"open","number":7,"html_url":"u7"}]`)
			}
		default:
			jsonOK(w, `{}`)
		}
	})
	st, u, err := CreatePR(context.Background(), "acme/widgets", "b", "main", "t", "b")
	if err != nil || st != "pr_open" || u != "u7" {
		t.Fatalf("got (%q,%q,%v)", st, u, err)
	}
}

func TestGetPRHead(t *testing.T) {
	cases := []struct {
		mergeable string
		want      forge.Mergeability
	}{{"true", MergeableClean}, {"false", MergeableConflicting}, {"null", MergeableUnknown}}
	for _, tc := range cases {
		fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
			if strings.HasSuffix(r.Path, "/pulls") {
				jsonOK(w, `[{"number":4}]`)
				return
			}
			jsonOK(w, `{"number":4,"state":"open","html_url":"u4","updated_at":"T","mergeable":`+tc.mergeable+`,"head":{"sha":"abc"},"base":{"ref":"main"}}`)
		})
		h, err := GetPRHead(context.Background(), "acme/widgets", "b")
		if err != nil {
			t.Fatal(err)
		}
		if h.Number != 4 || h.HeadSHA != "abc" || h.BaseRef != "main" || h.State != "pr_open" || h.URL != "u4" || h.UpdatedAt != "T" || h.Mergeable != tc.want {
			t.Errorf("mergeable=%s got %+v", tc.mergeable, h)
		}
	}
}

func TestGetPRHead_NoPR(t *testing.T) {
	for _, exists := range []bool{true, false} {
		fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
			if strings.HasSuffix(r.Path, "/pulls") {
				jsonOK(w, `[]`)
			} else if exists {
				jsonOK(w, `{}`)
			} else {
				http.Error(w, "{}", 404)
			}
		})
		h, err := GetPRHead(context.Background(), "acme/widgets", "b")
		if err != nil {
			t.Fatal(err)
		}
		want := forge.PRHead{}
		if exists {
			want.State = "pushed"
		}
		if h != want {
			t.Errorf("exists=%v got %+v", exists, h)
		}
	}
}

func TestGetPRReviews_Paginated(t *testing.T) {
	var srvURL string
	fakeAPI(t, func(r recorded, w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("page") == "2" {
			jsonOK(w, `[{"id":2,"state":"approved","body":"b2","user":{"login":"bob"},"submitted_at":"s2","author_association":"member"}]`)
			return
		}
		w.Header().Set("Link", `<`+srvURL+r.Path+`?per_page=100&page=2>; rel="next"`)
		jsonOK(w, `[{"id":1,"state":"changes_requested","body":"b1","user":{"login":"al"},"submitted_at":"s1","author_association":"owner"}]`)
	})
	srvURL = strings.TrimSuffix(apiBase(), "/")
	rs, err := GetPRReviews(context.Background(), "acme/widgets", 3)
	if err != nil || len(rs) != 2 {
		t.Fatalf("got %v %v", rs, err)
	}
	if rs[0].ID != "1" || rs[0].State != "CHANGES_REQUESTED" || rs[0].Author != "al" || rs[0].AuthorAssociation != "OWNER" || rs[1].Author != "bob" {
		t.Errorf("reviews = %+v", rs)
	}
}

func TestGetPRReviewComments(t *testing.T) {
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, `[{"id":1,"path":"a.go","line":10,"start_line":8,"side":"RIGHT","body":"x","diff_hunk":"h","commit_id":"c","user":{"login":"al"},"created_at":"t","author_association":"MEMBER"},
		{"id":2,"path":"b.go","line":null,"body":"y","user":{"login":"bo"}}]`)
	})
	cs, err := GetPRReviewComments(context.Background(), "acme/widgets", 3)
	if err != nil || len(cs) != 2 {
		t.Fatalf("got %v %v", cs, err)
	}
	if cs[0].Line != 10 || cs[0].StartLine != 8 || cs[0].Author != "al" || cs[0].CommitID != "c" {
		t.Errorf("c0 = %+v", cs[0])
	}
	if cs[1].Line != 0 || cs[1].StartLine != 0 {
		t.Errorf("c1 = %+v", cs[1])
	}
}

func TestGetFailedChecks(t *testing.T) {
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		switch {
		case strings.HasSuffix(r.Path, "/pulls/3"):
			jsonOK(w, `{"number":3,"head":{"sha":"abc"}}`)
		case strings.HasSuffix(r.Path, "/check-runs"):
			jsonOK(w, `{"check_runs":[{"name":"ok","conclusion":"success"},{"name":"build","conclusion":"failure","html_url":"l1"},{"name":"c","conclusion":"cancelled","html_url":"l2"},{"name":"p","conclusion":null}]}`)
		case strings.HasSuffix(r.Path, "/status"):
			jsonOK(w, `{"statuses":[{"context":"ci","state":"error","target_url":"l3"},{"context":"fine","state":"success"}]}`)
		default:
			t.Errorf("unexpected %s", r.Path)
		}
	})
	cs, err := GetFailedChecks(context.Background(), "acme/widgets", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []Check{{Name: "build", Link: "l1", Bucket: "fail"}, {Name: "c", Link: "l2", Bucket: "cancel"}, {Name: "ci", Link: "l3", Bucket: "fail"}}
	if len(cs) != len(want) {
		t.Fatalf("got %+v", cs)
	}
	for i := range want {
		if cs[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, cs[i], want[i])
		}
	}
}

func TestGetFailedChecks_Error(t *testing.T) {
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", 500) })
	if _, err := GetFailedChecks(context.Background(), "acme/widgets", 3); err == nil {
		t.Fatal("want error")
	}
}

func TestListOpenIssues(t *testing.T) {
	reqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, `[{"number":1,"title":"a","body":"b","html_url":"u1","author_association":"OWNER","labels":[{"name":"x"},{"name":"y"}]},
		{"number":2,"title":"pr","pull_request":{"url":"z"}}]`)
	})
	is, err := ListOpenIssues(context.Background(), "acme/widgets", "bug")
	if err != nil || len(is) != 1 {
		t.Fatalf("got %+v %v", is, err)
	}
	if is[0].Number != 1 || is[0].URL != "u1" || len(is[0].Labels) != 2 || is[0].AuthorAssociation != "OWNER" {
		t.Errorf("issue = %+v", is[0])
	}
	if q := (*reqs)[0].Query; !strings.Contains(q, "labels=bug") || !strings.Contains(q, "state=open") {
		t.Errorf("query = %q", q)
	}
}

func TestListOpenIssues_NoLabelFilter(t *testing.T) {
	reqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) { jsonOK(w, `[]`) })
	if _, err := ListOpenIssues(context.Background(), "acme/widgets", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains((*reqs)[0].Query, "labels") {
		t.Errorf("query = %q", (*reqs)[0].Query)
	}
}

func TestGetIssueComments(t *testing.T) {
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, `[{"id":5,"body":"hi","author_association":"MEMBER","user":{"login":"al"},"created_at":"t"}]`)
	})
	cs, err := GetIssueComments(context.Background(), "acme/widgets", 2)
	if err != nil || len(cs) != 1 || cs[0].ID != "5" || cs[0].Author != "al" || cs[0].Body != "hi" {
		t.Fatalf("got %+v %v", cs, err)
	}
}

func TestIssueWrites(t *testing.T) {
	reqs := fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) { jsonOK(w, `{}`) })
	ctx := context.Background()
	if err := AddIssueLabel(ctx, "acme/widgets", 4, "agent"); err != nil {
		t.Fatal(err)
	}
	if err := CommentOnIssue(ctx, "acme/widgets", 4, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := CloseIssueWithComment(ctx, "acme/widgets", 4, "done"); err != nil {
		t.Fatal(err)
	}
	want := []struct{ m, p, b string }{
		{"POST", "/repos/acme/widgets/issues/4/labels", `{"labels":["agent"]}`},
		{"POST", "/repos/acme/widgets/issues/4/comments", `{"body":"hello"}`},
		{"POST", "/repos/acme/widgets/issues/4/comments", `{"body":"done"}`},
		{"PATCH", "/repos/acme/widgets/issues/4", `{"state":"closed"}`},
	}
	if len(*reqs) != len(want) {
		t.Fatalf("reqs = %+v", *reqs)
	}
	for i, w := range want {
		g := (*reqs)[i]
		if g.Method != w.m || g.Path != w.p || g.Body != w.b {
			t.Errorf("[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestIssueWrites_Error(t *testing.T) {
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusForbidden) })
	ctx := context.Background()
	if AddIssueLabel(ctx, "a/b", 1, "x") == nil || CommentOnIssue(ctx, "a/b", 1, "x") == nil || CloseIssueWithComment(ctx, "a/b", 1, "x") == nil {
		t.Fatal("want errors")
	}
}

func TestResolveToken_Order(t *testing.T) {
	orig := ghAuthToken
	t.Cleanup(func() { ghAuthToken = orig; cliTokenCached = "" })
	ghAuthToken = func() (string, error) { return "clitok", nil }
	cliTokenCached = ""
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	if tok, src := resolveToken(); tok != "clitok" || src != "gh auth" {
		t.Errorf("cli: %q %q", tok, src)
	}
	t.Setenv("GITHUB_TOKEN", "gt")
	if tok, _ := resolveToken(); tok != "gt" {
		t.Errorf("GITHUB_TOKEN: %q", tok)
	}
	t.Setenv("GH_TOKEN", "ght")
	if tok, _ := resolveToken(); tok != "ght" {
		t.Errorf("GH_TOKEN: %q", tok)
	}
}

func TestAuthStatus_NoToken(t *testing.T) {
	orig := ghAuthToken
	t.Cleanup(func() { ghAuthToken = orig; cliTokenCached = "" })
	ghAuthToken = func() (string, error) { return "", fmt.Errorf("no gh") }
	cliTokenCached = ""
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	if ok, note := (GitHub{}).AuthStatus(); ok || note == "" {
		t.Errorf("got %v %q", ok, note)
	}
	t.Setenv("GITHUB_TOKEN", "x")
	if ok, note := (GitHub{}).AuthStatus(); !ok || note == "" {
		t.Errorf("got %v %q", ok, note)
	}
}

func TestForgeInterfaceDelegation(t *testing.T) {
	var f forge.Forge = GitHub{}
	fakeAPI(t, func(r recorded, w http.ResponseWriter, _ *http.Request) {
		jsonOK(w, `[{"state":"open","number":11,"html_url":"u11"}]`)
	})
	st, u, n, err := f.PRForBranch(context.Background(), "acme/widgets", "feat-x")
	if err != nil || st != "pr_open" || u != "u11" || n != 11 {
		t.Fatalf("got (%q,%q,%d,%v)", st, u, n, err)
	}
}

func TestParseGitHubName(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		want   string
		wantOK bool
	}{
		{"https", "https://github.com/org/repo", "org/repo", true},
		{"https with .git", "https://github.com/org/repo.git", "org/repo", true},
		{"ssh", "git@github.com:org/repo", "org/repo", true},
		{"ssh with .git", "git@github.com:org/repo.git", "org/repo", true},
		{"empty", "", "", false},
		{"junk", "not a url", "", false},
		{"gitlab", "https://gitlab.com/org/repo", "", false},
		{"missing repo", "https://github.com/onlyorg", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseGitHubName(tc.url)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGitHubName pins GitHub.Name()'s return value, which is relied on
// elsewhere (e.g. tasksource.GitHubIssues.Name() and ghsync's PR-review-
// comment source tagging) to match exactly "github".
func TestGitHubName(t *testing.T) {
	if got := (GitHub{}).Name(); got != "github" {
		t.Errorf("Name() = %q, want %q", got, "github")
	}
}

// TestGitHubCompareURL asserts the full constructed URL string, since URL
// construction (via url.Values.Encode(), which sorts keys alphabetically:
// body, expand, title) is CompareURL's only real logic.
func TestGitHubCompareURL(t *testing.T) {
	cases := []struct {
		name                   string
		repoName, base, branch string
		title, body            string
		want                   string
	}{
		{
			name:     "simple",
			repoName: "acme/widgets",
			base:     "main",
			branch:   "feat-x",
			title:    "My Title",
			body:     "My Body",
			want:     "https://github.com/acme/widgets/compare/main...feat-x?body=My+Body&expand=1&title=My+Title",
		},
		{
			name:     "needs escaping",
			repoName: "acme/widgets",
			base:     "main",
			branch:   "feat-x",
			title:    "T&T #1\nnewline",
			body:     "B&B #2\nnewline",
			want:     "https://github.com/acme/widgets/compare/main...feat-x?body=B%26B+%232%0Anewline&expand=1&title=T%26T+%231%0Anewline",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (GitHub{}).CompareURL(tc.repoName, tc.base, tc.branch, tc.title, tc.body)
			if got != tc.want {
				t.Errorf("CompareURL = %q, want %q", got, tc.want)
			}
		})
	}
}
