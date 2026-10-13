package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/ghsync"
)

const testWebhookSecret = "s3cret"

type fakeRefresher struct {
	mu    sync.Mutex
	refs  []ghsync.PRRef
	noted int
	ch    chan ghsync.PRRef
}

func newFakeRefresher() *fakeRefresher { return &fakeRefresher{ch: make(chan ghsync.PRRef, 16)} }

func (f *fakeRefresher) NoteWebhook() {
	f.mu.Lock()
	f.noted++
	f.mu.Unlock()
}

func (f *fakeRefresher) RefreshMatching(_ context.Context, ref ghsync.PRRef) int {
	f.mu.Lock()
	f.refs = append(f.refs, ref)
	f.mu.Unlock()
	f.ch <- ref
	return 1
}

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func doWebhook(h *GitHubWebhookHandler, event, delivery, body, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/github/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	w := httptest.NewRecorder()
	h.Handle(w, req)
	return w
}

func (f *fakeRefresher) next(t *testing.T) ghsync.PRRef {
	t.Helper()
	select {
	case r := <-f.ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("RefreshMatching was not invoked")
		return ghsync.PRRef{}
	}
}

func TestWebhook_SecretUnset_NotUsable(t *testing.T) {
	f := newFakeRefresher()
	h := NewGitHubWebhookHandler("", f)
	body := `{}`
	if w := doWebhook(h, "ping", "d1", body, sign("", body)); w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", w.Code)
	}
}

func TestWebhook_Signature(t *testing.T) {
	body := `{"zen":"hi"}`
	cases := map[string]struct {
		sig  string
		want int
	}{
		"valid":        {sign(testWebhookSecret, body), http.StatusOK},
		"missing":      {"", http.StatusUnauthorized},
		"wrong secret": {sign("other", body), http.StatusUnauthorized},
		"no prefix":    {strings.TrimPrefix(sign(testWebhookSecret, body), "sha256="), http.StatusUnauthorized},
		"not hex":      {"sha256=zzzz", http.StatusUnauthorized},
		"tampered":     {sign(testWebhookSecret, body+" "), http.StatusUnauthorized},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := NewGitHubWebhookHandler(testWebhookSecret, newFakeRefresher())
			if w := doWebhook(h, "ping", "", body, tc.sig); w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestWebhook_BodyTooLarge(t *testing.T) {
	h := NewGitHubWebhookHandler(testWebhookSecret, newFakeRefresher())
	body := `{"x":"` + strings.Repeat("a", webhookMaxBody) + `"}`
	if w := doWebhook(h, "pull_request", "", body, sign(testWebhookSecret, body)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("got %d, want 413", w.Code)
	}
}

func TestWebhook_EventMapping(t *testing.T) {
	cases := []struct {
		name, event, body string
		wantStatus        int
		wantRef           *ghsync.PRRef
	}{
		{"pr opened", "pull_request",
			`{"action":"opened","number":7,"repository":{"full_name":"acme/widgets"},"pull_request":{"number":7,"head":{"ref":"feat","sha":"aaa"}}}`,
			http.StatusAccepted, &ghsync.PRRef{Repo: "acme/widgets", Numbers: []int{7}, Branches: []string{"feat"}, HeadSHA: "aaa"}},
		{"pr closed", "pull_request",
			`{"action":"closed","repository":{"full_name":"acme/widgets"},"pull_request":{"number":7,"merged":true,"head":{"ref":"feat","sha":"aaa"}}}`,
			http.StatusAccepted, &ghsync.PRRef{Repo: "acme/widgets", Numbers: []int{7}, Branches: []string{"feat"}, HeadSHA: "aaa"}},
		{"pr labeled ignored", "pull_request",
			`{"action":"labeled","repository":{"full_name":"acme/widgets"},"pull_request":{"number":7}}`,
			http.StatusNoContent, nil},
		{"review", "pull_request_review",
			`{"action":"submitted","repository":{"full_name":"acme/widgets"},"pull_request":{"number":7,"head":{"ref":"feat","sha":"aaa"}}}`,
			http.StatusAccepted, &ghsync.PRRef{Repo: "acme/widgets", Numbers: []int{7}, Branches: []string{"feat"}, HeadSHA: "aaa"}},
		{"review comment", "pull_request_review_comment",
			`{"action":"created","repository":{"full_name":"acme/widgets"},"pull_request":{"number":7,"head":{"ref":"feat","sha":"aaa"}}}`,
			http.StatusAccepted, &ghsync.PRRef{Repo: "acme/widgets", Numbers: []int{7}, Branches: []string{"feat"}, HeadSHA: "aaa"}},
		{"issue comment on PR", "issue_comment",
			`{"action":"created","repository":{"full_name":"acme/widgets"},"issue":{"number":7,"pull_request":{"url":"x"}}}`,
			http.StatusAccepted, &ghsync.PRRef{Repo: "acme/widgets", Numbers: []int{7}}},
		{"issue comment on issue", "issue_comment",
			`{"action":"created","repository":{"full_name":"acme/widgets"},"issue":{"number":7}}`,
			http.StatusNoContent, nil},
		{"check suite completed", "check_suite",
			`{"action":"completed","repository":{"full_name":"acme/widgets"},"check_suite":{"head_sha":"bbb","head_branch":"feat","pull_requests":[{"number":7}]}}`,
			http.StatusAccepted, &ghsync.PRRef{Repo: "acme/widgets", Numbers: []int{7}, Branches: []string{"feat"}, HeadSHA: "bbb"}},
		{"check suite requested ignored", "check_suite",
			`{"action":"requested","repository":{"full_name":"acme/widgets"},"check_suite":{"head_sha":"bbb"}}`,
			http.StatusNoContent, nil},
		{"check run completed", "check_run",
			`{"action":"completed","repository":{"full_name":"acme/widgets"},"check_run":{"head_sha":"ccc","check_suite":{"head_branch":"feat"},"pull_requests":[{"number":7}]}}`,
			http.StatusAccepted, &ghsync.PRRef{Repo: "acme/widgets", Numbers: []int{7}, Branches: []string{"feat"}, HeadSHA: "ccc"}},
		{"status", "status",
			`{"sha":"ddd","state":"failure","repository":{"full_name":"acme/widgets"},"branches":[{"name":"feat"},{"name":"main"}]}`,
			http.StatusAccepted, &ghsync.PRRef{Repo: "acme/widgets", Branches: []string{"feat", "main"}, HeadSHA: "ddd"}},
		{"unhandled event", "push",
			`{"repository":{"full_name":"acme/widgets"}}`, http.StatusNoContent, nil},
		{"no repository", "pull_request",
			`{"action":"opened","pull_request":{"number":7}}`, http.StatusNoContent, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeRefresher()
			h := NewGitHubWebhookHandler(testWebhookSecret, f)
			w := doWebhook(h, tc.event, "", tc.body, sign(testWebhookSecret, tc.body))
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantRef == nil {
				select {
				case r := <-f.ch:
					t.Fatalf("unexpected refresh %+v", r)
				case <-time.After(50 * time.Millisecond):
				}
				return
			}
			got := f.next(t)
			if got.Repo != tc.wantRef.Repo || got.HeadSHA != tc.wantRef.HeadSHA ||
				strings.Join(got.Branches, ",") != strings.Join(tc.wantRef.Branches, ",") ||
				len(got.Numbers) != len(tc.wantRef.Numbers) || (len(got.Numbers) > 0 && got.Numbers[0] != tc.wantRef.Numbers[0]) {
				t.Errorf("ref = %+v, want %+v", got, *tc.wantRef)
			}
		})
	}
}

func TestWebhook_PingDoesNotRefresh(t *testing.T) {
	f := newFakeRefresher()
	h := NewGitHubWebhookHandler(testWebhookSecret, f)
	body := `{"zen":"x"}`
	if w := doWebhook(h, "ping", "d", body, sign(testWebhookSecret, body)); w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	if len(f.refs) != 0 {
		t.Error("ping must not refresh")
	}
}

func TestWebhook_DedupeByDelivery(t *testing.T) {
	f := newFakeRefresher()
	h := NewGitHubWebhookHandler(testWebhookSecret, f)
	body := `{"action":"opened","repository":{"full_name":"acme/widgets"},"pull_request":{"number":7}}`
	sig := sign(testWebhookSecret, body)
	if w := doWebhook(h, "pull_request", "dup-1", body, sig); w.Code != http.StatusAccepted {
		t.Fatalf("first: %d", w.Code)
	}
	f.next(t)
	w := doWebhook(h, "pull_request", "dup-1", body, sig)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "duplicate") {
		t.Fatalf("second: %d %s", w.Code, w.Body.String())
	}
	select {
	case r := <-f.ch:
		t.Fatalf("duplicate triggered refresh %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	// A different delivery ID is processed.
	if w := doWebhook(h, "pull_request", "dup-2", body, sig); w.Code != http.StatusAccepted {
		t.Fatalf("third: %d", w.Code)
	}
	f.next(t)
}

func TestDeliveryLRU_Evicts(t *testing.T) {
	l := newDeliveryLRU(2)
	l.add("a")
	l.add("b")
	l.add("c") // evicts a
	if l.add("a") {
		t.Error("a should have been evicted")
	}
	if !l.add("c") {
		t.Error("c should still be present")
	}
}

func TestWebhook_UnsignedNeverNotesOrRefreshes(t *testing.T) {
	f := newFakeRefresher()
	h := NewGitHubWebhookHandler(testWebhookSecret, f)
	body := `{"action":"opened","repository":{"full_name":"acme/widgets"},"pull_request":{"number":7}}`
	if w := doWebhook(h, "pull_request", "x", body, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.noted != 0 || len(f.refs) != 0 {
		t.Error("unsigned request must not note or refresh")
	}
}
