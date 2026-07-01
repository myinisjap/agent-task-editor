package config_test

import (
	"os"
	"testing"

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
