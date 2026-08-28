package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// retryBackoffs is the wait before each retry attempt (index 0 = wait
// before the 2nd attempt, index 1 = wait before the 3rd). Three attempts
// total. Package-level var (not a const) so tests can shrink it to keep the
// retry tests fast.
var retryBackoffs = []time.Duration{1 * time.Second, 4 * time.Second}

// deliver POSTs note as JSON to n.url, retrying on network error or a 5xx /
// 429 response (up to 3 attempts total, with backoff between attempts).
// Any other 4xx status is not retried -- it indicates a malformed target,
// and retrying would just spam it. ctx cancellation is respected between
// attempts so shutdown is never delayed.
//
// On final failure the error/log message includes only the target's
// scheme+host, never the full URL -- webhook URLs are secrets (e.g. Slack
// incoming-webhook tokens live in the path).
func (n *Notifier) deliver(ctx context.Context, note notification) error {
	body, err := json.Marshal(note)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	host := hostOnly(n.url)

	var lastErr error
	attempts := len(retryBackoffs) + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryBackoffs[attempt-1]):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request to %s: %w", host, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "agent-task-editor")

		resp, err := n.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("post to %s: %w", host, err)
			slog.Warn("notify: delivery attempt failed, will retry", "host", host, "attempt", attempt+1, "err", err)
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("post to %s: status %d", host, resp.StatusCode)
			slog.Warn("notify: delivery attempt failed, will retry", "host", host, "attempt", attempt+1, "status", resp.StatusCode)
			continue
		}
		// Any other 4xx: don't retry, the target is malformed.
		return fmt.Errorf("post to %s: status %d (not retrying)", host, resp.StatusCode)
	}
	return lastErr
}

// hostOnly returns scheme://host for rawURL, or "invalid-url" if it can't
// be parsed -- used so error messages/log lines never leak the full webhook
// URL (which may embed a secret token in its path or query).
func hostOnly(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "invalid-url"
	}
	return u.Scheme + "://" + u.Host
}
