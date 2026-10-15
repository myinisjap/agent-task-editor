package providers

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCodexRollout writes a rollout file for threadID under home, laid out
// the way codex 0.163.0 does.
func writeCodexRollout(t *testing.T, home, threadID, body string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "10", "15")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "rollout-2026-10-15T21-56-52-"+threadID+".jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const sampleRollout = `{"type":"session_meta","payload":{"id":"t1","cli_version":"0.163.0"}}
{"type":"turn_context","payload":{"model":"gpt-old"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":10}}}}
not json
{"type":"turn_context","payload":{"model":"gpt-4o"}}
{"type":"event_msg","payload":{"type":"token_count","info":null}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":300,"cached_input_tokens":200,"output_tokens":30}}}}
`

func TestCodexHomeDir(t *testing.T) {
	if got := codexHomeDir([]string{"HOME=/home/agent", "CODEX_HOME=/x"}); got != "/x" {
		t.Errorf("CODEX_HOME should win, got %q", got)
	}
	if got := codexHomeDir([]string{"PATH=/bin", "HOME=/home/agent"}); got != "/home/agent/.codex" {
		t.Errorf("got %q", got)
	}
	if got := codexHomeDir(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFindAndReadCodexRollout(t *testing.T) {
	home := t.TempDir()
	want := writeCodexRollout(t, home, "t1", sampleRollout)
	if got := findCodexRollout(home, "t1"); got != want {
		t.Fatalf("findCodexRollout = %q, want %q", got, want)
	}
	for _, bad := range []string{"", "nope", "*", "../t1"} {
		if got := findCodexRollout(home, bad); got != "" {
			t.Errorf("findCodexRollout(%q) = %q, want empty", bad, got)
		}
	}
	info := readCodexRollout(want)
	if info.Model != "gpt-4o" {
		t.Errorf("model = %q, want last turn_context model", info.Model)
	}
	if info.Total == nil || *info.Total != (runUsage{InputTokens: 300, CachedInputTokens: 200, OutputTokens: 30}) {
		t.Errorf("total = %+v, want last token_count total", info.Total)
	}
	if empty := readCodexRollout(filepath.Join(home, "missing")); empty.Model != "" || empty.Total != nil {
		t.Errorf("missing file should yield nothing, got %+v", empty)
	}
}

func TestCodexRunUsage(t *testing.T) {
	u := func(in, cached, out int64) *runUsage {
		return &runUsage{InputTokens: in, CachedInputTokens: cached, OutputTokens: out}
	}
	cases := []struct {
		name                  string
		reported, base, final *runUsage
		want                  *runUsage
	}{
		{"fresh run", u(10, 5, 2), nil, u(10, 5, 2), u(10, 5, 2)},
		{"resumed, codex reports session total", u(300, 200, 30), u(100, 40, 10), u(300, 200, 30), u(200, 160, 20)},
		{"resumed, codex reports this run only", u(50, 10, 5), u(100, 40, 10), u(150, 50, 15), u(50, 10, 5)},
		{"no rollout reads", u(50, 10, 5), nil, nil, u(50, 10, 5)},
		{"stopped mid-turn, fresh", nil, nil, u(70, 30, 7), u(70, 30, 7)},
		{"stopped mid-turn, resumed", nil, u(100, 40, 10), u(170, 70, 17), u(70, 30, 7)},
		{"stopped mid-turn, total reset", nil, u(100, 40, 10), u(60, 20, 6), u(60, 20, 6)},
		{"nothing at all", nil, u(1, 1, 1), nil, nil},
	}
	for _, c := range cases {
		got := codexRunUsage(c.reported, c.base, c.final)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestCodexCompletedToolStep(t *testing.T) {
	yes := []string{
		`{"type":"item.completed","item":{"id":"1","type":"command_execution","status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"2","type":"file_change"}}`,
		`{"type":"item.completed","item":{"id":"3","type":"mcp_tool_call"}}`,
		`{"type":"item.completed","item":{"id":"4","type":"web_search"}}`,
	}
	no := []string{
		`{"type":"item.started","item":{"id":"1","type":"command_execution"}}`,
		`{"type":"item.completed","item":{"id":"5","type":"agent_message","text":"item.completed"}}`,
		`{"type":"item.completed","item":{"id":"6","type":"reasoning"}}`,
		`{"type":"turn.completed","usage":{}}`,
		`plain text mentioning "item.completed"`,
	}
	for _, l := range yes {
		if !codexCompletedToolStep(l) {
			t.Errorf("expected a tool step: %s", l)
		}
	}
	for _, l := range no {
		if codexCompletedToolStep(l) {
			t.Errorf("did not expect a tool step: %s", l)
		}
	}
}
