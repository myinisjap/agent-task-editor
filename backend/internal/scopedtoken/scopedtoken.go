// Package scopedtoken mints short-lived, route-scoped bearer tokens for the MCP
// sidecars (mcp-server, mcp-board) that run next to an agent.
//
// Agents run as the same OS user as the backend, so anything handed to a
// sidecar (env, config file) is readable by the agent. Giving the sidecar the
// admin API_TOKEN would let an agent approve gates, edit configs and download
// backups. Instead each run/chat session gets a random token that the auth
// middleware accepts ONLY for the few routes that sidecar really calls, scoped
// to that run's own task, and that stops working when the run/session ends.
//
// Only the SHA-256 of a token is kept (in memory; grants never outlive the
// process, and neither do the runs they belong to).
package scopedtoken

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
)

// Kind identifies what a grant was minted for.
type Kind string

const (
	// KindRun is a task run's mcp-server sidecar (create_subtask).
	KindRun Kind = "run"
	// KindChat is a chat session's mcp-board sidecar.
	KindChat Kind = "chat"
)

// Grant describes what a scoped token may do.
type Grant struct {
	Kind   Kind
	RunID  string // KindRun: the agent run
	TaskID string // KindRun: the only task the token may touch
	Chat   string // KindChat: the chat session id
	// Profile (KindChat) widens the grant beyond the three create routes to the
	// exact method+path pairs of that board-tool profile: "readonly", "full" or
	// "admin" (MCP_BOARD_TOOL_PROFILE). Empty = the narrow create grant.
	Profile string
}

// Actor is the name recorded for requests made with this grant.
func (g Grant) Actor() string {
	if g.Kind == KindChat {
		return "chat:" + g.Chat
	}
	return "agent:" + g.RunID
}

// Allows reports whether the grant may call method+path. Methods and paths are matched
// exactly (no prefixes), so nothing else under /api/v1 is reachable.
func (g Grant) Allows(method, path string) bool {
	switch g.Kind {
	case KindRun:
		// Exactly what cmd/mcp-server calls: create_subtask.
		return g.TaskID != "" && method == http.MethodPost &&
			path == "/api/v1/tasks/"+g.TaskID+"/subtasks"
	case KindChat:
		// Exactly what cmd/mcp-board calls: list_repos, list_workflows, create_task.
		if g.Profile != "" {
			return g.profileAllows(method, path)
		}
		switch path {
		case "/api/v1/repos", "/api/v1/workflows":
			return method == http.MethodGet
		case "/api/v1/tasks":
			return method == http.MethodPost
		}
	}
	return false
}

// Registry holds live grants keyed by token hash.
type Registry struct {
	mu     sync.RWMutex
	grants map[string]Grant
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{grants: map[string]Grant{}} }

// Default is the process-wide registry used by the server's auth middleware
// and the provider runners.
var Default = NewRegistry()

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Issue mints a fresh 32-byte random token for g. The plaintext token is
// returned once; only its hash is stored.
func (r *Registry) Issue(g Grant) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token := "ate_" + hex.EncodeToString(b[:])
	r.mu.Lock()
	r.grants[hash(token)] = g
	r.mu.Unlock()
	return token, nil
}

// Revoke invalidates token. Safe to call repeatedly or with "".
func (r *Registry) Revoke(token string) {
	if token == "" {
		return
	}
	r.mu.Lock()
	delete(r.grants, hash(token))
	r.mu.Unlock()
}

// Lookup returns the live grant for token.
func (r *Registry) Lookup(token string) (Grant, bool) {
	if !strings.HasPrefix(token, "ate_") {
		return Grant{}, false
	}
	r.mu.RLock()
	g, ok := r.grants[hash(token)]
	r.mu.RUnlock()
	return g, ok
}

// route is one allowed method + path pattern; "{}" matches exactly one
// non-empty path segment.
type route struct{ method, pattern string }

var (
	readRoutes = []route{
		{"GET", "repos"}, {"GET", "repos/{}"},
		{"GET", "workflows"}, {"GET", "workflows/{}"}, {"GET", "workflows/{}/export.yaml"},
		{"GET", "agents"},
		{"GET", "tasks"}, {"GET", "tasks/{}"}, {"GET", "tasks/{}/runs"},
		{"GET", "tasks/{}/runs/{}/logs"}, {"GET", "tasks/{}/diff"},
		{"GET", "needs-me"}, {"GET", "dashboard"}, {"GET", "dashboard/cost-by-task"},
		{"GET", "provider-configs"}, {"GET", "agents/{}"},
		{"GET", "templates"}, {"GET", "schedules"},
	}
	fullRoutes = []route{
		{"POST", "tasks"}, {"POST", "tasks/{}/runs/{}/reply"},
		{"POST", "tasks/{}/approve"}, {"POST", "tasks/{}/reject"},
		{"PATCH", "tasks/{}/label"}, {"PATCH", "tasks/{}/archive"},
		{"POST", "tasks/{}/runs/{}/cancel"}, {"POST", "tasks/{}/rerun"}, {"POST", "tasks/{}/pr"},
	}
	adminRoutes = []route{
		{"POST", "repos"}, {"PATCH", "repos/{}"}, {"POST", "repos/{}/runtime/detect"},
		{"POST", "workflows/import"}, {"PUT", "workflows/{}/yaml"},
		{"POST", "agents"}, {"PUT", "agents/{}"},
		{"POST", "templates"}, {"GET", "templates/{}"}, {"PUT", "templates/{}"},
		{"POST", "schedules"}, {"GET", "schedules/{}"}, {"PUT", "schedules/{}"},
	}
)

// profileAllows is an exact method+path allowlist: exactly the REST calls the
// board tools of the profile make, nothing else (no DELETE, no other routes).
func (g Grant) profileAllows(method, path string) bool {
	var sets [][]route
	switch strings.ToLower(g.Profile) {
	case "readonly":
		sets = [][]route{readRoutes}
	case "full":
		sets = [][]route{readRoutes, fullRoutes}
	case "admin":
		sets = [][]route{readRoutes, fullRoutes, adminRoutes}
	default:
		return false
	}
	rest, ok := strings.CutPrefix(path, "/api/v1/")
	if !ok {
		return false
	}
	segs := strings.Split(rest, "/")
	for _, set := range sets {
		for _, r := range set {
			if r.method == method && matchSegs(strings.Split(r.pattern, "/"), segs) {
				return true
			}
		}
	}
	return false
}

func matchSegs(pat, segs []string) bool {
	if len(pat) != len(segs) {
		return false
	}
	for i, p := range pat {
		if segs[i] == "" || (p != "{}" && p != segs[i]) {
			return false
		}
	}
	return true
}
