package middleware_test

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
)

// TestLogger_WrapsHandlerAndPreservesStatus verifies Logger calls through to
// the wrapped handler and doesn't alter the response status/body.
func TestLogger_WrapsHandlerAndPreservesStatus(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	})

	req := httptest.NewRequest(http.MethodGet, "/foo/bar", nil)
	w := httptest.NewRecorder()
	middleware.Logger(inner).ServeHTTP(w, req)

	if w.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTeapot)
	}
	if w.Body.String() != "hi" {
		t.Errorf("body = %q, want %q", w.Body.String(), "hi")
	}
}

// TestLogger_DefaultsStatusToOKWhenHandlerNeverWritesHeader verifies the
// wrapped responseWriter starts at 200 if the inner handler never calls
// WriteHeader explicitly (relying on the implicit 200 on first Write).
func TestLogger_DefaultsStatusToOKWhenHandlerNeverWritesHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	middleware.Logger(inner).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// hijackableRecorder adds a no-op Hijack to httptest.ResponseRecorder so
// Logger's wrapped responseWriter can be type-asserted to http.Hijacker, the
// same as nhooyr.io/websocket does on a real *http.response during a WS
// upgrade.
type hijackableRecorder struct {
	*httptest.ResponseRecorder
}

func (h hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	server, _ := net.Pipe()
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

// TestLogger_HijackForwardsToUnderlyingResponseWriter verifies the wrapped
// responseWriter's Hijack forwards to the underlying http.Hijacker (needed
// for WS upgrades to work through the Logger middleware).
func TestLogger_HijackForwardsToUnderlyingResponseWriter(t *testing.T) {
	var hijacked bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("expected the wrapped ResponseWriter to implement http.Hijacker")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("Hijack: %v", err)
		}
		hijacked = true
		_ = conn.Close()
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := hijackableRecorder{httptest.NewRecorder()}
	middleware.Logger(inner).ServeHTTP(rec, req)

	if !hijacked {
		t.Error("expected Hijack to be called and succeed")
	}
}

// TestLoggerFromContext_WithRequestID verifies LoggerFromContext returns a
// logger without panicking when a chi request ID is present in ctx.
func TestLoggerFromContext_WithRequestID(t *testing.T) {
	ctx := context.WithValue(context.Background(), chimiddleware.RequestIDKey, "req-123")
	logger := middleware.LoggerFromContext(ctx)
	if logger == nil {
		t.Fatal("expected a non-nil logger")
	}
}

// TestLoggerFromContext_WithoutRequestID verifies the fallback to the
// default logger when no request ID is in context.
func TestLoggerFromContext_WithoutRequestID(t *testing.T) {
	logger := middleware.LoggerFromContext(context.Background())
	if logger == nil {
		t.Fatal("expected a non-nil logger")
	}
}
