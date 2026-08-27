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

	// WorktreeSweepInterval is how often the orphan-worktree sweeper (see
	// internal/worktreesweep) reconciles each repo's .ate-worktrees/<id>
	// directories against live (non-archived) task/chat-session ids,
	// reclaiming anything else — worktrees left behind by archiving a task on
	// a non-terminal label, or orphaned by a crash. Always enabled (unlike
	// backup, gated by BackupDir); this only controls how often it runs.
	WorktreeSweepInterval time.Duration `yaml:"worktree_sweep_interval"`

	// ChatMaxSessions caps the number of concurrent interactive chat terminal
	// sessions (agent.TerminalManager) kept alive at once. Each holds a live
	// PTY subprocess plus a scrollback buffer indefinitely otherwise. 0 (the
	// default) means unlimited, matching pre-existing behavior.
	ChatMaxSessions int `yaml:"chat_max_sessions"`
	// ChatIdleTimeout, if > 0, is how long a chat terminal session may go
	// without an attached WebSocket connection before it's reaped (subprocess
	// killed, scrollback released). 0 (the default) disables idle reaping.
	ChatIdleTimeout time.Duration `yaml:"chat_idle_timeout"`

	// GitTimeout bounds each individual shell-out to `git` performed by the
	// agent package (worktree provisioning, fetch, commit, push, merge,
	// branch cleanup). Without a bound, a stalled remote or an interactive
	// credential prompt can hang a `git` call indefinitely, which in turn
	// blocks the whole dispatch sweep loop since tasks are dispatched
	// serially. With this bound, a stuck git call fails that one task within
	// GitTimeout and dispatch keeps working on the next task/sweep instead of
	// stalling system-wide.
	GitTimeout time.Duration `yaml:"git_timeout"`

	// MaxDailyCostUSD, if > 0, caps total recorded agent spend (see
	// agent_runs.cost_usd, summed across every provider/task/agent config)
	// for the current UTC calendar day. Once cumulative spend for today
	// reaches this cap, the dispatcher stops starting new runs globally
	// (see Dispatcher.checkGlobalCostBudget) until the next UTC day begins —
	// in-flight runs are left to finish, only new dispatch is gated. 0 (the
	// default) means unlimited, matching how RepoBaseDir/APIToken treat "not
	// configured" — this must never default to a nonzero cap that could
	// silently halt an existing deployment on upgrade.
	MaxDailyCostUSD float64 `yaml:"max_daily_cost_usd"`
	// MaxMonthlyCostUSD is MaxDailyCostUSD's calendar-month equivalent (UTC),
	// checked independently — either cap tripping halts dispatch globally.
	// 0 (the default) means unlimited.
	MaxMonthlyCostUSD float64 `yaml:"max_monthly_cost_usd"`

	// NotifyWebhookURL, if set, enables the outbound webhook notifier (see
	// internal/notify): a JSON POST fires whenever a task needs a human
	// (task.needs_human, arrival at a human-gate label, task.cost_warning,
	// system.cost_budget_tripped). Empty (the default) disables it entirely
	// — this must never default to a nonzero value, since it is operator
	// infrastructure (a Slack/Discord/ntfy endpoint, etc.) that doesn't
	// exist until configured. Treat this value as a secret: many webhook
	// URLs (e.g. Slack incoming webhooks) embed an auth token in the path,
	// and it is never logged in full (see internal/notify/deliver.go).
	NotifyWebhookURL string `yaml:"notify_webhook_url"`
	// NotifyBaseURL, if set, is used to build a deep link to the task detail
	// page (<base>/tasks/<id>) included in each notification payload. Empty
	// (the default) omits the link entirely rather than guessing a
	// server-local address that wouldn't work for whoever receives the
	// notification.
	NotifyBaseURL string `yaml:"notify_base_url"`
	// NotifyDebounce is the minimum time between two outbound notifications
	// sharing the same dedupe key (reason+task_id), suppressing a retry
	// storm from spamming the webhook. Only meaningful when
	// NotifyWebhookURL is set.
	NotifyDebounce time.Duration `yaml:"notify_debounce"`
}

// Defaults returns a Config populated with safe defaults.
func Defaults() Config {
	return Config{
		DBPath:                "agent-task-editor.db",
		Port:                  "8080",
		CORSOrigins:           "http://localhost:5173,http://localhost:8080",
		LLMBaseURL:            "https://api.openai.com/v1",
		MaxWorkers:            5,
		GitHubSyncInterval:    30 * time.Second,
		IssueSyncInterval:     60 * time.Second,
		ScheduleInterval:      30 * time.Second,
		BackupInterval:        24 * time.Hour,
		BackupKeep:            7,
		LogRetentionDays:      0,
		LogRetentionInterval:  1 * time.Hour,
		WorktreeSweepInterval: 10 * time.Minute,
		ChatMaxSessions:       0,
		ChatIdleTimeout:       0,
		GitTimeout:            120 * time.Second,
		NotifyDebounce:        5 * time.Minute,
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
	if v := os.Getenv("WORKTREE_SWEEP_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.WorktreeSweepInterval = d
		} else {
			slog.Warn("invalid WORKTREE_SWEEP_INTERVAL; using default", "value", v, "default", cfg.WorktreeSweepInterval)
		}
	}
	if v := os.Getenv("CHAT_MAX_SESSIONS"); v != "" {
		// n >= 0: 0 is the valid "unlimited" sentinel and must be settable via
		// env explicitly, not just left at the zero-value default.
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.ChatMaxSessions = n
		} else {
			slog.Warn("invalid CHAT_MAX_SESSIONS; using default", "value", v, "default", cfg.ChatMaxSessions)
		}
	}
	if v := os.Getenv("CHAT_IDLE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.ChatIdleTimeout = d
		} else {
			slog.Warn("invalid CHAT_IDLE_TIMEOUT; using default", "value", v, "default", cfg.ChatIdleTimeout)
		}
	}
	if v := os.Getenv("GIT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.GitTimeout = d
		} else {
			slog.Warn("invalid GIT_TIMEOUT; using default", "value", v, "default", cfg.GitTimeout)
		}
	}
	if v := os.Getenv("MAX_DAILY_COST_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.MaxDailyCostUSD = f
		} else {
			slog.Warn("invalid MAX_DAILY_COST_USD; using default", "value", v, "default", cfg.MaxDailyCostUSD)
		}
	}
	if v := os.Getenv("MAX_MONTHLY_COST_USD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.MaxMonthlyCostUSD = f
		} else {
			slog.Warn("invalid MAX_MONTHLY_COST_USD; using default", "value", v, "default", cfg.MaxMonthlyCostUSD)
		}
	}
	if v := os.Getenv("NOTIFY_WEBHOOK_URL"); v != "" {
		cfg.NotifyWebhookURL = v
	}
	if v := os.Getenv("NOTIFY_BASE_URL"); v != "" {
		cfg.NotifyBaseURL = v
	}
	if v := os.Getenv("NOTIFY_DEBOUNCE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.NotifyDebounce = d
		} else {
			slog.Warn("invalid NOTIFY_DEBOUNCE; using default", "value", v, "default", cfg.NotifyDebounce)
		}
	}

	return cfg, nil
}
