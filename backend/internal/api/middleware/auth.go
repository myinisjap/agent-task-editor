package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
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
func BearerAuth(bearerToken string, namedTokens map[string]string) func(http.Handler) http.Handler {
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

			if !matched {
				w.Header().Set("WWW-Authenticate", `Bearer realm="agent-task-editor"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), actorKey, actor)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
