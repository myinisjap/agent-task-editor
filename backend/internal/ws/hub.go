package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// Event is the JSON envelope sent to every WebSocket client.
type Event struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// Hub fan-outs events to all connected clients.
// It satisfies both workflow.Publisher and agent.Publisher (same method set).
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
}

// NewHub creates an idle Hub. No goroutines are started.
func NewHub() *Hub {
	return &Hub{clients: make(map[*Client]struct{})}
}

func (h *Hub) register(c *Client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Publish sends an event to connected clients.
//
// Events that carry a "task_id" in the payload AND have type "agent.log" are
// delivered only to clients subscribed to that task; all other events are
// broadcast to every client.
func (h *Hub) Publish(eventType string, payload map[string]any) {
	msg, err := json.Marshal(Event{Type: eventType, Payload: payload})
	if err != nil {
		slog.Error("ws marshal event", "err", err)
		return
	}

	taskID, _ := payload["task_id"].(string)
	perTask := eventType == "agent.log" && taskID != ""

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
			slog.Warn("ws client send buffer full, dropping event", "type", eventType)
		}
	}
}
