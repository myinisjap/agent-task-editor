package ws

import (
	"encoding/json"
	"testing"
	"time"
)

// newTestClient creates a minimal Client wired to hub without a real WS conn.
func newTestClient(hub *Hub) *Client {
	return &Client{
		hub:           hub,
		send:          make(chan []byte, 64),
		subscriptions: make(map[string]bool),
	}
}

func TestHub_Publish_BroadcastToAll(t *testing.T) {
	hub := NewHub()

	c1 := newTestClient(hub)
	c2 := newTestClient(hub)
	hub.register(c1)
	hub.register(c2)

	hub.Publish("task.label_changed", map[string]any{"task_id": "t1", "label": "done"})

	for i, c := range []*Client{c1, c2} {
		select {
		case msg := <-c.send:
			var evt Event
			if err := json.Unmarshal(msg, &evt); err != nil {
				t.Fatalf("client %d: unmarshal: %v", i, err)
			}
			if evt.Type != "task.label_changed" {
				t.Errorf("client %d: expected task.label_changed, got %s", i, evt.Type)
			}
		default:
			t.Errorf("client %d: expected a message but got none", i)
		}
	}
}

func TestHub_Publish_AgentLog_OnlyToSubscribed(t *testing.T) {
	hub := NewHub()

	subscribed := newTestClient(hub)
	subscribed.subscriptions["task-42"] = true

	unsubscribed := newTestClient(hub)

	hub.register(subscribed)
	hub.register(unsubscribed)

	hub.Publish("agent.log", map[string]any{"task_id": "task-42", "content": "hello"})

	select {
	case <-subscribed.send:
		// expected
	default:
		t.Error("subscribed client should have received agent.log event")
	}

	select {
	case <-unsubscribed.send:
		t.Error("unsubscribed client should not have received agent.log event")
	default:
		// expected
	}
}

func TestHub_Publish_AgentLog_NoTaskID_Broadcasts(t *testing.T) {
	hub := NewHub()

	c := newTestClient(hub)
	hub.register(c)

	// agent.log without task_id behaves as broadcast
	hub.Publish("agent.log", map[string]any{"content": "no task id"})

	select {
	case <-c.send:
		// expected — no task_id means perTask=false, so broadcast
	default:
		t.Error("client should receive agent.log without task_id")
	}
}

func TestHub_Unregister_StopsDelivery(t *testing.T) {
	hub := NewHub()

	c := newTestClient(hub)
	hub.register(c)
	hub.unregister(c)

	hub.Publish("task.label_changed", map[string]any{})

	select {
	case <-c.send:
		t.Error("unregistered client should not receive messages")
	default:
		// expected
	}
}

func TestHub_Publish_FullBuffer_DoesNotBlock(t *testing.T) {
	hub := NewHub()

	// Buffer of 1 — will fill after the first publish
	c := &Client{
		hub:           hub,
		send:          make(chan []byte, 1),
		subscriptions: make(map[string]bool),
	}
	hub.register(c)

	hub.Publish("task.label_changed", map[string]any{}) // fills buffer

	// Second publish must drop the message and return, not block
	done := make(chan struct{})
	go func() {
		hub.Publish("task.label_changed", map[string]any{})
		close(done)
	}()

	select {
	case <-done:
		// expected — message dropped, no deadlock
	case <-time.After(500 * time.Millisecond):
		t.Error("Publish blocked on full client buffer")
	}
}

func TestHub_EventPayload_Roundtrips(t *testing.T) {
	hub := NewHub()

	c := newTestClient(hub)
	hub.register(c)

	payload := map[string]any{"task_id": "abc", "label": "done", "count": float64(3)}
	hub.Publish("test.event", payload)

	msg := <-c.send
	var evt Event
	if err := json.Unmarshal(msg, &evt); err != nil {
		t.Fatal(err)
	}
	if evt.Type != "test.event" {
		t.Errorf("expected type 'test.event', got %q", evt.Type)
	}
	if evt.Payload["task_id"] != "abc" {
		t.Errorf("expected task_id 'abc', got %v", evt.Payload["task_id"])
	}
}
