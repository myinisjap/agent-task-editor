package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BearerAuth returns a middleware that requires a static Bearer token when
// bearerToken is non-empty. If bearerToken is empty the middleware is a no-op.
func BearerAuth(bearerToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if bearerToken == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Allow WebSocket upgrades to pass through (browser can't set auth headers in WS)
			if r.Header.Get("Upgrade") == "websocket" {
				next.ServeHTTP(w, r)
				return
			}
			auth := r.Header.Get("Authorization")
			token := strings.TrimPrefix(auth, "Bearer ")
			if subtle.ConstantTimeCompare([]byte(token), []byte(bearerToken)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="agent-task-editor"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
