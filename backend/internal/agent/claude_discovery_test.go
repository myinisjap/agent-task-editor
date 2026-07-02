package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestListInstalledClaudePluginsFrom_Fixture(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{
		"plugins": {
			"frontend-design@claude-plugins-official": [],
			"oh-my-claudecode@omc": []
		}
	}`)

	got := listInstalledClaudePluginsFrom(home)
	want := []ClaudePlugin{
		{ID: "frontend-design@claude-plugins-official", Name: "frontend-design", Marketplace: "claude-plugins-official"},
		{ID: "oh-my-claudecode@omc", Name: "oh-my-claudecode", Marketplace: "omc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestListInstalledClaudePluginsFrom_MissingFile(t *testing.T) {
	home := t.TempDir()
	got := listInstalledClaudePluginsFrom(home)
	if len(got) != 0 {
		t.Fatalf("want empty slice for missing file, got %+v", got)
	}
}

func TestListInstalledClaudePluginsFrom_UnparseableFile(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `not json`)
	got := listInstalledClaudePluginsFrom(home)
	if len(got) != 0 {
		t.Fatalf("want empty slice for unparseable file, got %+v", got)
	}
}

func TestListAvailableClaudeMCPServersFrom_Fixture(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{
		"mcpServers": {
			"context7": {"type": "stdio", "command": "context7-mcp"},
			"github": {"type": "stdio", "command": "github-mcp"}
		},
		"projects": {
			"/some/path": {"mcpServers": {"project-only": {"type": "stdio", "command": "x"}}}
		}
	}`)

	got := listAvailableClaudeMCPServersFrom(home)
	want := []string{"context7", "github"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestListAvailableClaudeMCPServersFrom_MissingFile(t *testing.T) {
	home := t.TempDir()
	got := listAvailableClaudeMCPServersFrom(home)
	if len(got) != 0 {
		t.Fatalf("want empty slice for missing file, got %+v", got)
	}
}

func TestRawMCPServerConfigsFrom_SkipsTaskEditorAndMissing(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude.json"), `{
		"mcpServers": {
			"context7": {"type": "stdio", "command": "context7-mcp"},
			"task-editor": {"type": "stdio", "command": "should-not-be-used"}
		}
	}`)

	got := rawMCPServerConfigsFrom(home, []string{"context7", "task-editor", "does-not-exist"})
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	if _, ok := got["context7"]; !ok {
		t.Fatalf("want context7 in result, got %+v", got)
	}
	if _, ok := got["task-editor"]; ok {
		t.Fatalf("task-editor should be excluded, got %+v", got)
	}

	var entry mcpServerEntry
	if err := json.Unmarshal(got["context7"], &entry); err != nil {
		t.Fatalf("unmarshal context7 entry: %v", err)
	}
	if entry.Command != "context7-mcp" {
		t.Fatalf("want command context7-mcp, got %q", entry.Command)
	}
}
