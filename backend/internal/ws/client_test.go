package ws

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestServeWS_UnauthorizedRejection_IsJSON verifies the pre-upgrade auth
// rejection (bad/missing ticket and token) emits a JSON body matching
// handlers.Err's {"error": "..."} shape rather than plain text.
func TestServeWS_UnauthorizedRejection_IsJSON(t *testing.T) {
	hub := NewHub()

	req := httptest.NewRequest("GET", "/ws", nil)
	w := httptest.NewRecorder()

	ServeWS(hub, w, req, "s3cr3t", "*", nil)

	resp := w.Result()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected JSON content-type, got %q", ct)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("expected JSON body, got decode error: %v", err)
	}
	if body["error"] != "unauthorized" {
		t.Errorf("expected error=unauthorized, got %v", body)
	}
}
