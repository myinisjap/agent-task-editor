package ws

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/wsevent"
)

// resyncFrame is the pre-marshalled "resync" frame sent to a client that missed
// one or more events because its send buffer was full; it tells the client to
// refetch its state over REST. See Client.markLagged.
var resyncFrame = []byte(`{"type":"` + wsevent.TypeResync + `","payload":{}}`)

// Event is the JSON envelope sent to every WebSocket client. Payload is one of
// the typed structs in package wsevent; the wire contract is the WsEvent schema
// in openapi.yaml.
type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Hub fan-outs events to all connected clients.
// It satisfies both workflow.Publisher and agent.Publisher (same method set).
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}

	// tickets issues/validates short-lived WS auth tickets (see ticket.go).
	tickets *ticketStore
}

// NewHub creates an idle Hub. No goroutines are started.
func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]struct{}), tickets: newTicketStore()}
}

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	metrics.WSConnectedClients.Inc()
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	metrics.WSConnectedClients.Dec()
}

// logTaskID extracts the task_id of an agent.log payload so the hub can route
// it to subscribed clients only. Typed payloads take the fast path; anything
// else is inspected through its JSON form.
func logTaskID(payload any) string {
	switch p := payload.(type) {
	case wsevent.AgentLog:
		return p.TaskID
	case *wsevent.AgentLog:
		return p.TaskID
	}
	id, _ := wsevent.ToMap(payload)["task_id"].(string)
	return id
}

// Publish sends an event to connected clients.
//
// Events that carry a "task_id" in the payload AND have type "agent.log" are
// delivered only to clients subscribed to that task; all other events are
// broadcast to every client.
func (h *Hub) Publish(eventType string, payload any) {
	msg, err := json.Marshal(Event{Type: eventType, Payload: payload})
	if err != nil {
		slog.Error("ws marshal event", "err", err)
		return
	}

	var taskID string
	if eventType == wsevent.TypeAgentLog {
		taskID = logTaskID(payload)
	}
	perTask := taskID != ""

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if perTask {
			c.subMu.RLock()
			subscribed := c.subscriptions[taskID]
			c.subMu.RUnlock()
			if !subscribed {
				continue
			}
		}
		select {
		case c.send <- msg:
		default:
			metrics.WSBroadcastDroppedTotal.Inc()
			slog.Warn("ws client send buffer full, dropping event", "type", eventType)
			// The client has missed an event and cannot tell. Flag it so the
			// write pump sends a "resync" frame once the buffer drains.
			c.markLagged()
		}
	}
}
