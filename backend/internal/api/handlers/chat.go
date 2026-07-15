package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

// Terminal runs interactive chat sessions in a PTY. Implemented by
// agent.TerminalManager; may be nil where no manager is wired, in which case the
// terminal endpoint reports the feature unavailable.
type Terminal interface {
	Attach(ctx context.Context, sessionID, repoPath, provider, model string, resume bool, conn *websocket.Conn) error
	Stop(sessionID string)
}

// ChatHandler owns interactive chat session CRUD and the PTY terminal upgrade.
// Chat is separate from the task/workflow state machine — a session binds a repo
// + provider + git worktree, and the terminal runs the provider's interactive
// CLI in that worktree (see agent.TerminalManager).
type ChatHandler struct {
	q    *gen.Queries
	hub  *ws.Hub
	term Terminal
	// auth mirrors the /ws endpoint: WS handshakes can't carry the bearer header,
	// so a single-use ?ticket= (minted by the bearer-gated POST /ws-ticket) is
	// validated here. Empty bearerToken = open (no auth), same as ServeWS.
	bearerToken string
	corsOrigins string
}

func NewChatHandler(q *gen.Queries, hub *ws.Hub, term Terminal, bearerToken, corsOrigins string) *ChatHandler {
	return &ChatHandler{q: q, hub: hub, term: term, bearerToken: bearerToken, corsOrigins: corsOrigins}
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

// Get returns a session. The terminal has no server-side transcript — history
// lives in the CLI's own session store and the browser's terminal scrollback.
func (h *ChatHandler) Get(w http.ResponseWriter, r *http.Request) {
	sess, err := h.q.GetChatSession(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		Err(w, http.StatusNotFound, "chat session not found")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"session": sess})
}

// Delete removes the session and its git worktree, and kills any running
// terminal process for it. Best-effort on the worktree — a failure there still
// deletes the row so the UI isn't stuck with an undeletable session.
func (h *ChatHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := h.q.GetChatSession(r.Context(), id)
	if err != nil {
		Err(w, http.StatusNotFound, "chat session not found")
		return
	}
	if h.term != nil {
		h.term.Stop(id)
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

// Terminal upgrades to a WebSocket carrying the session's interactive CLI. The
// PTY runs in the session's repo worktree, provisioned here on first connect so
// edits persist across reconnects. Auth: single-use ?ticket= (see ServeWS).
func (h *ChatHandler) Terminal(w http.ResponseWriter, r *http.Request) {
	if h.term == nil {
		Err(w, http.StatusServiceUnavailable, "terminal is not available")
		return
	}
	if h.bearerToken != "" && !h.hub.ConsumeTicket(r.URL.Query().Get("ticket")) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	id := chi.URLParam(r, "id")
	sess, err := h.q.GetChatSession(r.Context(), id)
	if err != nil {
		http.Error(w, "chat session not found", http.StatusNotFound)
		return
	}
	repo, err := h.q.GetRepo(r.Context(), sess.RepoID)
	if err != nil {
		http.Error(w, "repo lookup failed", http.StatusInternalServerError)
		return
	}

	// An existing worktree means this session has launched a terminal before, so
	// ask the CLI to continue its most recent session in that worktree's cwd
	// (each session has its own worktree, so "most recent in cwd" is this
	// session's history — no cross-session mixups). Captured before provisioning.
	resume := sess.WorktreePath != ""

	// Provision the worktree on first connect and persist it; reuse afterwards so
	// terminal edits survive reconnects. This is the cwd the CLI runs in.
	workDir := sess.WorktreePath
	if workDir == "" {
		wtPath, _, _, perr := agent.ProvisionChatWorktree(r.Context(), repo.Path, id, sess.Title)
		if perr != nil {
			http.Error(w, "provision worktree failed", http.StatusInternalServerError)
			return
		}
		if err := h.q.SetChatSessionWorktree(r.Context(), gen.SetChatSessionWorktreeParams{WorktreePath: wtPath, ID: id}); err != nil {
			http.Error(w, "persist worktree failed", http.StatusInternalServerError)
			return
		}
		workDir = wtPath
	}

	var originPatterns []string
	if h.corsOrigins == "*" || h.corsOrigins == "" {
		originPatterns = []string{"*"}
	} else {
		for _, o := range strings.Split(h.corsOrigins, ",") {
			if s := strings.TrimSpace(o); s != "" {
				originPatterns = append(originPatterns, s)
			}
		}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: originPatterns})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := h.term.Attach(r.Context(), id, workDir, sess.Provider, sess.Model, resume, conn); err != nil {
		conn.Close(websocket.StatusInternalError, err.Error())
	}
}

// ensure the TerminalManager satisfies Terminal at compile time.
var _ Terminal = (*agent.TerminalManager)(nil)
