package ws

import (
	"testing"
	"time"
)

func TestTicketStore_IssueReturnsUniqueNonEmptyTickets(t *testing.T) {
	s := newTicketStore()

	t1, err := s.issue()
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if t1 == "" {
		t.Fatal("expected non-empty ticket")
	}

	t2, err := s.issue()
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if t2 == "" {
		t.Fatal("expected non-empty ticket")
	}
	if t1 == t2 {
		t.Fatal("expected unique tickets")
	}
}

func TestTicketStore_ConsumeIsSingleUse(t *testing.T) {
	s := newTicketStore()

	ticket, err := s.issue()
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if !s.consume(ticket) {
		t.Fatal("expected first consume to succeed")
	}
	if s.consume(ticket) {
		t.Fatal("expected second consume of same ticket to fail")
	}
}

func TestTicketStore_ConsumeUnknownTicketFails(t *testing.T) {
	s := newTicketStore()

	if s.consume("does-not-exist") {
		t.Fatal("expected consume of unknown ticket to fail")
	}
}

func TestTicketStore_ConsumeExpiredTicketFails(t *testing.T) {
	s := newTicketStoreWithTTL(10 * time.Millisecond)

	ticket, err := s.issue()
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	if s.consume(ticket) {
		t.Fatal("expected consume of expired ticket to fail")
	}
	// Also confirm it's gone (single-use even though expired).
	if s.consume(ticket) {
		t.Fatal("expected second consume of expired ticket to fail")
	}
}

func TestTicketStore_IssueSweepsExpiredEntries(t *testing.T) {
	s := newTicketStoreWithTTL(10 * time.Millisecond)

	old, err := s.issue()
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	if _, err := s.issue(); err != nil {
		t.Fatalf("issue: %v", err)
	}

	s.mu.Lock()
	_, stillPresent := s.tickets[old]
	s.mu.Unlock()
	if stillPresent {
		t.Fatal("expected expired ticket to be swept on subsequent issue()")
	}
}

func TestHub_IssueAndConsumeTicket(t *testing.T) {
	hub := NewHub()

	ticket, err := hub.IssueTicket()
	if err != nil {
		t.Fatalf("IssueTicket: %v", err)
	}
	if ticket == "" {
		t.Fatal("expected non-empty ticket")
	}
	if !hub.ConsumeTicket(ticket) {
		t.Fatal("expected ConsumeTicket to succeed")
	}
	if hub.ConsumeTicket(ticket) {
		t.Fatal("expected second ConsumeTicket to fail (single-use)")
	}
}
