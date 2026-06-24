// Package ws provides the WebSocket hub and per-client connection management.
package ws

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const maxSubscriptions = 100

// Client represents a single WebSocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte

	subMu         sync.RWMutex
	subscriptions map[string]bool
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
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request, authToken, corsOrigins string, q *gen.Queries) {
	// Validate token from query param (browsers can't set Authorization on WS).
	if authToken != "" {
		tok := r.URL.Query().Get("token")
		if subtle.ConstantTimeCompare([]byte(tok), []byte(authToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Build origin pattern list from the same CORS config used by the middleware.
	var originPatterns []string
	if corsOrigins == "*" || corsOrigins == "" {
		originPatterns = []string{"*"}
	} else {
		for _, o := range strings.Split(corsOrigins, ",") {
			if s := strings.TrimSpace(o); s != "" {
				originPatterns = append(originPatterns, s)
			}
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
					if len(c.subscriptions) < maxSubscriptions {
						c.subscriptions[msg.TaskID] = true
						added = true
					}
					c.subMu.Unlock()
					if added && q != nil {
						go replayTaskLogs(ctx, c, q, msg.TaskID)
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
				if err := conn.Write(ctx, websocket.MessageText, msg); err != nil {
					return
				}
			case <-ticker.C:
				// Keepalive ping
				if err := conn.Ping(ctx); err != nil {
					return
				}
			}
		}
	}()

	wg.Wait()
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// replayTaskLogs fetches historical log entries for the task's current run
// and sends them to the client so a reconnecting browser sees prior output.
func replayTaskLogs(ctx context.Context, c *Client, q *gen.Queries, taskID string) {
	task, err := q.GetTask(ctx, taskID)
	if err != nil || task.CurrentAgentRunID == nil {
		return
	}
	runID := *task.CurrentAgentRunID

	logs, err := q.ListAgentLogs(ctx, runID)
	if err != nil || len(logs) == 0 {
		return
	}

	for _, log := range logs {
		msg, err := json.Marshal(Event{
			Type: "agent.log",
			Payload: map[string]any{
				"run_id":  runID,
				"task_id": taskID,
				"entry": map[string]any{
					"type":    log.Type,
					"content": log.Content,
					"at":      log.Timestamp,
				},
			},
		})
		if err != nil {
			continue
		}
		select {
		case c.send <- msg:
		case <-ctx.Done():
			return
		}
	}
}
