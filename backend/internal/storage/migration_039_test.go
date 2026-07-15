package storage

import (
	"database/sql"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// TestMigration039ProviderConfigsRoundTrip seeds an agent_configs row and a
// chat_sessions row against the schema as it existed just before migration
// 039 (provider_config split), applies 039, and verifies:
//   - a provider_configs row was backfilled for each, reusing the parent
//     row's own id as the provider_configs.id (per the migration's approach)
//   - agent_configs/chat_sessions.provider_config_id was wired to it
//   - the provider/model/env values were preserved verbatim (chat_sessions
//     never had an env column, so its provider_config defaults env to '{}')
//   - agent_configs/chat_sessions no longer have provider/model/env columns
//
// It then rolls 039 back down and verifies provider/model/env are restored
// onto agent_configs/chat_sessions and the provider_configs table is gone.
func TestMigration039ProviderConfigsRoundTrip(t *testing.T) {
	dbPath := t.TempDir() + "/migtest039.db"
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	driver, err := sqlite3.WithInstance(db.SQL(), &sqlite3.Config{})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite3", driver)
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}

	// Roll back to just before 039 so we can seed rows against the old
	// (provider/model/env-on-agent_configs, provider/model-on-chat_sessions)
	// shape, then apply 039 forward from there. Version 38 is
	// 038_drop_chat_messages, which leaves chat_sessions with its
	// provider/model columns intact and no chat_messages table.
	const preMigrationVersion = 38
	if err := m.Migrate(preMigrationVersion); err != nil {
		t.Fatalf("down to version %d: %v", preMigrationVersion, err)
	}

	sqlDB := db.SQL()
	if _, err := sqlDB.Exec(`INSERT INTO workflows (id, name) VALUES ('wf1', 'Default')`); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO repos (id, name, path, workflow_id) VALUES ('repo1', 'repo', '/tmp/repo1', 'wf1')`); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	if _, err := sqlDB.Exec(`
		INSERT INTO agent_configs (id, name, provider, model, env, system_prompt, labels)
		VALUES ('ac1', 'My Agent', 'claude', 'claude-opus', '{"ANTHROPIC_API_KEY":"secret-key"}', 'do stuff', '["in_progress"]')
	`); err != nil {
		t.Fatalf("seed agent_config: %v", err)
	}
	if _, err := sqlDB.Exec(`
		INSERT INTO chat_sessions (id, repo_id, provider, model, title)
		VALUES ('cs1', 'repo1', 'gemini_cli', 'gemini-pro', 'My Chat')
	`); err != nil {
		t.Fatalf("seed chat_session: %v", err)
	}

	if err := m.Up(); err != nil {
		t.Fatalf("migrate up (applying 039): %v", err)
	}

	// agent_configs: provider_config_id should be wired to a provider_configs
	// row (reusing the agent_config's own id) preserving provider/model/env.
	var pcID, pcProvider, pcModel, pcEnv string
	if err := sqlDB.QueryRow(`SELECT provider_config_id FROM agent_configs WHERE id = 'ac1'`).Scan(&pcID); err != nil {
		t.Fatalf("query agent_configs.provider_config_id: %v", err)
	}
	if pcID != "ac1" {
		t.Fatalf("expected agent_configs.provider_config_id to reuse the row's own id 'ac1', got %q", pcID)
	}
	if err := sqlDB.QueryRow(`SELECT provider, model, env FROM provider_configs WHERE id = ?`, pcID).Scan(&pcProvider, &pcModel, &pcEnv); err != nil {
		t.Fatalf("query provider_configs for ac1: %v", err)
	}
	if pcProvider != "claude" || pcModel != "claude-opus" || pcEnv != `{"ANTHROPIC_API_KEY":"secret-key"}` {
		t.Fatalf("unexpected backfilled provider_config for ac1: provider=%q model=%q env=%q", pcProvider, pcModel, pcEnv)
	}

	// chat_sessions: same treatment (env defaults to '{}' since chat_sessions
	// never had its own env column).
	var csPCID, csProvider, csModel, csEnv string
	if err := sqlDB.QueryRow(`SELECT provider_config_id FROM chat_sessions WHERE id = 'cs1'`).Scan(&csPCID); err != nil {
		t.Fatalf("query chat_sessions.provider_config_id: %v", err)
	}
	if csPCID != "cs1" {
		t.Fatalf("expected chat_sessions.provider_config_id to reuse the row's own id 'cs1', got %q", csPCID)
	}
	if err := sqlDB.QueryRow(`SELECT provider, model, env FROM provider_configs WHERE id = ?`, csPCID).Scan(&csProvider, &csModel, &csEnv); err != nil {
		t.Fatalf("query provider_configs for cs1: %v", err)
	}
	if csProvider != "gemini_cli" || csModel != "gemini-pro" || csEnv != "{}" {
		t.Fatalf("unexpected backfilled provider_config for cs1: provider=%q model=%q env=%q", csProvider, csModel, csEnv)
	}

	// The old columns should be gone from both tables.
	assertColumnAbsent(t, sqlDB, "agent_configs", "provider")
	assertColumnAbsent(t, sqlDB, "agent_configs", "model")
	assertColumnAbsent(t, sqlDB, "agent_configs", "env")
	assertColumnAbsent(t, sqlDB, "chat_sessions", "provider")
	assertColumnAbsent(t, sqlDB, "chat_sessions", "model")

	// Now roll 039 back down and verify the original columns/values are restored.
	if err := m.Migrate(preMigrationVersion); err != nil {
		t.Fatalf("down to version %d (039 rollback): %v", preMigrationVersion, err)
	}

	var provider, model, env string
	if err := sqlDB.QueryRow(`SELECT provider, model, env FROM agent_configs WHERE id = 'ac1'`).Scan(&provider, &model, &env); err != nil {
		t.Fatalf("query restored agent_configs columns: %v", err)
	}
	if provider != "claude" || model != "claude-opus" || env != `{"ANTHROPIC_API_KEY":"secret-key"}` {
		t.Fatalf("agent_configs values not restored correctly after down migration: provider=%q model=%q env=%q", provider, model, env)
	}

	var csProviderRestored, csModelRestored string
	if err := sqlDB.QueryRow(`SELECT provider, model FROM chat_sessions WHERE id = 'cs1'`).Scan(&csProviderRestored, &csModelRestored); err != nil {
		t.Fatalf("query restored chat_sessions columns: %v", err)
	}
	if csProviderRestored != "gemini_cli" || csModelRestored != "gemini-pro" {
		t.Fatalf("chat_sessions values not restored correctly after down migration: provider=%q model=%q", csProviderRestored, csModelRestored)
	}

	var tableCount int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'provider_configs'`).Scan(&tableCount); err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("expected provider_configs table to be dropped after down migration, still present")
	}
}

func assertColumnAbsent(t *testing.T, db *sql.DB, table, column string) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notnull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == column {
			t.Fatalf("column %q still present on %q after migration", column, table)
		}
	}
}
