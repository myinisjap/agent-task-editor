package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/middleware"
)

// TestRecover_CatchesPanicAndReturns500 verifies a panicking handler is
// converted into a 500 response instead of crashing the server.
func TestRecover_CatchesPanicAndReturns500(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	middleware.Recover(inner).ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

// TestRecover_PassesThroughWhenNoPanic verifies the wrapped handler's normal
// response is unaffected when it doesn't panic.
func TestRecover_PassesThroughWhenNoPanic(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	middleware.Recover(inner).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	if w.Body.String() != "created" {
		t.Fatalf("body = %q, want %q", w.Body.String(), "created")
	}
}
