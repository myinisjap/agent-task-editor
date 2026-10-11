package remotemcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// A grant is one completed sign-in (authorization-code exchange). Its id
// (the "jti" claim) rides in every access and refresh token minted for it, so
// revoking the row kills that session's tokens immediately without touching
// other sessions or rotating MCP_SIGNING_KEY.
//
// The table is created here with IF NOT EXISTS rather than via a numbered
// migration: it belongs to an optional feature, and it avoids colliding with
// migration numbers on main.
const grantsDDL = `CREATE TABLE IF NOT EXISTS mcp_grants (
	id           TEXT PRIMARY KEY,
	email        TEXT NOT NULL,
	client_id    TEXT NOT NULL,
	created_at   TEXT NOT NULL,
	last_used_at TEXT,
	revoked_at   TEXT
)`

func ensureGrantsTable(db *sql.DB) error {
	if _, err := db.Exec(grantsDDL); err != nil {
		return fmt.Errorf("create mcp_grants: %w", err)
	}
	return nil
}

// Grant describes one sign-in, for listing.
type Grant struct {
	ID         string
	Email      string
	ClientID   string
	CreatedAt  string
	LastUsedAt string
	Revoked    bool
}

func (s *Server) createGrant(ctx context.Context, email, clientID string) (string, error) {
	id := randomString()[:22]
	_, err := s.cfg.DB.ExecContext(ctx, `INSERT INTO mcp_grants (id, email, client_id, created_at) VALUES (?, ?, ?, ?)`,
		id, email, clientID, s.now().UTC().Format(time.RFC3339))
	return id, err
}

// grantActive reports whether the grant exists and is not revoked. touch
// records the use (done on refresh, not on every tool call).
func (s *Server) grantActive(ctx context.Context, id string, touch bool) bool {
	if id == "" {
		return false
	}
	var revoked sql.NullString
	err := s.cfg.DB.QueryRowContext(ctx, `SELECT revoked_at FROM mcp_grants WHERE id = ?`, id).Scan(&revoked)
	if err != nil || revoked.Valid {
		return false
	}
	if touch {
		_, _ = s.cfg.DB.ExecContext(ctx, `UPDATE mcp_grants SET last_used_at = ? WHERE id = ?`, s.now().UTC().Format(time.RFC3339), id)
	}
	return true
}

// ListGrants returns every grant, newest first.
func ListGrants(ctx context.Context, db *sql.DB) ([]Grant, error) {
	if err := ensureGrantsTable(db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, email, client_id, created_at, COALESCE(last_used_at, ''), revoked_at IS NOT NULL FROM mcp_grants ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Grant
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.Email, &g.ClientID, &g.CreatedAt, &g.LastUsedAt, &g.Revoked); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RevokeGrant revokes one grant by id and reports whether it was active.
func RevokeGrant(ctx context.Context, db *sql.DB, id string) (bool, error) {
	if err := ensureGrantsTable(db); err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `UPDATE mcp_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RevokeEmail revokes every active grant of one account and returns how many.
func RevokeEmail(ctx context.Context, db *sql.DB, email string) (int64, error) {
	if email == "" {
		return 0, errors.New("email required")
	}
	if err := ensureGrantsTable(db); err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, `UPDATE mcp_grants SET revoked_at = ? WHERE lower(email) = lower(?) AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339), email)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
