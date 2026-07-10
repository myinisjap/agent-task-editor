package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/api/handlers"
	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

func TestWSTicketHandler_IssueTicket_ReturnsTicket(t *testing.T) {
	hub := ws.NewHub()
	h := handlers.NewWSTicketHandler(hub)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/ws-ticket", nil)
	w := httptest.NewRecorder()
	h.IssueTicket(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var body struct {
		Ticket    string `json:"ticket"`
		ExpiresIn string `json:"expires_in"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Ticket == "" {
		t.Error("expected non-empty ticket")
	}
	if body.ExpiresIn == "" {
		t.Error("expected non-empty expires_in")
	}
}

func TestWSTicketHandler_IssueTicket_ReturnsUniqueTickets(t *testing.T) {
	hub := ws.NewHub()
	h := handlers.NewWSTicketHandler(hub)

	issue := func() string {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ws-ticket", nil)
		w := httptest.NewRecorder()
		h.IssueTicket(w, req)
		var body struct {
			Ticket string `json:"ticket"`
		}
		_ = json.NewDecoder(w.Body).Decode(&body)
		return body.Ticket
	}

	t1 := issue()
	t2 := issue()
	if t1 == "" || t2 == "" {
		t.Fatal("expected non-empty tickets")
	}
	if t1 == t2 {
		t.Error("expected two calls to return different tickets")
	}
}
