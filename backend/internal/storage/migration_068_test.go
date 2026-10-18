package storage

import (
	"strings"
	"testing"
)

// TestAgentLogsQueriesUseCompositeIndex verifies migration 068 dropped the
// redundant idx_agent_logs_run and the per-run log queries still use
// idx_agent_logs_run_timestamp.
func TestAgentLogsQueriesUseCompositeIndex(t *testing.T) {
	db, err := Open(t.TempDir() + "/m68.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var n int
	if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='idx_agent_logs_run'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("idx_agent_logs_run still exists")
	}

	queries := []string{
		`SELECT * FROM agent_logs WHERE agent_run_id = 'r' ORDER BY timestamp ASC`,
		`SELECT COUNT(*) FROM agent_logs WHERE agent_run_id = 'r'`,
		`SELECT l.* FROM agent_logs l WHERE l.agent_run_id = 'r' AND l.timestamp < 'x' ORDER BY l.timestamp DESC, l.id DESC LIMIT 5`,
	}
	for _, q := range queries {
		rows, err := db.SQL().Query("EXPLAIN QUERY PLAN " + q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		if !strings.Contains(strings.Join(plan, "|"), "idx_agent_logs_run_timestamp") {
			t.Errorf("query %q plan %v does not use composite index", q, plan)
		}
	}
}
