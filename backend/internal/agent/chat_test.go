package agent

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// chatStreamProvider is a Provider that emits a couple of streamed log lines
// and returns a fixed session id, so we can assert the ChatRunner persists,
// broadcasts, and captures the resume session. It also records the RunInput it
// was called with, so tests can assert the resumed session id was threaded in.
type chatStreamProvider struct {
	sessionID string
	lastInput RunInput
	called    bool
}

func (p *chatStreamProvider) Run(_ context.Context, input RunInput, logCh chan<- LogEntry) (Result, error) {
	p.called = true
	p.lastInput = input
	logCh <- LogEntry{Type: LogStdout, Content: "hello from agent", At: time.Now()}
	logCh <- LogEntry{Type: LogToolCall, Content: "ran a tool", At: time.Now()}
	return Result{Status: "completed", Outcome: "success", SessionID: p.sessionID}, nil
}

// capturePub records every published event for assertions.
type capturePub struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	typ     string
	payload map[string]any
}

func (c *capturePub) Publish(eventType string, payload map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, capturedEvent{typ: eventType, payload: payload})
}

func (c *capturePub) countType(t string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.events {
		if e.typ == t {
			n++
		}
	}
	return n
}

func openChatTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp("", "chat-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	db, err := storage.Open(f.Name())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.SeedDefaultWorkflow(context.Background(), db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// seedChatSession creates a repo (backed by a real git checkout) and a chat
// session against it, returning the session id and repo path.
func seedChatSession(t *testing.T, q *gen.Queries) (sessionID, repoPath string) {
	t.Helper()
	ctx := context.Background()
	repoPath = initRepo(t)

	repoID := uuid.NewString()
	if _, err := q.CreateRepo(ctx, gen.CreateRepoParams{ID: repoID, Name: "repo", Path: repoPath}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	sess, err := q.CreateChatSession(ctx, gen.CreateChatSessionParams{
		ID:       uuid.NewString(),
		RepoID:   repoID,
		Provider: "mock",
		Model:    "none",
		Title:    "test chat",
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	return sess.ID, repoPath
}

// TestChatRunner_SendMessage_HappyPath verifies one turn: the user message and
// every streamed line are persisted as chat_messages and broadcast, a worktree
// is provisioned, and the provider's session id is captured for resume.
func TestChatRunner_SendMessage_HappyPath(t *testing.T) {
	db := openChatTestDB(t)
	q := gen.New(db.SQL())
	sessionID, repoPath := seedChatSession(t, q)

	prov := &chatStreamProvider{sessionID: "sess-abc"}
	pub := &capturePub{}
	cr := NewChatRunner(db.SQL(), func(AgentConfig) Provider { return prov }, pub, 2)

	if err := cr.SendMessage(context.Background(), sessionID, repoPath, "hi there"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// Transcript: 1 user message + 2 streamed provider lines.
	msgs, err := q.ListChatMessages(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 persisted messages (user + 2 streamed), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Type != "user" || msgs[0].Content != "hi there" {
		t.Fatalf("first message should be the user's, got type=%q content=%q", msgs[0].Type, msgs[0].Content)
	}

	// Broadcast: 3 chat.message events + 1 chat.turn_done.
	if got := pub.countType("chat.message"); got != 3 {
		t.Fatalf("expected 3 chat.message events, got %d", got)
	}
	if got := pub.countType("chat.turn_done"); got != 1 {
		t.Fatalf("expected 1 chat.turn_done event, got %d", got)
	}

	// Worktree provisioned and persisted.
	sess, err := q.GetChatSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.WorktreePath == "" {
		t.Fatal("expected a worktree path to be persisted after the first turn")
	}
	// Provider session id captured for the next turn's resume.
	if sess.ProviderSessionID != "sess-abc" {
		t.Fatalf("expected provider_session_id sess-abc, got %q", sess.ProviderSessionID)
	}
}

// TestChatRunner_SendMessage_ResumesSession verifies a second turn passes the
// stored provider session id back into the provider (so the CLI resumes rather
// than starting cold) and reuses the same worktree.
func TestChatRunner_SendMessage_ResumesSession(t *testing.T) {
	db := openChatTestDB(t)
	q := gen.New(db.SQL())
	sessionID, repoPath := seedChatSession(t, q)

	prov := &chatStreamProvider{sessionID: "sess-1"}
	cr := NewChatRunner(db.SQL(), func(AgentConfig) Provider { return prov }, &capturePub{}, 2)

	if err := cr.SendMessage(context.Background(), sessionID, repoPath, "first"); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	firstWorktree := prov.lastInput.RepoPath
	if prov.lastInput.ResumeSessionID != "" {
		t.Fatalf("first turn should have no resume id, got %q", prov.lastInput.ResumeSessionID)
	}

	// Second turn: the stored session id should be threaded back in.
	prov.sessionID = "sess-2"
	if err := cr.SendMessage(context.Background(), sessionID, repoPath, "second"); err != nil {
		t.Fatalf("second turn: %v", err)
	}
	if prov.lastInput.ResumeSessionID != "sess-1" {
		t.Fatalf("second turn should resume sess-1, got %q", prov.lastInput.ResumeSessionID)
	}
	if prov.lastInput.RepoPath != firstWorktree {
		t.Fatalf("second turn should reuse the first worktree %q, got %q", firstWorktree, prov.lastInput.RepoPath)
	}
}

// TestChatRunner_SendMessage_Saturated verifies the concurrency budget is
// enforced: with a 1-slot runner, a turn started while another holds the slot
// is rejected with ErrChatSaturated (different session so it's not ErrChatBusy).
func TestChatRunner_SendMessage_Saturated(t *testing.T) {
	db := openChatTestDB(t)
	q := gen.New(db.SQL())
	sessA, repoPath := seedChatSession(t, q)
	sessB, _ := seedChatSession(t, q)

	// A provider that blocks until released, so the first turn holds the slot.
	release := make(chan struct{})
	entered := make(chan struct{})
	blocking := providerFunc(func(_ context.Context, _ RunInput, _ chan<- LogEntry) (Result, error) {
		close(entered)
		<-release
		return Result{Status: "completed"}, nil
	})
	cr := NewChatRunner(db.SQL(), func(AgentConfig) Provider { return blocking }, &capturePub{}, 1)

	go func() { _ = cr.SendMessage(context.Background(), sessA, repoPath, "hold the slot") }()
	<-entered // the first turn now holds the single slot

	err := cr.SendMessage(context.Background(), sessB, repoPath, "should be rejected")
	if err != ErrChatSaturated {
		t.Fatalf("expected ErrChatSaturated, got %v", err)
	}
	close(release)
}

// TestChatRunner_Cancel verifies Cancel signals the in-flight turn: the
// provider run observes ctx cancellation and unblocks, and Cancel reports true
// while a turn is running / false when none is.
func TestChatRunner_Cancel(t *testing.T) {
	db := openChatTestDB(t)
	q := gen.New(db.SQL())
	sessID, repoPath := seedChatSession(t, q)

	entered := make(chan struct{})
	// Blocks until its context is cancelled, then returns — so the turn only
	// finishes if Cancel actually propagates cancellation.
	blocking := providerFunc(func(ctx context.Context, _ RunInput, _ chan<- LogEntry) (Result, error) {
		close(entered)
		<-ctx.Done()
		return Result{Status: "cancelled"}, ctx.Err()
	})
	cr := NewChatRunner(db.SQL(), func(AgentConfig) Provider { return blocking }, &capturePub{}, 1)

	// No turn running yet — Cancel is a no-op.
	if cr.Cancel(sessID) {
		t.Fatal("Cancel should return false when no turn is running")
	}

	done := make(chan error, 1)
	go func() { done <- cr.SendMessage(context.Background(), sessID, repoPath, "hang") }()
	<-entered // turn is now in-flight

	if !cr.Cancel(sessID) {
		t.Fatal("Cancel should return true for an in-flight turn")
	}

	select {
	case <-done: // turn unblocked because ctx was cancelled
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not unblock after Cancel — cancellation not propagated")
	}
}

// TestChatRunner_SendMessage_Busy verifies a second turn on the SAME session
// while one is in flight is rejected with ErrChatBusy (not ErrChatSaturated —
// the runner has spare slots here, so it's the per-session guard that fires).
func TestChatRunner_SendMessage_Busy(t *testing.T) {
	db := openChatTestDB(t)
	q := gen.New(db.SQL())
	sessID, repoPath := seedChatSession(t, q)

	release := make(chan struct{})
	entered := make(chan struct{})
	blocking := providerFunc(func(_ context.Context, _ RunInput, _ chan<- LogEntry) (Result, error) {
		close(entered)
		<-release
		return Result{Status: "completed"}, nil
	})
	// Two slots free, so saturation can't be the cause of a rejection.
	cr := NewChatRunner(db.SQL(), func(AgentConfig) Provider { return blocking }, &capturePub{}, 2)

	go func() { _ = cr.SendMessage(context.Background(), sessID, repoPath, "first") }()
	<-entered // first turn holds the session

	err := cr.SendMessage(context.Background(), sessID, repoPath, "second on same session")
	if err != ErrChatBusy {
		t.Fatalf("expected ErrChatBusy for a concurrent turn on the same session, got %v", err)
	}
	close(release)
}

// providerFunc adapts a func to the Provider interface for tests.
type providerFunc func(context.Context, RunInput, chan<- LogEntry) (Result, error)

func (f providerFunc) Run(ctx context.Context, in RunInput, ch chan<- LogEntry) (Result, error) {
	return f(ctx, in, ch)
}
