package handlers

import (
	"net/http"

	"github.com/myinisjap/agent-task-editor/backend/internal/ws"
)

// WSTicketHandler mints short-lived WebSocket auth tickets.
type WSTicketHandler struct {
	hub *ws.Hub
}

// NewWSTicketHandler constructs a WSTicketHandler for the given hub.
func NewWSTicketHandler(hub *ws.Hub) *WSTicketHandler {
	return &WSTicketHandler{hub: hub}
}

// IssueTicket returns a random, single-use ticket valid for ~30s, used to
// authenticate the WebSocket upgrade (GET /ws?ticket=...) without putting the
// long-lived API token in the URL. This endpoint itself sits behind the
// normal Bearer auth middleware, so minting a ticket requires already
// holding the token.
//
// POST /api/v1/ws-ticket
func (h *WSTicketHandler) IssueTicket(w http.ResponseWriter, r *http.Request) {
	ticket, err := h.hub.IssueTicket()
	if err != nil {
		Err(w, http.StatusInternalServerError, "failed to issue ticket")
		return
	}
	JSON(w, http.StatusOK, map[string]string{"ticket": ticket, "expires_in": "30s"})
}
