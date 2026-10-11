package ws

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func newSmallBufferClient(hub *Hub, size int) *Client {
	return &Client{
		hub:           hub,
		send:          make(chan []byte, size),
		wake:          make(chan struct{}, 1),
		subscriptions: make(map[string]bool),
	}
}

// A full buffer drops the event, flags the client, and only after the buffer
// drains does takeResync hand out exactly one resync frame and clear the flag.
func TestClient_DroppedEventMarksLaggedAndResyncsAfterDrain(t *testing.T) {
	hub := NewHub()
	c := newSmallBufferClient(hub, 2)
	hub.register(c)

	before := testutil.ToFloat64(metrics.WSBroadcastDroppedTotal)
	for i := 0; i < 5; i++ {
		hub.Publish("task.label_changed", map[string]any{"task_id": "t", "i": i})
	}
	if !c.lagged.Load() {
		t.Fatal("expected client to be flagged lagged after drops")
	}
	if got := testutil.ToFloat64(metrics.WSBroadcastDroppedTotal) - before; got != 3 {
		t.Errorf("expected 3 dropped events counted, got %v", got)
	}

	// Buffer still holds events: resync must wait so it lands after them.
	if _, ok := c.takeResync(); ok {
		t.Fatal("resync must not be delivered while buffer is non-empty")
	}
	if !c.lagged.Load() {
		t.Fatal("flag must stay set until the resync is taken")
	}

	<-c.send
	<-c.send

	frame, ok := c.takeResync()
	if !ok {
		t.Fatal("expected resync once buffer drained")
	}
	var evt Event
	if err := json.Unmarshal(frame, &evt); err != nil || evt.Type != "resync" {
		t.Fatalf("bad resync frame %q (err %v)", frame, err)
	}
	if c.lagged.Load() {
		t.Error("lagged flag should be cleared after resync taken")
	}
	if _, ok := c.takeResync(); ok {
		t.Error("resync must be sent only once per lag episode")
	}
}

func TestClient_NotLaggedNoResync(t *testing.T) {
	hub := NewHub()
	c := newSmallBufferClient(hub, 4)
	hub.register(c)
	hub.Publish("x", map[string]any{})
	<-c.send
	if _, ok := c.takeResync(); ok {
		t.Error("no resync expected for a client that never lagged")
	}
}

// End to end: a client that does not read lets its buffer fill; once it starts
// reading it receives a resync frame after the queued events.
func TestServeWS_SlowClientReceivesResync(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "", "*")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	conn.SetReadLimit(-1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		n := len(hub.clients)
		hub.mu.RUnlock()
		if n == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	var c *Client
	hub.mu.RLock()
	for cl := range hub.clients {
		c = cl
	}
	hub.mu.RUnlock()
	if c == nil {
		t.Fatal("client not registered")
	}

	// Publish large events without reading until the buffer overflows.
	big := strings.Repeat("x", 256*1024)
	for i := 0; !c.lagged.Load(); i++ {
		if i > 5000 {
			t.Fatal("never overflowed the client's send buffer")
		}
		hub.Publish("task.updated", map[string]any{"id": "t", "pad": big})
	}

	// Now read: the resync frame must arrive after the queued events.
	for {
		rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
		_, data, err := conn.Read(rctx)
		rcancel()
		if err != nil {
			t.Fatalf("never received resync frame: %v", err)
		}
		var evt Event
		if json.Unmarshal(data, &evt) == nil && evt.Type == "resync" {
			break
		}
	}
	deadline = time.Now().Add(2 * time.Second)
	for c.lagged.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.lagged.Load() {
		t.Error("lagged flag should be cleared after resync delivered")
	}
}
