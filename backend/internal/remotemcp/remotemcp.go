// Package remotemcp serves the board MCP tools (internal/boardtools) over the
// MCP streamable-HTTP transport, so a remote client such as a claude.ai
// custom connector can manage the board. It is disabled unless MCP_PUBLIC_URL
// is set.
//
// Remote clients can't send the backend's static API_TOKEN, so the endpoint is
// guarded by its own small OAuth 2.1 authorization server (oauth.go) that signs
// users in with Google and only admits the emails in MCP_ALLOWED_EMAILS.
// Tokens, and the client ids handed out by dynamic client registration, are
// HMAC-signed and stateless, so they survive restarts without a DB table.
//
// Tool calls are served by calling the API router in-process: each request is
// marked with middleware.WithTrustedActor(email), so it skips BearerAuth and
// is recorded under the signed-in user's email like a named API token.
//
// Routes (relative to the backend; MCP_PUBLIC_URL is where they're reachable
// from outside):
//
//	POST /mcp                                     JSON-RPC (streamable HTTP, JSON responses only)
//	GET  /.well-known/oauth-protected-resource/*  RFC 9728 resource metadata
//	GET  /.well-known/oauth-authorization-server/* RFC 8414 server metadata
//	POST /oauth/register                          RFC 7591 dynamic client registration
//	GET  /oauth/authorize                         starts sign-in (redirects to Google)
//	GET  /oauth/callback                          Google redirects back here
//	POST /oauth/token                             code / refresh-token exchange
package remotemcp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/boardtools"
)

// Config configures the remote MCP endpoint.
type Config struct {
	// PublicURL is the externally reachable base URL the routes are served
	// under, without a trailing slash (e.g. "https://example.com/tasks").
	PublicURL          string
	GoogleClientID     string
	GoogleClientSecret string
	AllowedEmails      []string
	// RedirectHosts are the hosts clients may register https redirect URIs on.
	RedirectHosts []string

	// Google endpoints; overridable for tests.
	GoogleAuthURL  string
	GoogleTokenURL string
}

// supportedProtocolVersions lists the MCP revisions this server speaks, newest
// first. JSON-only responses and no server-initiated messages keep all three
// compatible.
var supportedProtocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const serverInstructions = "Tools for the Agent Task Editor kanban board. Tasks move between labels (columns); " +
	"a task on an agent-triggerable label such as \"work\" is picked up by an AI agent automatically. " +
	"Use list_tasks/get_task to see state, list_runs/get_run_logs/get_diff to inspect agent work, " +
	"reply_to_run to answer an agent waiting on a human, and approve_task/reject_task/move_task to move work along."

// Server is the remote MCP endpoint plus its OAuth authorization server.
type Server struct {
	cfg     Config
	api     http.Handler
	tools   []boardtools.Tool
	key     []byte
	allowed map[string]bool
	http    *http.Client
	now     func() time.Time

	mu      sync.Mutex
	pending map[string]pendingAuth // Google state -> in-flight /authorize
	codes   map[string]authCode    // authorization code -> grant
}

// New validates cfg and returns a Server whose tool calls are served by api
// (the backend router).
func New(cfg Config, api http.Handler) (*Server, error) {
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	u, err := url.Parse(cfg.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("MCP_PUBLIC_URL must be an absolute http(s) URL, got %q", cfg.PublicURL)
	}
	if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" {
		return nil, errors.New("MCP_GOOGLE_CLIENT_ID and MCP_GOOGLE_CLIENT_SECRET are required when MCP_PUBLIC_URL is set")
	}
	allowed := map[string]bool{}
	for _, e := range cfg.AllowedEmails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			allowed[e] = true
		}
	}
	for i, h := range cfg.RedirectHosts {
		cfg.RedirectHosts[i] = strings.ToLower(strings.TrimSpace(h))
	}
	if len(allowed) == 0 {
		return nil, errors.New("MCP_ALLOWED_EMAILS is required when MCP_PUBLIC_URL is set")
	}
	if cfg.GoogleAuthURL == "" {
		cfg.GoogleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if cfg.GoogleTokenURL == "" {
		cfg.GoogleTokenURL = "https://oauth2.googleapis.com/token"
	}

	// Derive the token-signing key from the Google client secret so there's no
	// extra secret to manage; rotating the Google secret revokes every token.
	mac := hmac.New(sha256.New, []byte(cfg.GoogleClientSecret))
	mac.Write([]byte("agent-task-editor remote MCP token key v1"))

	return &Server{
		cfg:     cfg,
		api:     api,
		tools:   boardtools.Select(boardtools.RemoteTools),
		key:     mac.Sum(nil),
		allowed: allowed,
		http:    &http.Client{Timeout: 15 * time.Second},
		now:     time.Now,
		pending: map[string]pendingAuth{},
		codes:   map[string]authCode{},
	}, nil
}

// Wrap returns a handler that serves the MCP and OAuth routes and passes
// everything else to next.
func (s *Server) Wrap(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.handleResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-protected-resource/", s.handleResourceMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleServerMetadata)
	mux.HandleFunc("/.well-known/oauth-authorization-server/", s.handleServerMetadata)
	mux.HandleFunc("/oauth/register", s.handleRegister)
	mux.HandleFunc("/oauth/authorize", s.handleAuthorize)
	mux.HandleFunc("/oauth/callback", s.handleCallback)
	mux.HandleFunc("/oauth/token", s.handleToken)
	own := middleware.Recover(middleware.Logger(mux))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// These routes are exposed without the reverse proxy's login wall, so
		// refuse dot-segments outright rather than trust every proxy in front
		// to normalize them before routing (e.g. /tasks/oauth/../api/...).
		if strings.Contains(r.URL.Path, "/../") || strings.HasSuffix(r.URL.Path, "/..") ||
			strings.Contains(r.URL.Path, "/./") || strings.Contains(r.URL.Path, "//") {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		if _, pattern := mux.Handler(r); pattern != "" {
			own.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// resourceURL is the MCP endpoint's public URL, used as the token audience.
func (s *Server) resourceURL() string { return s.cfg.PublicURL + "/mcp" }

// wellKnownURL builds an RFC 8414/9728-style metadata URL: the well-known
// segment is inserted between the host and the path of target.
func wellKnownURL(target, name string) string {
	u, _ := url.Parse(target)
	return u.Scheme + "://" + u.Host + "/.well-known/" + name + strings.TrimRight(u.Path, "/")
}

// --- MCP transport ---

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// No server-initiated stream (GET) and no sessions to end (DELETE).
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	email, ok := s.authenticate(r)
	if !ok {
		challenge := fmt.Sprintf(`Bearer resource_metadata=%q`, wellKnownURL(s.resourceURL(), "oauth-protected-resource"))
		if r.Header.Get("Authorization") != "" {
			challenge += `, error="invalid_token"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32600, Message: "request too large"}})
		return
	}
	ctx := middleware.WithTrustedActor(r.Context(), email)

	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		var batch []rpcRequest
		if err := json.Unmarshal(body, &batch); err != nil {
			writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			return
		}
		var out []rpcResponse
		for _, req := range batch {
			if resp, ok := s.dispatch(ctx, req); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	resp, ok := s.dispatch(ctx, req)
	if !ok {
		// Notifications and responses get 202 with no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// dispatch handles one JSON-RPC message. ok is false for notifications, which
// get no response.
func (s *Server) dispatch(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	if req.ID == nil || req.Method == "" {
		return rpcResponse{}, false
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := supportedProtocolVersions[0]
		for _, v := range supportedProtocolVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "agent-task-editor", "version": "1.0.0"},
			"instructions":    serverInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": boardtools.Defs(s.tools)}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		if !slices.ContainsFunc(s.tools, func(t boardtools.Tool) bool { return t.Name == p.Name }) {
			resp.Error = &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
			break
		}
		c := &boardtools.Client{HTTP: inProcess{h: s.api, ctx: ctx}}
		text, isErr := c.Call(s.tools, p.Name, p.Arguments)
		resp.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return resp, true
}

// inProcess serves boardtools API requests by calling the router directly,
// under a context that marks the caller as already authenticated.
type inProcess struct {
	h   http.Handler
	ctx context.Context
}

func (p inProcess) Do(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	p.h.ServeHTTP(rec, req.WithContext(p.ctx))
	return rec.Result(), nil
}

// authenticate returns the email of a valid access token's subject.
func (s *Server) authenticate(r *http.Request) (string, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return "", false
	}
	c, err := s.verify(strings.TrimSpace(raw), tokenAccess)
	if err != nil {
		return "", false
	}
	if c.Aud != s.resourceURL() || !s.allowed[c.Sub] {
		slog.Warn("remote MCP: rejected token", "sub", c.Sub)
		return "", false
	}
	return c.Sub, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
