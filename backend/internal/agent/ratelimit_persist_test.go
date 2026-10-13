package agent

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

type fakeRLStore struct {
	rows    map[string]PersistedRateLimit
	failAll bool
}

func newFakeRLStore() *fakeRLStore { return &fakeRLStore{rows: map[string]PersistedRateLimit{}} }

func (f *fakeRLStore) Save(_ context.Context, id string, until time.Time, attempts int) error {
	if f.failAll {
		return errors.New("boom")
	}
	f.rows[id] = PersistedRateLimit{AgentConfigID: id, Until: until, Attempts: attempts}
	return nil
}

func (f *fakeRLStore) Delete(_ context.Context, id string) error {
	if f.failAll {
		return errors.New("boom")
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeRLStore) LoadActive(_ context.Context, now time.Time) ([]PersistedRateLimit, error) {
	if f.failAll {
		return nil, errors.New("boom")
	}
	var out []PersistedRateLimit
	for _, r := range f.rows {
		if r.Until.After(now) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRLStore) PruneExpired(_ context.Context, now time.Time) error {
	if f.failAll {
		return errors.New("boom")
	}
	for id, r := range f.rows {
		if !r.Until.After(now) {
			delete(f.rows, id)
		}
	}
	return nil
}

func TestRateLimitRegistry_RestoresUnexpiredIgnoresExpired(t *testing.T) {
	st := newFakeRLStore()
	future := time.Now().Add(time.Hour)
	st.rows["live"] = PersistedRateLimit{AgentConfigID: "live", Until: future, Attempts: 3}
	st.rows["dead"] = PersistedRateLimit{AgentConfigID: "dead", Until: time.Now().Add(-time.Minute), Attempts: 2}

	r := NewRateLimitRegistryWithStore(st)
	if ok, until := r.IsBlocked("live"); !ok || !until.Equal(future) {
		t.Fatalf("live block not restored: %v %v", ok, until)
	}
	if ok, _ := r.IsBlocked("dead"); ok {
		t.Fatal("expired block must not be restored")
	}
	if _, still := st.rows["dead"]; still {
		t.Fatal("expired row should be pruned")
	}
	// Attempts restored: next backoff continues the ladder (30s*2^3 = 4m).
	r.BlockWithBackoff("live")
	if got := time.Until(r.BlockedUntil("live")); got < 3*time.Minute || got > 5*time.Minute {
		t.Fatalf("backoff did not continue ladder: %v", got)
	}
}

func TestRateLimitRegistry_WriteThrough(t *testing.T) {
	st := newFakeRLStore()
	r := NewRateLimitRegistryWithStore(st)
	until := time.Now().Add(time.Hour)
	r.Block("a", until)
	if row, ok := st.rows["a"]; !ok || !row.Until.Equal(until) || row.Attempts != 1 {
		t.Fatalf("Block not written through: %+v", st.rows)
	}
	r.BlockWithBackoff("b")
	if _, ok := st.rows["b"]; !ok {
		t.Fatal("BlockWithBackoff not written through")
	}
	r.Unblock("a")
	if _, ok := st.rows["a"]; ok {
		t.Fatal("Unblock not written through")
	}
	if !r.UnblockIfNotBlockedSince("b", time.Now().Add(time.Hour)) {
		t.Fatal("expected clear")
	}
	if len(st.rows) != 0 {
		t.Fatalf("conditional clear not written through: %+v", st.rows)
	}
	// A preserved block stays persisted.
	r.Block("c", until)
	if r.UnblockIfNotBlockedSince("c", time.Now().Add(-time.Hour)) {
		t.Fatal("fresh block must be preserved")
	}
	if _, ok := st.rows["c"]; !ok {
		t.Fatal("preserved block must stay persisted")
	}
}

func TestRateLimitRegistry_StoreErrorsDoNotBreak(t *testing.T) {
	st := newFakeRLStore()
	st.failAll = true
	r := NewRateLimitRegistryWithStore(st)
	r.Block("a", time.Now().Add(time.Hour))
	if ok, _ := r.IsBlocked("a"); !ok {
		t.Fatal("in-memory block must work when store fails")
	}
	r.Unblock("a")
	if ok, _ := r.IsBlocked("a"); ok {
		t.Fatal("unblock must work when store fails")
	}
	if NewRateLimitRegistryWithStore(nil) == nil {
		t.Fatal("nil store must be allowed")
	}
}

func TestSQLRateLimitStore_RoundTrip(t *testing.T) {
	f, err := os.CreateTemp("", "ratelimit-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	st := NewSQLRateLimitStore(gen.New(db.SQL()))

	now := time.Now()
	if err := st.Save(ctx, "live", now.Add(time.Hour), 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(ctx, "old", now.Add(-time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(ctx, "live", now.Add(2*time.Hour), 3); err != nil { // upsert
		t.Fatal(err)
	}
	rows, err := st.LoadActive(ctx, now)
	if err != nil || len(rows) != 1 || rows[0].AgentConfigID != "live" || rows[0].Attempts != 3 {
		t.Fatalf("LoadActive: %+v %v", rows, err)
	}
	if err := st.PruneExpired(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "live"); err != nil {
		t.Fatal(err)
	}
	rows, err = st.LoadActive(ctx, now)
	if err != nil || len(rows) != 0 {
		t.Fatalf("after delete: %+v %v", rows, err)
	}

	// Full registry restart via the real store.
	r1 := NewRateLimitRegistryWithStore(st)
	until := now.Add(time.Hour)
	r1.Block("cfg", until)
	r2 := NewRateLimitRegistryWithStore(st)
	if ok, u := r2.IsBlocked("cfg"); !ok || u.Sub(until).Abs() > time.Second {
		t.Fatalf("restart did not restore block: %v %v", ok, u)
	}
}
