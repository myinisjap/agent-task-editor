package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// ChatSender runs one interactive-chat turn for a session. Implemented by
// agent.ChatRunner; may be nil in contexts where no runner is wired, in which
// case sending a message reports the feature unavailable.
type ChatSender interface {
	SendMessage(ctx context.Context, sessionID, repoPath, message string) error
	Cancel(sessionID string) bool
}

// ChatHandler owns interactive chat session CRUD and message dispatch. Chat is
// separate from the task/workflow state machine — see agent.ChatRunner.
type ChatHandler struct {
	q      *gen.Queries
	sender ChatSender
}

func NewChatHandler(q *gen.Queries, sender ChatSender) *ChatHandler {
	return &ChatHandler{q: q, sender: sender}
}

func (h *ChatHandler) List(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.q.ListChatSessions(r.Context())
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusOK, sessions)
}

type createChatReq struct {
	RepoID   string `json:"repo_id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Title    string `json:"title"`
}

func (h *ChatHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createChatReq
	if err := decode(r, &req); err != nil {
		Err(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.RepoID == "" || req.Provider == "" {
		Err(w, http.StatusBadRequest, "repo_id and provider are required")
		return
	}
	// Validate the repo exists before creating a session bound to it.
	if _, err := h.q.GetRepo(r.Context(), req.RepoID); err != nil {
		Err(w, http.StatusBadRequest, "unknown repo_id")
		return
	}
	sess, err := h.q.CreateChatSession(r.Context(), gen.CreateChatSessionParams{
		ID:       uuid.NewString(),
		RepoID:   req.RepoID,
		Provider: req.Provider,
		Model:    req.Model,
		Title:    req.Title,
	})
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	JSON(w, http.StatusCreated, sess)
}

// Get returns a session plus its full message transcript.
func (h *ChatHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := h.q.GetChatSession(r.Context(), id)
	if err != nil {
		Err(w, http.StatusNotFound, "chat session not found")
		return
	}
	msgs, err := h.q.ListChatMessages(r.Context(), id)
	if err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	if msgs == nil {
		msgs = []gen.ChatMessage{}
	}
	JSON(w, http.StatusOK, map[string]any{"session": sess, "messages": msgs})
}

// Delete removes the session (and its messages, via ON DELETE CASCADE) and its
// git worktree. Best-effort on the worktree — a failure there still deletes the
// session row so the UI isn't stuck with an undeletable session.
func (h *ChatHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := h.q.GetChatSession(r.Context(), id)
	if err != nil {
		Err(w, http.StatusNotFound, "chat session not found")
		return
	}
	if sess.WorktreePath != "" {
		if repo, rerr := h.q.GetRepo(r.Context(), sess.RepoID); rerr == nil {
			_ = agent.RemoveWorktree(r.Context(), repo.Path, sess.WorktreePath)
		}
	}
	if err := h.q.DeleteChatSession(r.Context(), id); err != nil {
		Err(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sendChatReq struct {
	Message string `json:"message"`
}

// SendMessage runs one chat turn. The turn streams over WebSocket (chat.message
// / chat.turn_done events), so this endpoint kicks the turn off in the
// background and returns 202 immediately rather than blocking for the whole run.
func (h *ChatHandler) SendMessage(w http.ResponseWriter, r *http.Request) {
	if h.sender == nil {
		Err(w, http.StatusServiceUnavailable, "chat is not available")
		return
	}
	id := chi.URLParam(r, "id")
	var req sendChatReq
	if err := decode(r, &req); err != nil || req.Message == "" {
		Err(w, http.StatusBadRequest, "message is required")
		return
	}
	sess, err := h.q.GetChatSession(r.Context(), id)
	if err != nil {
		Err(w, http.StatusNotFound, "chat session not found")
		return
	}
	repo, err := h.q.GetRepo(r.Context(), sess.RepoID)
	if err != nil {
		Err(w, http.StatusInternalServerError, "repo lookup failed")
		return
	}

	// Run the turn in the background so a long agent turn doesn't hold the HTTP
	// request open. Use a detached context — the turn must outlive this request.
	// The runner rejects a second concurrent turn per session (ErrChatBusy).
	go func() {
		_ = h.sender.SendMessage(context.Background(), id, repo.Path, req.Message)
	}()
	w.WriteHeader(http.StatusAccepted)
}

// Cancel signals an in-flight turn to stop. 202 if signalled, 409 if no turn
// is currently running for the session.
func (h *ChatHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if h.sender == nil {
		Err(w, http.StatusServiceUnavailable, "chat is not available")
		return
	}
	if !h.sender.Cancel(chi.URLParam(r, "id")) {
		Err(w, http.StatusConflict, "no turn in progress")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// ensure the ChatRunner satisfies ChatSender at compile time.
var _ ChatSender = (*agent.ChatRunner)(nil)
