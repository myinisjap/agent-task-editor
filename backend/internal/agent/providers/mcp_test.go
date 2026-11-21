package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myinisjap/agent-task-editor/backend/internal/scopedtoken"
)

func TestMCPManagerPrepare_ScopedTokenNotAdminToken(t *testing.T) {
	t.Setenv("API_TOKEN", "admin-secret")
	m := &MCPManager{ServerBinary: "/app/mcp-server"}
	cfg, err := m.Prepare("run-1", nil, nil, nil, &SubtaskEnv{BackendURL: "http://x", TaskID: "task-1", Enabled: true, MaxSubtasks: 3})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg.ConfigFile)
	if strings.Contains(string(data), "admin-secret") {
		t.Fatal("admin token leaked into MCP config")
	}
	if !strings.Contains(string(data), cfg.Token) || !strings.HasPrefix(cfg.Token, "ate_") {
		t.Fatalf("scoped token missing from config: %s", data)
	}
	// Private dir, not a shared predictable /tmp path.
	dir := filepath.Dir(cfg.ConfigFile)
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0700 || dir == os.TempDir() {
		t.Fatalf("config dir not private: %v %v", st, err)
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), "ate-mcp-run-1.json")); err == nil {
		t.Fatal("legacy shared config path was written")
	}
	g, ok := scopedtoken.Default.Lookup(cfg.Token)
	if !ok || g.TaskID != "task-1" || g.RunID != "run-1" {
		t.Fatalf("grant: %+v ok=%v", g, ok)
	}

	cfg.Cleanup()
	if _, ok := scopedtoken.Default.Lookup(cfg.Token); ok {
		t.Error("token still valid after Cleanup")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("config dir not removed")
	}
}

func TestMCPManagerPrepare_NoSubtasksNoToken(t *testing.T) {
	m := &MCPManager{ServerBinary: "/app/mcp-server"}
	cfg, err := m.Prepare("run-2", nil, nil, nil, &SubtaskEnv{TaskID: "t", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Cleanup()
	data, _ := os.ReadFile(cfg.ConfigFile)
	if cfg.Token != "" || strings.Contains(string(data), "API_TOKEN") {
		t.Errorf("unexpected token when subtasks disabled: %s", data)
	}
}

func TestBuildSidecarEnv_ReviewPass(t *testing.T) {
	env, _, err := buildSidecarEnv("r", "/tmp/x", nil, nil, &SubtaskEnv{ReviewPass: true})
	if err != nil {
		t.Fatal(err)
	}
	if env["REVIEW_PASS"] != "1" {
		t.Fatalf("REVIEW_PASS not set: %v", env)
	}
	for _, se := range []*SubtaskEnv{nil, {}} {
		env, _, err = buildSidecarEnv("r", "/tmp/x", nil, nil, se)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := env["REVIEW_PASS"]; ok {
			t.Fatalf("REVIEW_PASS set unexpectedly: %v", env)
		}
	}
}

func TestReadResult_ReviewFindings(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "result.json")
	body := `{"Status":"completed","ReviewFindings":{"summary":"s","findings":[{"severity":"major","confidence":"high","path":"a.go","line":3,"title":"t","body":"b"}]}}`
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := (&MCPRunConfig{ResultFile: f}).ReadResult()
	if r.ReviewFindings == nil || len(r.ReviewFindings.Findings) != 1 || r.ReviewFindings.Findings[0].Line != 3 {
		t.Fatalf("got %+v", r.ReviewFindings)
	}
}
