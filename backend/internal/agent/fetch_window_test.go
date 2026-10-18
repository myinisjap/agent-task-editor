package agent

import (
	"testing"
	"time"
)

func TestFetchSkipWindow(t *testing.T) {
	repo := t.TempDir()
	now := time.Now()
	if recentlyFetched(repo, now) {
		t.Fatal("unfetched repo should not be skipped")
	}
	markFetched(repo, now)
	if !recentlyFetched(repo, now.Add(fetchSkipWindow-time.Second)) {
		t.Fatal("expected skip inside window")
	}
	if recentlyFetched(repo, now.Add(fetchSkipWindow+time.Second)) {
		t.Fatal("expected fetch after window")
	}
	if recentlyFetched(t.TempDir(), now) {
		t.Fatal("other repo must not be affected")
	}
}
