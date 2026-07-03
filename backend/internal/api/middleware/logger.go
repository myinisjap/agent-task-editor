package middleware

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Hijack forwards to the underlying ResponseWriter so WebSocket upgrades work.
// nhooyr.io/websocket v1.8.x uses http.Hijacker directly rather than http.ResponseController.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return rw.ResponseWriter.(http.Hijacker).Hijack()
}

// Logger logs each request with method, path, status, duration, and request ID.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration", time.Since(start).String(),
			"request_id", chimiddleware.GetReqID(r.Context()),
		)
	})
}

// LoggerFromContext returns a logger scoped with the request ID found in ctx,
// falling back to the default logger if no request ID is present.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if id := chimiddleware.GetReqID(ctx); id != "" {
		return slog.With("request_id", id)
	}
	return slog.Default()
}
