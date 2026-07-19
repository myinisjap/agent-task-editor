// Package config loads server configuration from a YAML file,
// with environment variables taking precedence over file values.
package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all server configuration values.
type Config struct {
	DBPath      string `yaml:"db_path"`
	Port        string `yaml:"port"`
	CORSOrigins string `yaml:"cors_origins"`
	APIToken    string `yaml:"api_token"`
	// APITokens maps an actor name to a bearer token, allowing multiple
	// named credentials so human-triggered transitions can record *who*
	// approved them (see task_label_history.actor_id). APIToken above
	// remains supported as a legacy/anonymous fallback (actor name "").
	APITokens map[string]string `yaml:"api_tokens"`
	// MetricsToken optionally gates GET /metrics with its own bearer token,
	// independent of APIToken. Empty (the default) leaves /metrics
	// unauthenticated, matching most Prometheus scrape setups that can't
	// easily carry a different token than other tooling.
	MetricsToken       string        `yaml:"metrics_token"`
	MCPBinary          string        `yaml:"mcp_server_path"`
	MCPBoardBinary     string        `yaml:"mcp_board_path"`
	LLMBaseURL         string        `yaml:"llm_base_url"`
	LLMAPIKey          string        `yaml:"llm_api_key"`
	MaxWorkers         int           `yaml:"max_workers"`
	RepoBaseDir        string        `yaml:"repo_base_dir"`
	UploadDir          string        `yaml:"upload_dir"`
	GitHubSyncInterval time.Duration `yaml:"github_sync_interval"`
	IssueSyncInterval  time.Duration `yaml:"issue_sync_interval"`
	// ScheduleInterval is how often the task-schedule sweep runs (see
	// internal/schedule.Scheduler), checking every enabled task_schedule for
	// due firings. Cron expressions are minute-granularity, so this only
	// needs to be frequent enough to reliably catch each minute boundary.
	ScheduleInterval time.Duration `yaml:"schedule_interval"`

	// BackupDir, if set, enables the built-in scheduler that periodically
	// writes a rotated VACUUM INTO snapshot of the database to this
	// directory (see internal/backup.Scheduler). Empty disables it — this
	// is separate from the always-available GET /api/v1/backup endpoint.
	BackupDir string `yaml:"backup_dir"`
	// BackupInterval is how often the scheduler writes a new snapshot.
	// Only meaningful when BackupDir is set.
	BackupInterval time.Duration `yaml:"backup_interval"`
	// BackupKeep is how many of the most recent snapshots to retain in
	// BackupDir before pruning older ones.
	BackupKeep int `yaml:"backup_keep"`

	// LogRetentionDays, if > 0, enables the built-in agent-log pruner: logs
	// belonging to runs in a terminal status whose completed_at is older than
	// this many days are deleted on a schedule (see internal/logretention).
	// 0 (default) disables pruning entirely - behavior is unchanged from
	// today (logs are kept forever) unless explicitly opted in.
	LogRetentionDays int `yaml:"log_retention_days"`
	// LogRetentionInterval is how often the pruner runs. Only meaningful when
	// LogRetentionDays > 0.
	LogRetentionInterval time.Duration `yaml:"log_retention_interval"`

	// UpdateCheckEnabled, when true, opts into the Health page's "update
	// available" check, which shells out to `gh release view` to compare the
	// running version against the latest GitHub release tag. Disabled by
	// default so the app never phones home without the operator explicitly
	// opting in (and already having gh/network configured). See
	// internal/health.updateCheck.
	UpdateCheckEnabled bool `yaml:"update_check_enabled"`
}

// Defaults returns a Config populated with safe defaults.
func Defaults() Config {
	return Config{
		DBPath:               "agent-task-editor.db",
		Port:                 "8080",
		CORSOrigins:          "http://localhost:5173,http://localhost:8080",
		LLMBaseURL:           "https://api.openai.com/v1",
		MaxWorkers:           5,
		GitHubSyncInterval:   30 * time.Second,
		IssueSyncInterval:    60 * time.Second,
		ScheduleInterval:     30 * time.Second,
		BackupInterval:       24 * time.Hour,
		BackupKeep:           7,
		LogRetentionDays:     0,
		LogRetentionInterval: 1 * time.Hour,
	}
}

// Load reads a YAML config file (if path is non-empty and the file exists),
// then overrides fields from environment variables.
func Load(path string) (Config, error) {
	cfg := Defaults()

	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return cfg, err
			}
		}
	}

	// Env vars always win
	if v := os.Getenv("DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("CORS_ORIGINS"); v != "" {
		cfg.CORSOrigins = v
	}
	if v := os.Getenv("API_TOKEN"); v != "" {
		cfg.APIToken = v
	}
	if v := os.Getenv("API_TOKENS"); v != "" {
		if cfg.APITokens == nil {
			cfg.APITokens = make(map[string]string)
		}
		for _, pair := range strings.Split(v, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			name, token, ok := strings.Cut(pair, ":")
			name = strings.TrimSpace(name)
			token = strings.TrimSpace(token)
			if !ok || name == "" || token == "" {
				slog.Warn("skipping malformed API_TOKENS entry", "entry", pair)
				continue
			}
			cfg.APITokens[name] = token
		}
	}
	if v := os.Getenv("METRICS_TOKEN"); v != "" {
		cfg.MetricsToken = v
	}
	if v := os.Getenv("MCP_SERVER_PATH"); v != "" {
		cfg.MCPBinary = v
	}
	if v := os.Getenv("MCP_BOARD_PATH"); v != "" {
		cfg.MCPBoardBinary = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		cfg.LLMBaseURL = v
	}
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		cfg.LLMAPIKey = v
	}
	if v := os.Getenv("MAX_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxWorkers = n
		}
	}
	if v := os.Getenv("REPO_BASE_DIR"); v != "" {
		if strings.HasPrefix(v, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				v = filepath.Join(home, v[2:])
			}
		}
		cfg.RepoBaseDir = v
	}
	if v := os.Getenv("UPLOAD_DIR"); v != "" {
		cfg.UploadDir = v
	}
	if v := os.Getenv("GITHUB_SYNC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.GitHubSyncInterval = d
		} else {
			slog.Warn("invalid GITHUB_SYNC_INTERVAL; using default", "value", v, "default", cfg.GitHubSyncInterval)
		}
	}
	if v := os.Getenv("ISSUE_SYNC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.IssueSyncInterval = d
		} else {
			slog.Warn("invalid ISSUE_SYNC_INTERVAL; using default", "value", v, "default", cfg.IssueSyncInterval)
		}
	}
	if v := os.Getenv("SCHEDULE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.ScheduleInterval = d
		} else {
			slog.Warn("invalid SCHEDULE_INTERVAL; using default", "value", v, "default", cfg.ScheduleInterval)
		}
	}
	if v := os.Getenv("BACKUP_DIR"); v != "" {
		cfg.BackupDir = v
	}
	if v := os.Getenv("BACKUP_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.BackupInterval = d
		} else {
			slog.Warn("invalid BACKUP_INTERVAL; using default", "value", v, "default", cfg.BackupInterval)
		}
	}
	if v := os.Getenv("BACKUP_KEEP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BackupKeep = n
		} else {
			slog.Warn("invalid BACKUP_KEEP; using default", "value", v, "default", cfg.BackupKeep)
		}
	}
	if v := os.Getenv("LOG_RETENTION_DAYS"); v != "" {
		// n >= 0 (unlike BackupKeep's n > 0): 0 is the valid "disabled"
		// sentinel and must be settable via env explicitly, not just left at
		// the zero-value default.
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.LogRetentionDays = n
		} else {
			slog.Warn("invalid LOG_RETENTION_DAYS; using default", "value", v, "default", cfg.LogRetentionDays)
		}
	}
	if v := os.Getenv("LOG_RETENTION_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.LogRetentionInterval = d
		} else {
			slog.Warn("invalid LOG_RETENTION_INTERVAL; using default", "value", v, "default", cfg.LogRetentionInterval)
		}
	}
	if v := os.Getenv("UPDATE_CHECK_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.UpdateCheckEnabled = b
		} else {
			slog.Warn("invalid UPDATE_CHECK_ENABLED; using default", "value", v, "default", cfg.UpdateCheckEnabled)
		}
	}

	return cfg, nil
}
