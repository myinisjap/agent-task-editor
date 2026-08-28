package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/config"
)

func TestDefaults_PopulatesRequiredFields(t *testing.T) {
	cfg := config.Defaults()

	if cfg.DBPath == "" {
		t.Error("DBPath must have a default")
	}
	if cfg.Port == "" {
		t.Error("Port must have a default")
	}
	if cfg.MaxWorkers <= 0 {
		t.Errorf("MaxWorkers must be positive, got %d", cfg.MaxWorkers)
	}
	if cfg.BackupDir != "" {
		t.Errorf("BackupDir must default to empty (disabled), got %q", cfg.BackupDir)
	}
	if cfg.BackupInterval <= 0 {
		t.Errorf("BackupInterval must have a positive default, got %v", cfg.BackupInterval)
	}
	if cfg.BackupKeep <= 0 {
		t.Errorf("BackupKeep must have a positive default, got %d", cfg.BackupKeep)
	}
	if cfg.ScheduleInterval <= 0 {
		t.Errorf("ScheduleInterval must have a positive default, got %v", cfg.ScheduleInterval)
	}
}

func TestDefaults_CORSOriginsDefaultsToLocalOrigins(t *testing.T) {
	cfg := config.Defaults()
	want := "http://localhost:5173,http://localhost:8080"
	if cfg.CORSOrigins != want {
		t.Errorf("expected default CORSOrigins %q, got %q", want, cfg.CORSOrigins)
	}
}

func TestLoad_CORSOriginsWildcard_ExplicitOverride(t *testing.T) {
	t.Setenv("CORS_ORIGINS", "*")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CORSOrigins != "*" {
		t.Errorf("expected explicit CORS_ORIGINS=* to be honored, got %q", cfg.CORSOrigins)
	}
}

func TestDefaults_LogRetentionDisabledByDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.LogRetentionDays != 0 {
		t.Errorf("LogRetentionDays must default to 0 (disabled), got %d", cfg.LogRetentionDays)
	}
	if cfg.LogRetentionInterval <= 0 {
		t.Errorf("LogRetentionInterval must have a positive default, got %v", cfg.LogRetentionInterval)
	}
}

func TestLoad_EmptyPath_ReturnsDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defaults := config.Defaults()
	if cfg.Port != defaults.Port {
		t.Errorf("expected default port %s, got %s", defaults.Port, cfg.Port)
	}
	if cfg.MaxWorkers != defaults.MaxWorkers {
		t.Errorf("expected default max_workers %d, got %d", defaults.MaxWorkers, cfg.MaxWorkers)
	}
}

func TestLoad_MissingFile_ReturnsDefaults(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if cfg.Port == "" {
		t.Error("expected defaults when file is missing")
	}
}

func TestLoad_FromYAML(t *testing.T) {
	// Clear env vars that would override YAML values
	t.Setenv("PORT", "")
	t.Setenv("DB_PATH", "")

	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	_, _ = f.WriteString("port: \"9090\"\ndb_path: myapp.db\nmax_workers: 12\napi_token: tok123\n")
	_ = f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("expected port 9090, got %s", cfg.Port)
	}
	if cfg.DBPath != "myapp.db" {
		t.Errorf("expected db_path myapp.db, got %s", cfg.DBPath)
	}
	if cfg.MaxWorkers != 12 {
		t.Errorf("expected max_workers 12, got %d", cfg.MaxWorkers)
	}
	if cfg.APIToken != "tok123" {
		t.Errorf("expected api_token tok123, got %s", cfg.APIToken)
	}
}

func TestLoad_EnvVarsOverrideDefaults(t *testing.T) {
	t.Setenv("PORT", "7777")
	t.Setenv("DB_PATH", "env-override.db")
	t.Setenv("API_TOKEN", "env-token")
	t.Setenv("CORS_ORIGINS", "https://my-app.com")
	t.Setenv("LLM_BASE_URL", "https://custom-llm.example.com/v1")
	t.Setenv("LLM_API_KEY", "key-xyz")
	t.Setenv("MCP_SERVER_PATH", "/usr/local/bin/mcp")
	t.Setenv("MCP_BOARD_PATH", "/usr/local/bin/mcp-board")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Port", cfg.Port, "7777"},
		{"DBPath", cfg.DBPath, "env-override.db"},
		{"APIToken", cfg.APIToken, "env-token"},
		{"CORSOrigins", cfg.CORSOrigins, "https://my-app.com"},
		{"LLMBaseURL", cfg.LLMBaseURL, "https://custom-llm.example.com/v1"},
		{"LLMAPIKey", cfg.LLMAPIKey, "key-xyz"},
		{"MCPBinary", cfg.MCPBinary, "/usr/local/bin/mcp"},
		{"MCPBoardBinary", cfg.MCPBoardBinary, "/usr/local/bin/mcp-board"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: want %q, got %q", c.name, c.want, c.got)
		}
	}
}

func TestLoad_EnvVarsOverrideYAML(t *testing.T) {
	// Clear DB_PATH so YAML value is used for non-overridden field check
	t.Setenv("DB_PATH", "")

	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	_, _ = f.WriteString("port: \"9090\"\ndb_path: file.db\n")
	_ = f.Close()

	t.Setenv("PORT", "1234")

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Port != "1234" {
		t.Errorf("env var should override YAML: expected 1234, got %s", cfg.Port)
	}
	// Non-overridden YAML field is preserved
	if cfg.DBPath != "file.db" {
		t.Errorf("non-overridden YAML field should be preserved: expected file.db, got %s", cfg.DBPath)
	}
}

func TestLoad_BackupEnvVarsOverrideDefaults(t *testing.T) {
	t.Setenv("BACKUP_DIR", "/data/backups")
	t.Setenv("BACKUP_INTERVAL", "12h")
	t.Setenv("BACKUP_KEEP", "3")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.BackupDir != "/data/backups" {
		t.Errorf("expected backup dir /data/backups, got %s", cfg.BackupDir)
	}
	if cfg.BackupInterval != 12*time.Hour {
		t.Errorf("expected backup interval 12h, got %v", cfg.BackupInterval)
	}
	if cfg.BackupKeep != 3 {
		t.Errorf("expected backup keep 3, got %d", cfg.BackupKeep)
	}
}

func TestLoad_BackupFromYAML(t *testing.T) {
	t.Setenv("BACKUP_DIR", "")
	t.Setenv("BACKUP_INTERVAL", "")
	t.Setenv("BACKUP_KEEP", "")

	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	_, _ = f.WriteString("backup_dir: /data/backups\nbackup_interval: 6h\nbackup_keep: 14\n")
	_ = f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.BackupDir != "/data/backups" {
		t.Errorf("expected backup dir /data/backups, got %s", cfg.BackupDir)
	}
	if cfg.BackupInterval != 6*time.Hour {
		t.Errorf("expected backup interval 6h, got %v", cfg.BackupInterval)
	}
	if cfg.BackupKeep != 14 {
		t.Errorf("expected backup keep 14, got %d", cfg.BackupKeep)
	}
}

func TestLoad_InvalidBackupInterval_UsesDefault(t *testing.T) {
	t.Setenv("BACKUP_INTERVAL", "not-a-duration")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.BackupInterval != config.Defaults().BackupInterval {
		t.Errorf("expected default backup interval on invalid input, got %v", cfg.BackupInterval)
	}
}

func TestLoad_ScheduleIntervalEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("SCHEDULE_INTERVAL", "15s")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ScheduleInterval != 15*time.Second {
		t.Errorf("expected schedule interval 15s, got %v", cfg.ScheduleInterval)
	}
}

func TestLoad_InvalidScheduleInterval_UsesDefault(t *testing.T) {
	t.Setenv("SCHEDULE_INTERVAL", "not-a-duration")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ScheduleInterval != config.Defaults().ScheduleInterval {
		t.Errorf("expected default schedule interval on invalid input, got %v", cfg.ScheduleInterval)
	}
}

func TestLoad_LogRetentionEnvVarsOverrideDefaults(t *testing.T) {
	t.Setenv("LOG_RETENTION_DAYS", "30")
	t.Setenv("LOG_RETENTION_INTERVAL", "2h")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogRetentionDays != 30 {
		t.Errorf("expected log retention days 30, got %d", cfg.LogRetentionDays)
	}
	if cfg.LogRetentionInterval != 2*time.Hour {
		t.Errorf("expected log retention interval 2h, got %v", cfg.LogRetentionInterval)
	}
}

func TestLoad_LogRetentionDaysZero_ExplicitlyDisables(t *testing.T) {
	// 0 is a valid, explicit "disabled" value and must be settable via env,
	// not just left at the zero-value default.
	t.Setenv("LOG_RETENTION_DAYS", "0")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogRetentionDays != 0 {
		t.Errorf("expected log retention days 0, got %d", cfg.LogRetentionDays)
	}
}

func TestLoad_InvalidLogRetentionDays_UsesDefault(t *testing.T) {
	t.Setenv("LOG_RETENTION_DAYS", "not-a-number")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogRetentionDays != config.Defaults().LogRetentionDays {
		t.Errorf("expected default log retention days on invalid input, got %d", cfg.LogRetentionDays)
	}
}

func TestLoad_InvalidLogRetentionInterval_UsesDefault(t *testing.T) {
	t.Setenv("LOG_RETENTION_INTERVAL", "not-a-duration")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogRetentionInterval != config.Defaults().LogRetentionInterval {
		t.Errorf("expected default log retention interval on invalid input, got %v", cfg.LogRetentionInterval)
	}
}

func TestLoad_WorktreeSweepIntervalEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("WORKTREE_SWEEP_INTERVAL", "5m")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.WorktreeSweepInterval != 5*time.Minute {
		t.Errorf("expected worktree sweep interval 5m, got %v", cfg.WorktreeSweepInterval)
	}
}

func TestLoad_InvalidWorktreeSweepInterval_UsesDefault(t *testing.T) {
	t.Setenv("WORKTREE_SWEEP_INTERVAL", "not-a-duration")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.WorktreeSweepInterval != config.Defaults().WorktreeSweepInterval {
		t.Errorf("expected default worktree sweep interval on invalid input, got %v", cfg.WorktreeSweepInterval)
	}
}

func TestLoad_ChatMaxSessionsEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("CHAT_MAX_SESSIONS", "10")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ChatMaxSessions != 10 {
		t.Errorf("expected chat max sessions 10, got %d", cfg.ChatMaxSessions)
	}
}

func TestLoad_ChatMaxSessionsZero_ExplicitlyUnlimited(t *testing.T) {
	// 0 is a valid, explicit "unlimited" value and must be settable via env,
	// not just left at the zero-value default.
	t.Setenv("CHAT_MAX_SESSIONS", "0")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ChatMaxSessions != 0 {
		t.Errorf("expected chat max sessions 0, got %d", cfg.ChatMaxSessions)
	}
}

func TestLoad_InvalidChatMaxSessions_UsesDefault(t *testing.T) {
	t.Setenv("CHAT_MAX_SESSIONS", "not-a-number")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ChatMaxSessions != config.Defaults().ChatMaxSessions {
		t.Errorf("expected default chat max sessions on invalid input, got %d", cfg.ChatMaxSessions)
	}
}

func TestLoad_ChatIdleTimeoutEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("CHAT_IDLE_TIMEOUT", "2h")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ChatIdleTimeout != 2*time.Hour {
		t.Errorf("expected chat idle timeout 2h, got %v", cfg.ChatIdleTimeout)
	}
}

func TestLoad_InvalidChatIdleTimeout_UsesDefault(t *testing.T) {
	t.Setenv("CHAT_IDLE_TIMEOUT", "not-a-duration")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ChatIdleTimeout != config.Defaults().ChatIdleTimeout {
		t.Errorf("expected default chat idle timeout on invalid input, got %v", cfg.ChatIdleTimeout)
	}
}

func TestLoad_GitTimeoutEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("GIT_TIMEOUT", "45s")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.GitTimeout != 45*time.Second {
		t.Errorf("expected git timeout 45s, got %v", cfg.GitTimeout)
	}
}

func TestLoad_InvalidGitTimeout_UsesDefault(t *testing.T) {
	t.Setenv("GIT_TIMEOUT", "not-a-duration")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.GitTimeout != config.Defaults().GitTimeout {
		t.Errorf("expected default git timeout on invalid input, got %v", cfg.GitTimeout)
	}
}

func TestLoad_MaxDailyCostUSDEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("MAX_DAILY_COST_USD", "25.50")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MaxDailyCostUSD != 25.50 {
		t.Errorf("expected max daily cost 25.50, got %v", cfg.MaxDailyCostUSD)
	}
}

func TestLoad_InvalidMaxDailyCostUSD_UsesDefault(t *testing.T) {
	t.Setenv("MAX_DAILY_COST_USD", "not-a-number")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MaxDailyCostUSD != config.Defaults().MaxDailyCostUSD {
		t.Errorf("expected default max daily cost on invalid input, got %v", cfg.MaxDailyCostUSD)
	}
}

func TestLoad_MaxDailyCostUSDDefaultsToUnlimited(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MaxDailyCostUSD != 0 {
		t.Errorf("expected max daily cost to default to 0 (unlimited), got %v", cfg.MaxDailyCostUSD)
	}
}

func TestLoad_MaxMonthlyCostUSDEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("MAX_MONTHLY_COST_USD", "500")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MaxMonthlyCostUSD != 500 {
		t.Errorf("expected max monthly cost 500, got %v", cfg.MaxMonthlyCostUSD)
	}
}

func TestLoad_InvalidMaxMonthlyCostUSD_UsesDefault(t *testing.T) {
	t.Setenv("MAX_MONTHLY_COST_USD", "not-a-number")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MaxMonthlyCostUSD != config.Defaults().MaxMonthlyCostUSD {
		t.Errorf("expected default max monthly cost on invalid input, got %v", cfg.MaxMonthlyCostUSD)
	}
}

func TestLoad_MaxMonthlyCostUSDDefaultsToUnlimited(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MaxMonthlyCostUSD != 0 {
		t.Errorf("expected max monthly cost to default to 0 (unlimited), got %v", cfg.MaxMonthlyCostUSD)
	}
}

func TestLoad_LogRetentionFromYAML(t *testing.T) {
	t.Setenv("LOG_RETENTION_DAYS", "")
	t.Setenv("LOG_RETENTION_INTERVAL", "")

	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	_, _ = f.WriteString("log_retention_days: 14\nlog_retention_interval: 30m\n")
	_ = f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LogRetentionDays != 14 {
		t.Errorf("expected log retention days 14, got %d", cfg.LogRetentionDays)
	}
	if cfg.LogRetentionInterval != 30*time.Minute {
		t.Errorf("expected log retention interval 30m, got %v", cfg.LogRetentionInterval)
	}
}

func TestLoad_NoTokens_DefaultsToEmpty(t *testing.T) {
	t.Setenv("API_TOKEN", "")
	t.Setenv("API_TOKENS", "")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APIToken != "" {
		t.Errorf("expected empty APIToken by default, got %q", cfg.APIToken)
	}
	if len(cfg.APITokens) != 0 {
		t.Errorf("expected empty/nil APITokens by default, got %v", cfg.APITokens)
	}
}

func TestLoad_APITokensEnvVar_Parsed(t *testing.T) {
	t.Setenv("API_TOKENS", "alice:tok1,bob:tok2")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APITokens["alice"] != "tok1" {
		t.Errorf("expected alice:tok1, got %q", cfg.APITokens["alice"])
	}
	if cfg.APITokens["bob"] != "tok2" {
		t.Errorf("expected bob:tok2, got %q", cfg.APITokens["bob"])
	}
	if len(cfg.APITokens) != 2 {
		t.Errorf("expected exactly 2 tokens, got %d: %v", len(cfg.APITokens), cfg.APITokens)
	}
}

func TestLoad_APITokensEnvVar_TrimsWhitespaceAndSkipsMalformed(t *testing.T) {
	t.Setenv("API_TOKENS", " alice : tok1 , malformed-entry, bob:tok2, ,")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APITokens["alice"] != "tok1" {
		t.Errorf("expected alice:tok1 (trimmed), got %q", cfg.APITokens["alice"])
	}
	if cfg.APITokens["bob"] != "tok2" {
		t.Errorf("expected bob:tok2, got %q", cfg.APITokens["bob"])
	}
	if len(cfg.APITokens) != 2 {
		t.Errorf("expected malformed entries to be skipped, got %v", cfg.APITokens)
	}
}

func TestLoad_APITokensEnvVar_OverridesYAML(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	_, _ = f.WriteString("api_tokens:\n  alice: yaml-tok\n  carol: carol-tok\n")
	_ = f.Close()

	t.Setenv("API_TOKENS", "alice:env-tok")

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APITokens["alice"] != "env-tok" {
		t.Errorf("expected env var to override YAML for alice, got %q", cfg.APITokens["alice"])
	}
	if cfg.APITokens["carol"] != "carol-tok" {
		t.Errorf("expected non-overridden YAML entry to be preserved, got %q", cfg.APITokens["carol"])
	}
}

func TestLoad_APITokensFromYAML(t *testing.T) {
	t.Setenv("API_TOKENS", "")

	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	_, _ = f.WriteString("api_tokens:\n  alice: tok1\n  bob: tok2\n")
	_ = f.Close()

	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.APITokens["alice"] != "tok1" || cfg.APITokens["bob"] != "tok2" {
		t.Errorf("expected tokens from YAML, got %v", cfg.APITokens)
	}
}

func TestLoad_InvalidYAML_ReturnsError(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()

	_, _ = f.WriteString("port: [this is not valid yaml for a string\n")
	_ = f.Close()

	_, err = config.Load(f.Name())
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestDefaults_NotifyDisabledByDefault(t *testing.T) {
	cfg := config.Defaults()
	if cfg.NotifyWebhookURL != "" {
		t.Errorf("NotifyWebhookURL must default to empty (disabled), got %q", cfg.NotifyWebhookURL)
	}
	if cfg.NotifyBaseURL != "" {
		t.Errorf("NotifyBaseURL must default to empty, got %q", cfg.NotifyBaseURL)
	}
	if cfg.NotifyDebounce != 5*time.Minute {
		t.Errorf("expected default NotifyDebounce of 5m, got %v", cfg.NotifyDebounce)
	}
}

func TestLoad_NotifyWebhookURLEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("NOTIFY_WEBHOOK_URL", "https://example.com/webhook/abc123")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.NotifyWebhookURL != "https://example.com/webhook/abc123" {
		t.Errorf("expected NotifyWebhookURL to be set from env, got %q", cfg.NotifyWebhookURL)
	}
}

func TestLoad_NotifyBaseURLEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("NOTIFY_BASE_URL", "https://ate.example.com")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.NotifyBaseURL != "https://ate.example.com" {
		t.Errorf("expected NotifyBaseURL to be set from env, got %q", cfg.NotifyBaseURL)
	}
}

func TestLoad_NotifyDebounceEnvVarOverridesDefault(t *testing.T) {
	t.Setenv("NOTIFY_DEBOUNCE", "10m")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.NotifyDebounce != 10*time.Minute {
		t.Errorf("expected notify debounce 10m, got %v", cfg.NotifyDebounce)
	}
}

func TestLoad_InvalidNotifyDebounce_UsesDefault(t *testing.T) {
	t.Setenv("NOTIFY_DEBOUNCE", "not-a-duration")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.NotifyDebounce != config.Defaults().NotifyDebounce {
		t.Errorf("expected default notify debounce on invalid input, got %v", cfg.NotifyDebounce)
	}
}
