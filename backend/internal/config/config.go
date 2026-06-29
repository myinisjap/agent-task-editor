// Package config loads server configuration from a YAML file,
// with environment variables taking precedence over file values.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all server configuration values.
type Config struct {
	DBPath             string        `yaml:"db_path"`
	Port               string        `yaml:"port"`
	CORSOrigins        string        `yaml:"cors_origins"`
	APIToken           string        `yaml:"api_token"`
	MCPBinary          string        `yaml:"mcp_server_path"`
	LLMBaseURL         string        `yaml:"llm_base_url"`
	LLMAPIKey          string        `yaml:"llm_api_key"`
	MaxWorkers         int           `yaml:"max_workers"`
	RepoBaseDir        string        `yaml:"repo_base_dir"`
	UploadDir          string        `yaml:"upload_dir"`
	GitHubSyncInterval time.Duration `yaml:"github_sync_interval"`
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

	return cfg, nil
}
