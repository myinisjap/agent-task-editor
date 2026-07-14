package agent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ChatRunner powers interactive chat sessions: free-form conversations against
// a repo, separate from the task/workflow state machine. Each user message runs
// exactly one provider turn (one CLI invocation) in the session's git worktree,
// streaming output over WebSocket and resuming the provider session between
// turns. Unlike the task Pool, there is no workflow-outcome handling — a chat
// turn just streams and records its transcript.
type ChatRunner struct {
	db       *sql.DB
	q        *gen.Queries
	factory  func(cfg AgentConfig) Provider
	pub      Publisher
	sem      chan struct{} // bounds concurrent chat turns, independent of the task pool

	// mu guards running so a human can cancel an in-flight turn.
	mu      sync.Mutex
	running map[string]context.CancelFunc // session_id -> cancel
}

// ErrChatBusy means this session already has an in-flight turn (chat is
// strictly turn-based; one message must finish before the next starts).
var ErrChatBusy = errors.New("chat session already has a turn in progress")

// ErrChatSaturated means the global chat concurrency budget is exhausted.
var ErrChatSaturated = errors.New("chat is at capacity, try again shortly")

// NewChatRunner builds a ChatRunner. maxConcurrent bounds simultaneous turns
// across all sessions (CHAT_MAX_WORKERS); pass >=1.
func NewChatRunner(db *sql.DB, factory func(cfg AgentConfig) Provider, pub Publisher, maxConcurrent int) *ChatRunner {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &ChatRunner{
		db:      db,
		q:       gen.New(db),
		factory: factory,
		pub:     pub,
		sem:     make(chan struct{}, maxConcurrent),
		running: make(map[string]context.CancelFunc),
	}
}

// Cancel stops an in-flight turn for a session, if any. Returns false if the
// session has no turn running (on this instance).
func (c *ChatRunner) Cancel(sessionID string) bool {
	c.mu.Lock()
	cancel := c.running[sessionID]
	c.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// SendMessage runs one turn for the session: provisions the worktree on first
// use, invokes the provider with the user's message (resuming the prior session
// where the provider supports it), streams and persists the response, and
// records the new provider session id for the next turn. It blocks until the
// turn completes. The user's message is persisted before the turn starts.
//
// repoPath is the repo's base checkout (a worktree is cut from it); it is passed
// in rather than looked up so the handler owns the repo lookup + validation.
func (c *ChatRunner) SendMessage(ctx context.Context, sessionID, repoPath, message string) error {
	// One turn per session at a time. Reserve the session slot before the
	// global budget so a busy session fails fast without holding a token.
	c.mu.Lock()
	if _, busy := c.running[sessionID]; busy {
		c.mu.Unlock()
		return ErrChatBusy
	}
	// Placeholder cancel installed under the lock; replaced with the real one
	// once we derive the run context below. Marks the session as reserved.
	c.running[sessionID] = func() {}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.running, sessionID)
		c.mu.Unlock()
	}()

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	default:
		return ErrChatSaturated
	}

	sess, err := c.q.GetChatSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get chat session: %w", err)
	}

	// Persist the user's message first so the transcript is coherent even if the
	// turn fails, and echo it over WS for other connected clients.
	if err := c.record(ctx, sessionID, "user", message); err != nil {
		return err
	}

	// Provision the session's worktree on first message; reuse it afterwards so
	// in-progress edits (e.g. a half-resolved merge conflict) persist across turns.
	workDir := sess.WorktreePath
	if workDir == "" {
		wtPath, _, _, perr := provisionWorktree(ctx, repoPath, sessionID, chatWorktreeTitle(sess))
		if perr != nil {
			return fmt.Errorf("provision chat worktree: %w", perr)
		}
		if err := c.q.SetChatSessionWorktree(ctx, gen.SetChatSessionWorktreeParams{
			WorktreePath: wtPath,
			ID:           sessionID,
		}); err != nil {
			return fmt.Errorf("persist chat worktree: %w", err)
		}
		workDir = wtPath
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.running[sessionID] = cancel
	c.mu.Unlock()

	provider := c.factory(AgentConfig{Provider: sess.Provider, Model: sess.Model})
	input := RunInput{
		RunID:           uuid.NewString(),
		Task:            Task{ID: sessionID, Title: chatWorktreeTitle(sess), Description: message, RepoPath: repoPath},
		AgentConfig:     AgentConfig{Provider: sess.Provider, Model: sess.Model},
		RepoPath:        workDir,
		ResumeSessionID: sess.ProviderSessionID,
	}

	logCh := make(chan LogEntry, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range logCh {
			// Persist and broadcast every streamed line as a chat message.
			if err := c.record(runCtx, sessionID, string(entry.Type), entry.Content); err != nil {
				slog.Warn("chat: record message", "session_id", sessionID, "err", err)
			}
		}
	}()

	result, runErr := provider.Run(runCtx, input, logCh)
	close(logCh)
	<-done

	// Persist the provider session id so the next turn resumes this conversation.
	if result.SessionID != "" {
		if err := c.q.SetChatSessionResume(context.Background(), gen.SetChatSessionResumeParams{
			ProviderSessionID: result.SessionID,
			ID:                sessionID,
		}); err != nil {
			slog.Warn("chat: persist provider session", "session_id", sessionID, "err", err)
		}
	}

	c.publish("chat.turn_done", map[string]any{
		"session_id": sessionID,
		"status":     result.Status,
	})

	if runErr != nil {
		return fmt.Errorf("provider run: %w", runErr)
	}
	return nil
}

// record inserts a chat message and broadcasts it to subscribers.
func (c *ChatRunner) record(ctx context.Context, sessionID, msgType, content string) error {
	msg, err := c.q.CreateChatMessage(ctx, gen.CreateChatMessageParams{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		Type:      msgType,
		Content:   content,
	})
	if err != nil {
		return fmt.Errorf("create chat message: %w", err)
	}
	c.publish("chat.message", map[string]any{
		"session_id": sessionID,
		"message": map[string]any{
			"id":         msg.ID,
			"type":       msg.Type,
			"content":    msg.Content,
			"created_at": msg.CreatedAt.Format(time.RFC3339),
		},
	})
	return nil
}

func (c *ChatRunner) publish(eventType string, payload map[string]any) {
	if c.pub != nil {
		c.pub.Publish(eventType, payload)
	}
}

// chatWorktreeTitle produces a branch-friendly title for the session's worktree.
func chatWorktreeTitle(sess gen.ChatSession) string {
	if sess.Title != "" {
		return sess.Title
	}
	return "chat"
}
