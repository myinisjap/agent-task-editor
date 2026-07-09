package ws

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// ticketTTL is how long an issued WS auth ticket remains valid if unused.
const ticketTTL = 30 * time.Second

// ticketStore tracks short-lived, single-use tickets that authenticate a
// WebSocket upgrade without putting the long-lived API token in the URL
// (query strings leak into reverse-proxy/access logs and browser history).
type ticketStore struct {
	mu      sync.Mutex
	tickets map[string]time.Time // ticket -> expiry
	ttl     time.Duration
}

// newTicketStore creates an empty ticket store using the default TTL.
func newTicketStore() *ticketStore {
	return newTicketStoreWithTTL(ticketTTL)
}

// newTicketStoreWithTTL is like newTicketStore but allows overriding the TTL,
// used by tests to exercise expiry without sleeping 30s.
func newTicketStoreWithTTL(ttl time.Duration) *ticketStore {
	return &ticketStore{tickets: make(map[string]time.Time), ttl: ttl}
}

// issue mints a new random, single-use ticket valid for ttl. It also
// opportunistically sweeps expired entries so the map doesn't grow unbounded
// if tickets are minted but never consumed.
func (s *ticketStore) issue() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate ticket: %w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for t, expiry := range s.tickets {
		if now.After(expiry) {
			delete(s.tickets, t)
		}
	}
	s.tickets[ticket] = now.Add(s.ttl)
	return ticket, nil
}

// consume looks up ticket and reports whether it was present and not yet
// expired. The ticket is deleted regardless of the outcome, so it is always
// single-use — replaying an expired or already-used ticket always fails.
func (s *ticketStore) consume(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	expiry, ok := s.tickets[ticket]
	delete(s.tickets, ticket)
	if !ok {
		return false
	}
	return !time.Now().After(expiry)
}

// IssueTicket mints a new short-lived, single-use ticket for authenticating
// a WebSocket upgrade. See ticketStore for details.
func (h *Hub) IssueTicket() (string, error) {
	return h.tickets.issue()
}

// ConsumeTicket validates and consumes a ticket previously returned by
// IssueTicket. It returns true iff the ticket existed and had not expired.
func (h *Hub) ConsumeTicket(ticket string) bool {
	return h.tickets.consume(ticket)
}
