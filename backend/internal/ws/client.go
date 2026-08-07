// Package ws provides the WebSocket hub and per-client connection management.
package ws

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const maxSubscriptions = 100

// writeTimeout bounds a single frame write on the write pump; pingTimeout
// bounds a ping frame's write-and-wait-for-pong. Both exist because a
// hijacked connection has no socket deadlines and r.Context() is never
// cancelled by a peer disconnect (laptop sleep, NAT timeout, proxy drop) — so
// without them a half-open peer would park the write pump in Write/Ping
// forever, the read pump would never error, hub.unregister would never run,
// and the client would stay in Hub.clients (and the WSConnectedClients
// gauge) for the process lifetime.
const writeTimeout = 10 * time.Second
const pingTimeout = 10 * time.Second

// maxConcurrentReplays bounds how many replayTaskLogs goroutines a single
// client may have in flight at once. Each replay does a GetTask +
// ListAgentLogsPage(limit replayLimit+1) and marshals up to replayLimit
// entries, so a client subscribing to many tasks in quick succession could
// otherwise fan out unbounded concurrent DB reads and large payloads. Kept
// small since a client rarely needs more than a handful of replays running
// at once; new subscriptions simply wait for a slot rather than being
// dropped, so every subscription still gets its replay.
const maxConcurrentReplays = 4

// replayLimit caps how many historical log entries are replayed to a client on
// subscribe. A single batched agent.log_replay message carries the newest
// replayLimit entries; older entries are fetched on demand via the REST logs
// endpoint ("load earlier"). This bounds the work done — and the send-buffer
// pressure — when subscribing to a task with a very long run.
const replayLimit = 500

// Client represents a single WebSocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte

	subMu         sync.RWMutex
	subscriptions map[string]bool

	// replaySlots bounds concurrent replayTaskLogs goroutines for this
	// client; see maxConcurrentReplays.
	replaySlots chan struct{}
}

// inboundMsg is sent by the browser to subscribe/unsubscribe a task.
type inboundMsg struct {
	Type   string `json:"type"`
	TaskID string `json:"task_id"`
}

// ServeWS upgrades the HTTP connection and starts the client goroutines.
// authToken is the expected bearer token (empty = no auth required).
// corsOrigins is the CORS allowed origins list (comma-separated, "*" = any).
// q is used to replay historical log entries when a client subscribes to a task.
//
// Auth: browsers can't set the Authorization header on a WS handshake, so
// auth travels via the query string instead. The primary mechanism is a
// short-lived, single-use ticket minted by POST /api/v1/ws-ticket (itself
// bearer-authed) and passed as ?ticket=; ServeWS validates and consumes it
// via hub.ConsumeTicket. The long-lived ?token=<API_TOKEN> query param is
// still accepted as a deprecated fallback for existing setups, but it
// leaves a durable credential in logs/history, so ?ticket= should be
// preferred — a warning is logged whenever the fallback is used.
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request, authToken, corsOrigins string, q *gen.Queries) {
	if authToken != "" {
		authorized := false
		if ticket := r.URL.Query().Get("ticket"); ticket != "" {
			authorized = hub.ConsumeTicket(ticket)
		}
		if !authorized {
			// Deprecated fallback: ?token= — kept for existing setups, logged so
			// operators can see usage and plan migration to ws-ticket.
			tok := r.URL.Query().Get("token")
			if subtle.ConstantTimeCompare([]byte(tok), []byte(authToken)) == 1 {
				authorized = true
				slog.Warn("ws auth via deprecated ?token= query param; migrate to POST /api/v1/ws-ticket", "remote", r.RemoteAddr)
			}
		}
		if !authorized {
			// This runs before websocket.Accept, so it's a normal HTTP
			// response — match handlers.Err's {"error": "..."} JSON shape
			// rather than plain text.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
	}

	// Build origin pattern list from the same CORS config used by the
	// middleware. CORS_ORIGINS entries are full origins (scheme + host,
	// e.g. "http://localhost:5173"), matching the exact-string comparison
	// against the Origin header done by middleware.CORS. nhooyr's
	// OriginPatterns, however, matches only against the request Origin's
	// host (no scheme) via filepath.Match — so patterns must be reduced to
	// just the host here, or every legitimately configured origin would be
	// silently rejected.
	var originPatterns []string
	if corsOrigins == "*" || corsOrigins == "" {
		originPatterns = []string{"*"}
	} else {
		for _, o := range strings.Split(corsOrigins, ",") {
			s := strings.TrimSpace(o)
			if s == "" {
				continue
			}
			if u, err := url.Parse(s); err == nil && u.Host != "" {
				s = u.Host
			}
			originPatterns = append(originPatterns, s)
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: originPatterns,
	})
	if err != nil {
		slog.Error("ws upgrade", "err", err)
		return
	}

	c := &Client{
		hub:           hub,
		conn:          conn,
		send:          make(chan []byte, 256),
		subscriptions: make(map[string]bool),
		replaySlots:   make(chan struct{}, maxConcurrentReplays),
	}
	hub.register(c)
	defer hub.unregister(c)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// Read pump: handle subscribe/unsubscribe from client
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var msg inboundMsg
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "subscribe":
				if msg.TaskID != "" {
					c.subMu.Lock()
					added := false
					if !c.subscriptions[msg.TaskID] && len(c.subscriptions) < maxSubscriptions {
						c.subscriptions[msg.TaskID] = true
						added = true
					}
					c.subMu.Unlock()
					if added && q != nil {
						// Bound concurrent replays per client (see
						// maxConcurrentReplays); block for a slot rather than
						// drop so every newly-added subscription still gets
						// its replay. Capture taskID by value: msg is reused
						// across loop iterations.
						taskID := msg.TaskID
						go func() {
							select {
							case c.replaySlots <- struct{}{}:
							case <-ctx.Done():
								return
							}
							defer func() { <-c.replaySlots }()
							replayTaskLogs(ctx, c, q, taskID)
						}()
					}
				}
			case "unsubscribe":
				if msg.TaskID != "" {
					c.subMu.Lock()
					delete(c.subscriptions, msg.TaskID)
					c.subMu.Unlock()
				}
			}
		}
	}()

	// Write pump: send queued events to client
	go func() {
		defer wg.Done()
		defer cancel()
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-c.send:
				if !ok {
					return
				}
				wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
				err := conn.Write(wctx, websocket.MessageText, msg)
				wcancel()
				if err != nil {
					return
				}
			case <-ticker.C:
				// Keepalive ping. pingTimeout bounds both the ping frame's
				// write and the wait for the pong, so a half-open peer that
				// never responds gets its connection closed instead of
				// parking this goroutine forever.
				pctx, pcancel := context.WithTimeout(ctx, pingTimeout)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					return
				}
			}
		}
	}()

	wg.Wait()
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// replayTaskLogs fetches the tail of the task's current run's log and sends it
// to the client as a single batched agent.log_replay message, so a reconnecting
// browser sees prior output without the server queueing tens of thousands of
// individual messages through the send buffer. Only the newest replayLimit
// entries are sent; older ones are loaded on demand via the REST logs endpoint.
// The has_more flag tells the client whether earlier entries exist.
func replayTaskLogs(ctx context.Context, c *Client, q *gen.Queries, taskID string) {
	task, err := q.GetTask(ctx, taskID)
	if err != nil || task.CurrentAgentRunID == nil {
		return
	}
	runID := *task.CurrentAgentRunID

	// Fetch one extra (newest-first) to detect whether earlier entries exist.
	logs, err := q.ListAgentLogsPage(ctx, gen.ListAgentLogsPageParams{
		AgentRunID: runID,
		Column2:    "",
		Limit:      int64(replayLimit) + 1,
	})
	if err != nil || len(logs) == 0 {
		return
	}
	hasMore := false
	if len(logs) > replayLimit {
		logs = logs[:replayLimit]
		hasMore = true
	}

	// logs are newest-first; reverse to chronological order for the client.
	entries := make([]map[string]any, len(logs))
	for i := range logs {
		log := logs[len(logs)-1-i]
		entries[i] = map[string]any{
			"id":      log.ID,
			"type":    log.Type,
			"content": log.Content,
			"at":      log.Timestamp,
		}
	}

	msg, err := json.Marshal(Event{
		Type: "agent.log_replay",
		Payload: map[string]any{
			"run_id":   runID,
			"task_id":  taskID,
			"has_more": hasMore,
			"entries":  entries,
		},
	})
	if err != nil {
		return
	}
	select {
	case c.send <- msg:
	case <-ctx.Done():
	}
}
