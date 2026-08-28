// Package notify implements an optional outbound webhook subscriber: when a
// task needs a human (task.needs_human, arrival at a human-gate label,
// task.cost_warning, or system.cost_budget_tripped), it POSTs a JSON payload
// to an operator-configured URL. Disabled by default (NOTIFY_WEBHOOK_URL
// empty); env-only for this pass -- no per-workflow/per-repo settings, no
// Slack-specific formatting, no per-user routing (see issue #256).
//
// Notifier satisfies the same single-method Publisher interface every WS
// event producer already depends on (workflow.Publisher, agent.Publisher,
// ghsync.Publisher, tasksource.Publisher, schedule.Publisher), so it is
// wired in as a second "subscriber" alongside the WS hub via MultiPublisher
// (see multi.go) rather than the Hub gaining a subscriber mechanism of its
// own.
//
// Design constraints (see Publish's doc comment for why): Publish must never
// block or do I/O on the caller's goroutine -- every event producer above
// calls Publish synchronously from a serial hot path (the dispatch sweep
// loop, the workflow engine's transition commit). All HTTP/DB work happens
// on the Run goroutine.
package notify

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/metrics"
	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

// queueCap bounds the number of pending events buffered between Publish and
// Run. Sized generously relative to how bursty these events are (a handful
// per sweep, not a firehose); once full, Publish drops the event rather than
// blocking the caller -- see Publish's doc comment.
const queueCap = 256

// rawEvent is what Publish enqueues; classification and DB lookups happen
// later, on the Run goroutine.
type rawEvent struct {
	eventType string
	payload   map[string]any
}

// Notifier POSTs a JSON payload to a configured webhook URL whenever a
// trigger event (see events.go) is published. A zero-value Notifier (or one
// created with an empty url via New) is safe to use: Publish becomes a
// no-op and Run returns immediately.
type Notifier struct {
	// url is the target webhook endpoint. Never logged in full -- see
	// deliver.go's doc comment; only scheme+host may be logged.
	url string
	// baseURL, if set, is used to build a deep link to the task detail page
	// (NOTIFY_BASE_URL). Omitted from the payload when unset.
	baseURL string
	client  *http.Client
	q       *gen.Queries

	queue chan rawEvent

	debounce *debouncer

	now func() time.Time // injectable for tests
}

// New creates a Notifier that posts to rawURL. If rawURL is empty, or is not
// a valid http(s) URL, the returned Notifier is a no-op (Publish drops
// everything, Run returns immediately) and a warning is logged for the
// invalid (non-empty) case -- a misconfigured webhook URL disables
// notifications rather than failing to boot.
//
// debounceWindow is the minimum time between two notifications sharing the
// same dedupe key (see debounce.go); baseURL is used to build deep links
// (see events.go).
func New(rawURL, baseURL string, debounceWindow time.Duration, q *gen.Queries) *Notifier {
	n := &Notifier{
		baseURL:  baseURL,
		client:   &http.Client{Timeout: 10 * time.Second},
		q:        q,
		queue:    make(chan rawEvent, queueCap),
		debounce: newDebouncer(debounceWindow),
		now:      time.Now,
	}
	if rawURL == "" {
		return n
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		// NOTIFY_WEBHOOK_URL is operator-set via env only (no API), so this is
		// not a new SSRF exposure today -- but it would become one the moment
		// it's made settable via the API. Reject non-http(s) schemes here
		// rather than failing to boot.
		slog.Warn("invalid NOTIFY_WEBHOOK_URL; outbound notifications disabled")
		return n
	}
	n.url = rawURL
	slog.Info("outbound notifications enabled", "host", u.Host)
	return n
}

// enabled reports whether this Notifier has a valid target URL.
func (n *Notifier) enabled() bool {
	return n != nil && n.url != ""
}

// Publish enqueues eventType/payload for asynchronous classification and
// delivery on the Run goroutine. It never blocks: if the internal queue is
// full (Run isn't keeping up, or isn't running), the event is dropped and
// counted via metrics.NotifyDroppedTotal. Safe to call on a nil *Notifier.
//
// This is called synchronously from hot paths that dispatch/transition tasks
// serially (see package doc). A blocking Publish here would stall the whole
// board behind a hung webhook endpoint, so buffering + drop-on-full is
// non-negotiable.
func (n *Notifier) Publish(eventType string, payload map[string]any) {
	if !n.enabled() {
		return
	}
	select {
	case n.queue <- rawEvent{eventType: eventType, payload: payload}:
	default:
		metrics.NotifyDroppedTotal.Inc()
		slog.Warn("notify: queue full, dropping event", "type", eventType)
	}
}

// Run drains the event queue until ctx is cancelled, classifying and
// delivering each event in turn. Safe to call on a nil/disabled *Notifier
// (returns immediately).
func (n *Notifier) Run(ctx context.Context) {
	if !n.enabled() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-n.queue:
			n.handle(ctx, ev)
		}
	}
}

// handle classifies a raw event, applies debounce, and delivers it if
// warranted. Errors are logged, never fatal -- a failed delivery must never
// take down the notifier goroutine.
func (n *Notifier) handle(ctx context.Context, ev rawEvent) {
	note, ok := n.classify(ctx, ev)
	if !ok {
		return
	}
	key := note.debounceKey()
	if n.debounce.seen(key, n.now()) {
		metrics.NotifySuppressedTotal.Inc()
		return
	}
	if err := n.deliver(ctx, note); err != nil {
		metrics.NotifyFailedTotal.Inc()
		slog.Warn("notify: delivery failed", "reason", note.Reason, "err", err)
		return
	}
	metrics.NotifyDeliveredTotal.Inc()
}
