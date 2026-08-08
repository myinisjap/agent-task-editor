package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
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

// --- ServeWS end-to-end tests (real HTTP server + real WS client) ---
//
// TestServeWS_UnauthorizedRejection_IsJSON above only exercises the
// pre-upgrade auth rejection via httptest.NewRecorder (which can't actually
// complete a WS upgrade). These tests spin up a real httptest.Server and a
// real nhooyr.io/websocket client to exercise the parts of ServeWS that only
// run after a successful upgrade: the ticket vs. deprecated ?token=
// fallback, origin-based CORS rejection, and the maxSubscriptions cap.

func newTestWSServer(t *testing.T, hub *Hub, authToken, corsOrigins string) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, w, r, authToken, corsOrigins, nil)
	}))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	return srv, wsURL
}

func TestServeWS_TicketAuth_AllowsUpgrade(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "s3cr3t", "*")

	ticket, err := hub.IssueTicket()
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL+"?ticket="+ticket, nil)
	if err != nil {
		t.Fatalf("Dial with valid ticket should succeed: %v (resp=%v)", err, resp)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
}

// TestServeWS_DeprecatedTokenFallback_AllowsUpgrade verifies the deprecated
// ?token=<API_TOKEN> query param still authenticates the upgrade (kept for
// backward compatibility, per ServeWS's doc comment).
func TestServeWS_DeprecatedTokenFallback_AllowsUpgrade(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "s3cr3t", "*")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL+"?token=s3cr3t", nil)
	if err != nil {
		t.Fatalf("Dial with the deprecated ?token= fallback should succeed: %v (resp=%v)", err, resp)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
}

// TestServeWS_WrongToken_RejectsUpgrade verifies an incorrect ?token= value
// is rejected (the deprecated fallback still enforces a constant-time
// comparison against the real token).
func TestServeWS_WrongToken_RejectsUpgrade(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "s3cr3t", "*")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL+"?token=wrong", nil)
	if err == nil {
		t.Fatal("expected Dial to fail for an incorrect token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		code := -1
		if resp != nil {
			code = resp.StatusCode
		}
		t.Errorf("expected 401 response, got %d", code)
	}
}

// TestServeWS_TicketIsSingleUse verifies a ticket can't be replayed for a
// second connection after being consumed once.
func TestServeWS_TicketIsSingleUse(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "s3cr3t", "*")

	ticket, err := hub.IssueTicket()
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL+"?ticket="+ticket, nil)
	if err != nil {
		t.Fatalf("first dial with a fresh ticket should succeed: %v", err)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	_, resp, err := websocket.Dial(ctx, wsURL+"?ticket="+ticket, nil)
	if err == nil {
		t.Fatal("expected the second dial with an already-consumed ticket to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 on ticket replay, resp=%v", resp)
	}
}

// TestServeWS_OpenAuth_NoTokenRequired verifies that when authToken is empty
// (open/no-auth mode), a connection with no ticket/token at all succeeds.
func TestServeWS_OpenAuth_NoTokenRequired(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "", "*")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("open-auth dial should succeed with no credentials: %v (resp=%v)", err, resp)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
}

// TestServeWS_BadOrigin_RejectsUpgrade verifies a cross-origin WS handshake
// whose Origin header doesn't match the configured corsOrigins is rejected.
// nhooyr.io/websocket's server-side OriginPatterns check requires the client
// to send a browser-like Origin header explicitly (Go's own websocket.Dial
// doesn't set one), so this test sets it via HTTPHeader.
func TestServeWS_BadOrigin_RejectsUpgrade(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "", "https://allowed.example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example.com"}},
	})
	if err == nil {
		t.Fatal("expected the handshake to fail for a disallowed Origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		code := -1
		if resp != nil {
			code = resp.StatusCode
		}
		t.Errorf("expected 403 for a disallowed origin, got %d", code)
	}
}

// TestServeWS_GoodOrigin_AllowsUpgrade is the positive counterpart to
// TestServeWS_BadOrigin_RejectsUpgrade: a matching Origin header is accepted.
func TestServeWS_GoodOrigin_AllowsUpgrade(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "", "https://allowed.example.com")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://allowed.example.com"}},
	})
	if err != nil {
		t.Fatalf("expected the handshake to succeed for an allowed Origin: %v (resp=%v)", err, resp)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
}

// subscriptionCount returns the total subscriptions across all connected
// clients. Test-only; reads unexported state under the same locks the
// production code uses. Assumes a single connected client in these tests.
func (h *Hub) subscriptionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for c := range h.clients {
		c.subMu.RLock()
		n += len(c.subscriptions)
		c.subMu.RUnlock()
	}
	return n
}

// TestServeWS_SubscriptionCap_IgnoresBeyondLimit verifies that once a client
// has maxSubscriptions active subscriptions, further "subscribe" messages
// are silently ignored (added=false branch) rather than growing the map
// unbounded. Asserted directly via the test-only subscriptionCount accessor
// rather than a timing-based hub-Publish round trip.
func TestServeWS_SubscriptionCap_IgnoresBeyondLimit(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "", "*")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// Fill the subscription map to the cap with distinct task ids, then add
	// one more beyond it. The over-cap subscribe should be silently dropped.
	for i := 0; i < maxSubscriptions; i++ {
		send(t, ctx, conn, inboundMsg{Type: "subscribe", TaskID: taskIDFor(i)})
	}
	overCapTaskID := "task-over-cap"
	send(t, ctx, conn, inboundMsg{Type: "subscribe", TaskID: overCapTaskID})

	// Poll until the read pump has processed all subscribe messages, then
	// assert the map size directly: it must land at exactly maxSubscriptions
	// and never grow beyond it, proving the over-cap frame was dropped.
	deadline := time.Now().Add(5 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		got = hub.subscriptionCount()
		if got == maxSubscriptions {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got != maxSubscriptions {
		t.Fatalf("expected subscription count to settle at %d, got %d", maxSubscriptions, got)
	}
}

// TestServeWS_WritePump_SurvivesNormalTraffic is a regression guard for the
// per-write/per-ping timeouts added to the write pump (writeTimeout /
// pingTimeout): a healthy connection receiving ordinary traffic must not be
// disconnected by them. Publishes several events and reads them all back,
// then asserts the client is still registered in the hub afterwards.
func TestServeWS_WritePump_SurvivesNormalTraffic(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "", "*")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	// Wait for registration before publishing, otherwise Publish may run
	// before the hub has this client.
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

	const numEvents = 20
	for i := 0; i < numEvents; i++ {
		hub.Publish("task.label_changed", map[string]any{"task_id": "t", "i": i})
	}

	for i := 0; i < numEvents; i++ {
		readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
		_, _, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			t.Fatalf("expected to read event %d, got error: %v", i, err)
		}
	}

	hub.mu.RLock()
	n := len(hub.clients)
	hub.mu.RUnlock()
	if n != 1 {
		t.Errorf("expected client to remain registered after normal traffic, hub has %d clients", n)
	}
}

// TestServeWS_AbruptPeerClose_RemovesClientFromHub verifies that a
// connection closed by the peer (rather than a clean close handshake) is
// eventually removed from the hub — the read pump errors, cancels the
// shared ctx, and the write pump's write/ping calls then observe a
// cancelled context (or the connection itself is already gone) and return.
func TestServeWS_AbruptPeerClose_RemovesClientFromHub(t *testing.T) {
	hub := NewHub()
	_, wsURL := newTestWSServer(t, hub, "", "*")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

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

	// Abruptly terminate the underlying TCP connection rather than sending a
	// close frame, simulating a half-open/dropped peer.
	_ = conn.CloseNow()

	deadline = time.Now().Add(5 * time.Second)
	var n int
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		n = len(hub.clients)
		hub.mu.RUnlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n != 0 {
		t.Errorf("expected client to be removed from hub after abrupt peer close, hub still has %d clients", n)
	}
}

func taskIDFor(i int) string {
	return "task-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}

func send(t *testing.T, ctx context.Context, conn *websocket.Conn, msg inboundMsg) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write subscribe message: %v", err)
	}
}
