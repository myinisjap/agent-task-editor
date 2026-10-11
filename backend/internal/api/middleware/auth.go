package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/myinisjap/agent-task-editor/backend/internal/scopedtoken"
)

type ctxKey int

const actorKey ctxKey = iota

// ActorFromContext returns the resolved actor name for the request's bearer
// token (see BearerAuth), or "" if unauthenticated/anonymous — i.e. the
// legacy single shared token (or no auth at all) was used rather than a
// named token from APITokens.
func ActorFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(actorKey).(string); ok {
		return v
	}
	return ""
}

// BearerAuth returns a middleware that requires a Bearer token when either
// bearerToken (the legacy single shared token) or namedTokens (name -> token)
// is non-empty. If both are empty the middleware is a no-op.
//
// When the presented token matches an entry in namedTokens, that name is
// stored in the request context and can be retrieved via ActorFromContext —
// this lets handlers record *who* performed a human-triggered action (see
// task_label_history.actor_id). A match against the legacy bearerToken (or
// no auth configured at all) resolves to actor "", preserving prior
// anonymous behavior.
//
// Besides the admin tokens, a per-run/per-session scoped token minted by
// scopedtoken.Default (handed to the MCP sidecars) is accepted, but only for
// the exact routes that sidecar calls; any other route gets 403. Its actor is
// "agent:<run id>" or "chat:<session id>". In open mode (no tokens configured)
// the middleware stays a no-op and scoped tokens are irrelevant.
func BearerAuth(bearerToken string, namedTokens map[string]string) func(http.Handler) http.Handler {
	return BearerAuthScoped(bearerToken, namedTokens, scopedtoken.Default)
}

// BearerAuthScoped is BearerAuth with an explicit scoped-token registry.
func BearerAuthScoped(bearerToken string, namedTokens map[string]string, scoped *scopedtoken.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if bearerToken == "" && len(namedTokens) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			token := strings.TrimPrefix(auth, "Bearer ")

			// Check every named token candidate. We deliberately don't
			// short-circuit on the first match so the number of comparisons
			// performed doesn't leak which (if any) candidate matched via
			// timing — though, as with the single-token compare below, this
			// doesn't defend against timing differences *between* requests
			// with different numbers of configured tokens. That's an
			// accepted limitation matching the existing security posture of
			// this codebase.
			actor := ""
			matched := false
			for name, candidate := range namedTokens {
				if subtle.ConstantTimeCompare([]byte(token), []byte(candidate)) == 1 {
					actor = name
					matched = true
				}
			}

			if !matched && bearerToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(bearerToken)) == 1 {
				matched = true
				actor = ""
			}

			if !matched && scoped != nil {
				if g, ok := scoped.Lookup(token); ok {
					// RawPath != "" means the router (chi prefers RawPath)
					// could route differently from URL.Path; refuse.
					if r.URL.RawPath != "" || !g.Allows(r.Method, r.URL.Path) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusForbidden)
						_, _ = w.Write([]byte(`{"error":"forbidden"}`))
						return
					}
					ctx := context.WithValue(r.Context(), actorKey, g.Actor())
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			if !matched {
				// Inlined rather than reusing handlers.Err — importing the
				// handlers package here would risk a cycle since handlers
				// depends on middleware for auth context/actor lookups. This
				// mirrors Err's exact {"error": "..."} shape.
				w.Header().Set("WWW-Authenticate", `Bearer realm="agent-task-editor"`)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}

			ctx := context.WithValue(r.Context(), actorKey, actor)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
