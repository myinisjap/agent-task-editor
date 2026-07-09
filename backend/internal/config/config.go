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
	APITokens          map[string]string `yaml:"api_tokens"`
	MCPBinary          string            `yaml:"mcp_server_path"`
	LLMBaseURL         string            `yaml:"llm_base_url"`
	LLMAPIKey          string            `yaml:"llm_api_key"`
	MaxWorkers         int               `yaml:"max_workers"`
	RepoBaseDir        string            `yaml:"repo_base_dir"`
	UploadDir          string            `yaml:"upload_dir"`
	GitHubSyncInterval time.Duration     `yaml:"github_sync_interval"`
	IssueSyncInterval  time.Duration     `yaml:"issue_sync_interval"`

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
}

// Defaults returns a Config populated with safe defaults.
func Defaults() Config {
	return Config{
		DBPath:             "agent-task-editor.db",
		Port:               "8080",
		CORSOrigins:        "*",
		LLMBaseURL:         "https://api.openai.com/v1",
		MaxWorkers:         5,
		GitHubSyncInterval: 30 * time.Second,
		IssueSyncInterval:  60 * time.Second,
		BackupInterval:     24 * time.Hour,
		BackupKeep:         7,
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
	if v := os.Getenv("MCP_SERVER_PATH"); v != "" {
		cfg.MCPBinary = v
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

	return cfg, nil
}
