package ghsync

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/forge"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// webhookFixture seeds one repo (acme/widgets) with two open-PR tasks and a
// Syncer whose getPRHead records which branches were refreshed.
type webhookFixture struct {
	s          *Syncer
	q          *gen.Queries
	t7, t8     gen.Task
	mu         sync.Mutex
	refreshed  map[string]int // branch -> getPRHead call count
	refreshedC chan string
}

func newWebhookFixture(t *testing.T) *webhookFixture {
	t.Helper()
	f := &webhookFixture{refreshed: map[string]int{}, refreshedC: make(chan string, 16)}
	s, q, _ := newTestSyncer(t, func(_ context.Context, _ repoInfo, branch string) (forge.PRHead, error) {
		f.mu.Lock()
		f.refreshed[branch]++
		f.mu.Unlock()
		f.refreshedC <- branch
		return fakePRHead("pr_open", "https://github.com/acme/widgets/pull/7", 0), nil
	})
	f.s, f.q = s, q
	wfID, label := newTestWorkflow(t, q)
	repoID := newTestRepo(t, q, wfID, t.TempDir(), ghURL())
	f.t7 = newTestTask(t, q, repoID, wfID, label, "feat-7", "", "pr_open", "https://github.com/acme/widgets/pull/7")
	f.t8 = newTestTask(t, q, repoID, wfID, label, "feat-8", "", "pr_open", "https://github.com/acme/widgets/pull/8")
	return f
}

func (f *webhookFixture) waitRefresh(t *testing.T, want string) {
	t.Helper()
	select {
	case got := <-f.refreshedC:
		if got != want {
			t.Fatalf("refreshed branch %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for refresh of %q", want)
	}
}

func (f *webhookFixture) expectNoRefresh(t *testing.T) {
	t.Helper()
	select {
	case got := <-f.refreshedC:
		t.Fatalf("unexpected refresh of %q", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestPRNumberFromURL(t *testing.T) {
	cases := map[string]int{
		"https://github.com/acme/widgets/pull/7":   7,
		"https://github.com/acme/widgets/pull/12/": 12,
		"https://github.com/acme/widgets/pull/9#x": 9,
		"https://github.com/acme/widgets/issues/7": 0,
		"": 0,
		"https://github.com/acme/widgets/pull/nope": 0,
	}
	for in, want := range cases {
		if got := prNumberFromURL(in); got != want {
			t.Errorf("prNumberFromURL(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestRefreshMatching_ByPRNumber_InvokesSyncPath(t *testing.T) {
	f := newWebhookFixture(t)
	n := f.s.RefreshMatching(context.Background(), PRRef{Repo: "Acme/Widgets", Numbers: []int{7}})
	if n != 1 {
		t.Fatalf("matched = %d, want 1", n)
	}
	f.waitRefresh(t, "feat-7")
	f.expectNoRefresh(t)
}

func TestRefreshMatching_ByBranch(t *testing.T) {
	f := newWebhookFixture(t)
	if n := f.s.RefreshMatching(context.Background(), PRRef{Repo: "acme/widgets", Branches: []string{"feat-8"}}); n != 1 {
		t.Fatalf("matched = %d, want 1", n)
	}
	f.waitRefresh(t, "feat-8")
}

func TestRefreshMatching_ByHeadSHA(t *testing.T) {
	f := newWebhookFixture(t)
	if _, err := f.q.UpsertTaskPRReviewState(context.Background(), gen.UpsertTaskPRReviewStateParams{TaskID: f.t8.ID, HeadSha: "abc123"}); err != nil {
		t.Fatal(err)
	}
	if n := f.s.RefreshMatching(context.Background(), PRRef{Repo: "acme/widgets", HeadSHA: "ABC123"}); n != 1 {
		t.Fatalf("matched = %d, want 1", n)
	}
	f.waitRefresh(t, "feat-8")
}

func TestRefreshMatching_NoMatch(t *testing.T) {
	f := newWebhookFixture(t)
	ctx := context.Background()
	for name, ref := range map[string]PRRef{
		"other repo":   {Repo: "acme/other", Numbers: []int{7}},
		"other number": {Repo: "acme/widgets", Numbers: []int{99}},
		"other branch": {Repo: "acme/widgets", Branches: []string{"nope"}},
		"empty":        {Repo: "acme/widgets"},
	} {
		if n := f.s.RefreshMatching(ctx, ref); n != 0 {
			t.Errorf("%s: matched %d, want 0", name, n)
		}
	}
	f.expectNoRefresh(t)
}

func TestRefreshMatching_SkipsTerminalTasks(t *testing.T) {
	f := newWebhookFixture(t)
	if _, err := f.q.SetTaskPR(context.Background(), gen.SetTaskPRParams{GitState: "pr_merged", PrUrl: f.t7.PrUrl, ID: f.t7.ID}); err != nil {
		t.Fatal(err)
	}
	if n := f.s.RefreshMatching(context.Background(), PRRef{Repo: "acme/widgets", Numbers: []int{7}}); n != 0 {
		t.Fatalf("matched = %d, want 0 for merged task", n)
	}
}

func TestRefreshMatching_DebouncesPerTask(t *testing.T) {
	f := newWebhookFixture(t)
	f.s.debounce = 100 * time.Millisecond
	for i := 0; i < 5; i++ {
		f.s.RefreshMatching(context.Background(), PRRef{Repo: "acme/widgets", Numbers: []int{7}})
	}
	f.waitRefresh(t, "feat-7")
	f.expectNoRefresh(t)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refreshed["feat-7"] != 1 {
		t.Errorf("refresh count = %d, want 1 (coalesced)", f.refreshed["feat-7"])
	}
}

func TestWebhookRecent(t *testing.T) {
	s := &Syncer{}
	now := time.Now()
	if s.webhookRecent(now) {
		t.Error("no webhook yet: want not recent")
	}
	s.NoteWebhook()
	if !s.webhookRecent(now) {
		t.Error("just noted: want recent")
	}
	if s.webhookRecent(now.Add(webhookQuietWindow + time.Second)) {
		t.Error("past quiet window: want not recent")
	}
}
