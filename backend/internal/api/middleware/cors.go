// Package middleware provides HTTP middleware for the API router.
package middleware

import (
	"net/http"
	"strings"
)

// CORS returns middleware that sets permissive CORS headers.
// origins is a comma-separated list of allowed origins; "*" allows all.
func CORS(origins string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed := false

			if origins == "*" {
				allowed = true
			} else {
				for _, o := range strings.Split(origins, ",") {
					if strings.TrimSpace(o) == origin {
						allowed = true
						break
					}
				}
			}

			if allowed && origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
