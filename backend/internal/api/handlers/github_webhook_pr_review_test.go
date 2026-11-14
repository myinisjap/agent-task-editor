package handlers

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type fakeTrigger struct{ ch chan [2]any }

func (f *fakeTrigger) ConsiderPR(_ context.Context, repo string, n int) { f.ch <- [2]any{repo, n} }

func TestWebhook_PullRequestTriggersReviewIngest(t *testing.T) {
	trig := &fakeTrigger{ch: make(chan [2]any, 4)}
	h := NewGitHubWebhookHandler("s3cret", newFakeRefresher())
	h.SetPRReviewTrigger(trig)
	for _, action := range []string{"synchronize", "labeled", "opened", "ready_for_review"} {
		body := `{"action":"` + action + `","number":12,"repository":{"full_name":"o/r"},"pull_request":{"number":12,"head":{"ref":"f","sha":"abc"}}}`
		w := doWebhook(h, "pull_request", "", body, sign("s3cret", body))
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s: got %d", action, w.Code)
		}
		select {
		case got := <-trig.ch:
			if got[0] != "o/r" || got[1] != 12 {
				t.Fatalf("%s: got %v", action, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: ConsiderPR not called", action)
		}
	}
	// A comment on an issue/PR does not trigger a review.
	body := `{"action":"created","issue":{"number":12,"pull_request":{}},"repository":{"full_name":"o/r"}}`
	doWebhook(h, "issue_comment", "", body, sign("s3cret", body))
	select {
	case got := <-trig.ch:
		t.Fatalf("unexpected trigger %v", got)
	case <-time.After(150 * time.Millisecond):
	}
}
