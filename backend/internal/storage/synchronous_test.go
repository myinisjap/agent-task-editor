package storage

import (
	"context"
	"testing"
)

// TestSynchronousNormal verifies pooled connections use synchronous=NORMAL (1).
func TestSynchronousNormal(t *testing.T) {
	db, err := Open(t.TempDir() + "/sync.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	conn, err := db.SQL().Conn(context.Background())
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer func() { _ = conn.Close() }()

	var v int
	if err := conn.QueryRowContext(context.Background(), `PRAGMA synchronous`).Scan(&v); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if v != 1 {
		t.Fatalf("PRAGMA synchronous = %d, want 1 (NORMAL)", v)
	}
}
