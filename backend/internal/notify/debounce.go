package notify

import (
	"sync"
	"time"
)

// defaultDebounceWindow is the fallback debounce window used when the
// configured value is zero or negative (see config.NotifyDebounce).
const defaultDebounceWindow = 5 * time.Minute

// debouncer suppresses a duplicate notification key seen again within
// window of the first sighting. Mirrors internal/ws's ticketStore pattern:
// a plain map guarded by a mutex, swept opportunistically on insert rather
// than via a background goroutine. Evaluated on the Notifier's Run
// goroutine after classification, so a suppressed event costs no HTTP call.
type debouncer struct {
	mu     sync.Mutex
	seenAt map[string]time.Time
	window time.Duration
}

// newDebouncer creates a debouncer with the given window. A window <= 0
// falls back to defaultDebounceWindow.
func newDebouncer(window time.Duration) *debouncer {
	if window <= 0 {
		window = defaultDebounceWindow
	}
	return &debouncer{seenAt: make(map[string]time.Time), window: window}
}

// seen reports whether key was already recorded within window of now, and
// records/refreshes key -> now either way. The first call for a given key
// (or a call after the window has elapsed) returns false ("not a dup, go
// ahead and notify"); a call within the window returns true ("duplicate,
// suppress").
func (d *debouncer) seen(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Opportunistic sweep of expired entries so the map doesn't grow
	// unbounded across the process lifetime.
	for k, at := range d.seenAt {
		if now.Sub(at) > d.window {
			delete(d.seenAt, k)
		}
	}

	if at, ok := d.seenAt[key]; ok && now.Sub(at) <= d.window {
		return true
	}
	d.seenAt[key] = now
	return false
}
