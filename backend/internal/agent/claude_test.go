package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain supports the subprocess helper pattern: when the test binary is
// re-invoked with CLAUDE_TEST_HELPER=1, it acts as a fake "claude" binary
// instead of running tests.
func TestMain(m *testing.M) {
	switch os.Getenv("CLAUDE_TEST_HELPER") {
	case "exit1":
		// Simulate: claude exits with code 1 (auth error, crash, etc.).
		// Emit a stream-json result line that looks like a success so we can
		// verify the exit code still wins over the parsed outcome.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success"}`)
		os.Exit(1)
	case "exit1_no_output":
		// Simulate: claude exits with code 1 and no useful output.
		os.Exit(1)
	case "exit0_success":
		// Simulate: claude exits cleanly with a success outcome.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success"}`)
		os.Exit(0)
	case "exit0_no_outcome":
		// Simulate: claude exits cleanly with no outcome (empty result).
		os.Exit(0)
	case "exit0_success_with_usage":
		// Simulate: claude exits cleanly with a success outcome and a
		// result message carrying usage/total_cost_usd, as the real CLI
		// does — total_cost_usd here is authoritative and should be used
		// as-is rather than re-estimated.
		fmt.Println(`{"type":"result","subtype":"success","result":"OUTCOME: success","usage":{"input_tokens":111,"output_tokens":222},"total_cost_usd":0.05}`)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// helperRunner returns a ClaudeRunner whose binary re-invokes the current test
// binary as the given helper mode.
func helperRunner(mode string) *ClaudeRunner {
	return &ClaudeRunner{
		BinaryPath: os.Args[0], // re-invoke the test binary itself
	}
}

// makeInput builds a minimal RunInput sufficient for ClaudeRunner.Run.
func makeInput(mode string) RunInput {
	return RunInput{
		RunID: "test-run",
		Task:  Task{ID: "task-1", Title: "test task"},
		AgentConfig: AgentConfig{
			// Pass the mode via Env so the test binary knows which helper to be.
			Env:         map[string]string{"CLAUDE_TEST_HELPER": mode},
			TimeoutSecs: 10,
		},
		RepoPath: os.TempDir(),
	}
}

func drainLogs(logCh <-chan LogEntry) []LogEntry {
	var entries []LogEntry
	for e := range logCh {
		entries = append(entries, e)
	}
	return entries
}

func runWithHelper(t *testing.T, mode string) (Result, []LogEntry) {
	t.Helper()
	runner := helperRunner(mode)
	logCh := make(chan LogEntry, 256)

	// Run in a goroutine so we can drain logs concurrently.
	type outcome struct {
		r   Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), makeInput(mode), logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()

	logs := drainLogs(logCh)
	res := <-ch
	if res.err != nil {
		t.Fatalf("Run returned unexpected error: %v", res.err)
	}
	return res.r, logs
}

// TestClaudeExitCode1_IsFailed verifies that a non-zero exit from the claude
// binary is always treated as failure, even when the stream output contained a
// success outcome.
func TestClaudeExitCode1_IsFailed(t *testing.T) {
	result, logs := runWithHelper(t, "exit1")

	if result.Status != "failed" {
		t.Errorf("want Status=%q, got %q", "failed", result.Status)
	}
	// Log a warning if outcome was discarded — verify we emitted the discrepancy log.
	var found bool
	for _, e := range logs {
		if e.Type == LogSystem {
			if contains(e.Content, "treating as failed") {
				found = true
			}
		}
	}
	if !found {
		t.Logf("(optional) expected a 'treating as failed' log entry; logs: %v", logContents(logs))
	}
}

// TestClaudeExitCode1_NoOutput_IsFailed verifies exit-code-1 with no output
// is treated as failure.
func TestClaudeExitCode1_NoOutput_IsFailed(t *testing.T) {
	result, _ := runWithHelper(t, "exit1_no_output")
	if result.Status != "failed" {
		t.Errorf("want Status=%q, got %q", "failed", result.Status)
	}
}

// TestClaudeExitCode0_Success verifies normal success path still works.
func TestClaudeExitCode0_Success(t *testing.T) {
	result, _ := runWithHelper(t, "exit0_success")
	if result.Status != "completed" {
		t.Errorf("want Status=%q, got %q", "completed", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("want Outcome=%q, got %q", "success", result.Outcome)
	}
}

// TestClaudeExitCode0_NoOutcome verifies exit-0 with no outcome returns
// completed with empty outcome (not failed).
func TestClaudeExitCode0_NoOutcome(t *testing.T) {
	result, _ := runWithHelper(t, "exit0_no_outcome")
	if result.Status != "completed" {
		t.Errorf("want Status=%q, got %q", "completed", result.Status)
	}
}

// TestClassifyStreamJSON_ResultUsage verifies that a "result" message
// containing both a usage object and total_cost_usd is parsed into a
// runUsage with the expected fields, and that the outcome is still parsed
// correctly from the accompanying OUTCOME marker.
func TestClassifyStreamJSON_ResultUsage(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"OUTCOME: success","usage":{"input_tokens":123,"output_tokens":456,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"total_cost_usd":0.0789,"duration_ms":1500}`

	entry, outcome, usage, _ := classifyStreamJSON(line)

	if entry.Type != LogSystem {
		t.Errorf("want LogSystem entry, got %v", entry.Type)
	}
	if outcome != "success" {
		t.Errorf("want outcome=success, got %q", outcome)
	}
	if usage == nil {
		t.Fatalf("want non-nil usage, got nil")
	}
	if usage.InputTokens != 123 {
		t.Errorf("want InputTokens=123, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 456 {
		t.Errorf("want OutputTokens=456, got %d", usage.OutputTokens)
	}
	if usage.CostUSD != 0.0789 {
		t.Errorf("want CostUSD=0.0789, got %v", usage.CostUSD)
	}
}

// TestClassifyStreamJSON_ResultNoUsage verifies a "result" message with no
// usage/total_cost_usd fields (e.g. an older CLI version) returns a nil
// usage rather than a zero-valued struct, so callers don't overwrite a
// previously-seen usage with zeros.
func TestClassifyStreamJSON_ResultNoUsage(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"OUTCOME: success"}`

	_, outcome, usage, _ := classifyStreamJSON(line)

	if outcome != "success" {
		t.Errorf("want outcome=success, got %q", outcome)
	}
	if usage != nil {
		t.Errorf("want nil usage, got %+v", usage)
	}
}

// TestClassifyStreamJSON_NonResultMessagesReturnNilUsage verifies that
// non-"result" message types never populate usage.
func TestClassifyStreamJSON_NonResultMessagesReturnNilUsage(t *testing.T) {
	for _, line := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"tool_use"}`,
		`{"type":"tool_result"}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result"}]}}`,
	} {
		_, _, usage, _ := classifyStreamJSON(line)
		if usage != nil {
			t.Errorf("line %q: want nil usage, got %+v", line, usage)
		}
	}
}

// TestClaudeRunner_PropagatesUsageFromResultMessage verifies that a full
// Run() invocation propagates the token usage and CLI-authoritative
// total_cost_usd parsed from the stream-json "result" message onto the
// returned Result (the non-MCP code path).
func TestClaudeRunner_PropagatesUsageFromResultMessage(t *testing.T) {
	result, _ := runWithHelper(t, "exit0_success_with_usage")
	if result.Status != "completed" {
		t.Fatalf("want Status=completed, got %q", result.Status)
	}
	if result.InputTokens != 111 {
		t.Errorf("want InputTokens=111, got %d", result.InputTokens)
	}
	if result.OutputTokens != 222 {
		t.Errorf("want OutputTokens=222, got %d", result.OutputTokens)
	}
	if result.CostUSD != 0.05 {
		t.Errorf("want CostUSD=0.05, got %v", result.CostUSD)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func logContents(logs []LogEntry) []string {
	out := make([]string, len(logs))
	for i, l := range logs {
		out[i] = fmt.Sprintf("[%s] %s @ %s", l.Type, l.Content, l.At.Format(time.RFC3339))
	}
	return out
}

// TestBuildPrompt_FeedbackInjected verifies a human rejection note (carried as
// RunInput.Feedback) is rendered at the top of the agent prompt — the read side
// of the reject-feedback round-trip.
func TestBuildPrompt_FeedbackInjected(t *testing.T) {
	fb := "needs more tests"
	out := buildPrompt(RunInput{
		Task:     Task{Title: "Do the thing"},
		Feedback: &fb,
	})
	if !strings.HasPrefix(out, "FEEDBACK FROM PRIOR REVIEW:\n"+fb) {
		t.Fatalf("feedback not at top of prompt; got:\n%s", out)
	}
}

// TestBuildPrompt_OpenReviewCommentsInjected verifies that open inline diff
// review comments are rendered into the prompt with their comment_id, file
// and line anchors, quoted diff text, and the resolve_comment instruction.
func TestBuildPrompt_OpenReviewCommentsInjected(t *testing.T) {
	out := buildPrompt(RunInput{
		Task: Task{Title: "Do the thing"},
		OpenReviewComments: []ReviewComment{
			{ID: "c-1", FilePath: "main.go", Side: "new", StartLine: 10, EndLine: 12, QuotedText: "x := 1", Body: "use the existing helper"},
			{ID: "c-2", FilePath: "util.go", Side: "new", StartLine: 5, EndLine: 5, Body: "typo in comment"},
		},
	})
	for _, want := range []string{
		"OPEN REVIEW COMMENTS",
		"[comment_id: c-1] main.go (lines 10-12)",
		"x := 1",
		"→ use the existing helper",
		"[comment_id: c-2] util.go (line 5)",
		"mcp__task-editor__resolve_comment",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q; got:\n%s", want, out)
		}
	}
}

// TestBuildPrompt_NoReviewComments verifies the section is absent when there
// are no open comments.
func TestBuildPrompt_NoReviewComments(t *testing.T) {
	out := buildPrompt(RunInput{Task: Task{Title: "Do the thing"}})
	if strings.Contains(out, "OPEN REVIEW COMMENTS") {
		t.Fatalf("unexpected review comments section in prompt:\n%s", out)
	}
}

// findFlagValue returns the argument immediately following the given flag
// name in args, or "" if not found.
func findFlagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestBuildClaudeArgs_MaxTurnsDefault verifies that when AgentConfig.MaxTurns
// is unset (zero), the constructed args default --max-turns to 50 (today's
// previously-hardcoded behavior).
func TestBuildClaudeArgs_MaxTurnsDefault(t *testing.T) {
	args, err := buildClaudeArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{},
	}, false, nil)
	if err != nil {
		t.Fatalf("buildClaudeArgs: %v", err)
	}
	if got := findFlagValue(args, "--max-turns"); got != "50" {
		t.Fatalf("expected default --max-turns 50, got %q (args=%v)", got, args)
	}
}

// TestBuildClaudeArgs_MaxTurnsConfigured verifies that a custom
// AgentConfig.MaxTurns value is passed through to the --max-turns flag
// instead of the hardcoded default.
func TestBuildClaudeArgs_MaxTurnsConfigured(t *testing.T) {
	args, err := buildClaudeArgs(RunInput{
		Task:        Task{Title: "t"},
		AgentConfig: AgentConfig{MaxTurns: 12},
	}, false, nil)
	if err != nil {
		t.Fatalf("buildClaudeArgs: %v", err)
	}
	if got := findFlagValue(args, "--max-turns"); got != "12" {
		t.Fatalf("expected --max-turns 12, got %q (args=%v)", got, args)
	}
}

// TestBuildClaudeSettingsJSON_FallbackNoInventory verifies that a selected
// plugin is explicitly enabled even when it isn't present in the discovered
// inventory (or discovery finds nothing at all). HOME is pointed at an empty
// temp dir so this is deterministic regardless of the host's real
// ~/.claude/plugins/installed_plugins.json contents.
func TestBuildClaudeSettingsJSON_FallbackNoInventory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := buildClaudeSettingsJSON([]string{"some-plugin@marketplace"}, nil, nil)
	if err != nil {
		t.Fatalf("buildClaudeSettingsJSON: %v", err)
	}
	var parsed struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("unmarshal settings json: %v", err)
	}
	if !parsed.EnabledPlugins["some-plugin@marketplace"] {
		t.Fatalf("want selected plugin enabled, got %+v", parsed.EnabledPlugins)
	}
}

func TestBuildClaudeSettingsJSON_NoSelection_EmptyMap(t *testing.T) {
	// Isolate from the real user's ~/.claude/plugins/installed_plugins.json:
	// point HOME at an empty temp dir so plugin discovery finds nothing and
	// the fallback (empty map) path is exercised deterministically.
	t.Setenv("HOME", t.TempDir())
	got, err := buildClaudeSettingsJSON(nil, nil, nil)
	if err != nil {
		t.Fatalf("buildClaudeSettingsJSON: %v", err)
	}
	var parsed struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("unmarshal settings json: %v", err)
	}
	if len(parsed.EnabledPlugins) != 0 {
		t.Fatalf("want empty enabledPlugins map, got %+v", parsed.EnabledPlugins)
	}
}

// TestBuildClaudeSettingsJSON_CommandPermissions verifies that non-empty
// command allow/deny lists are translated into Bash(pattern) entries under
// the "permissions" key of the settings JSON, and that an empty pair of
// lists produces no "permissions" key at all (backward compatible).
func TestBuildClaudeSettingsJSON_CommandPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("both lists populated", func(t *testing.T) {
		got, err := buildClaudeSettingsJSON(nil, []string{"git *", "npm test"}, []string{"rm -rf *"})
		if err != nil {
			t.Fatalf("buildClaudeSettingsJSON: %v", err)
		}
		var parsed struct {
			Permissions struct {
				Allow []string `json:"allow"`
				Deny  []string `json:"deny"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("unmarshal settings json: %v", err)
		}
		wantAllow := []string{"Bash(git *)", "Bash(npm test)"}
		if len(parsed.Permissions.Allow) != len(wantAllow) {
			t.Fatalf("allow = %+v, want %+v", parsed.Permissions.Allow, wantAllow)
		}
		for i, w := range wantAllow {
			if parsed.Permissions.Allow[i] != w {
				t.Fatalf("allow[%d] = %q, want %q", i, parsed.Permissions.Allow[i], w)
			}
		}
		wantDeny := []string{"Bash(rm -rf *)"}
		if len(parsed.Permissions.Deny) != len(wantDeny) || parsed.Permissions.Deny[0] != wantDeny[0] {
			t.Fatalf("deny = %+v, want %+v", parsed.Permissions.Deny, wantDeny)
		}
	})

	t.Run("denylist only", func(t *testing.T) {
		got, err := buildClaudeSettingsJSON(nil, nil, []string{"sudo *"})
		if err != nil {
			t.Fatalf("buildClaudeSettingsJSON: %v", err)
		}
		var parsed struct {
			Permissions struct {
				Allow []string `json:"allow"`
				Deny  []string `json:"deny"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("unmarshal settings json: %v", err)
		}
		if len(parsed.Permissions.Allow) != 0 {
			t.Fatalf("expected no allow entries, got %+v", parsed.Permissions.Allow)
		}
		if len(parsed.Permissions.Deny) != 1 || parsed.Permissions.Deny[0] != "Bash(sudo *)" {
			t.Fatalf("deny = %+v, want [Bash(sudo *)]", parsed.Permissions.Deny)
		}
	})

	t.Run("empty lists omit permissions key entirely", func(t *testing.T) {
		got, err := buildClaudeSettingsJSON(nil, nil, nil)
		if err != nil {
			t.Fatalf("buildClaudeSettingsJSON: %v", err)
		}
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("unmarshal settings json: %v", err)
		}
		if _, ok := parsed["permissions"]; ok {
			t.Fatalf("expected no permissions key when both lists are empty, got %s", got)
		}
	})
}

// TestBuildClaudeArgs_CommandPermissions verifies buildClaudeArgs threads the
// agent config's command allow/deny lists through into the --settings JSON
// payload passed to the claude CLI.
func TestBuildClaudeArgs_CommandPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	args, err := buildClaudeArgs(RunInput{
		Task: Task{Title: "t"},
		AgentConfig: AgentConfig{
			CommandAllowlist: []string{"git *"},
			CommandDenylist:  []string{"rm -rf *"},
		},
	}, false, nil)
	if err != nil {
		t.Fatalf("buildClaudeArgs: %v", err)
	}
	settingsJSON := findFlagValue(args, "--settings")
	if settingsJSON == "" {
		t.Fatalf("expected --settings flag in args: %v", args)
	}
	if !strings.Contains(settingsJSON, `"Bash(git *)"`) {
		t.Fatalf("expected allow entry in settings JSON, got %s", settingsJSON)
	}
	if !strings.Contains(settingsJSON, `"Bash(rm -rf *)"`) {
		t.Fatalf("expected deny entry in settings JSON, got %s", settingsJSON)
	}
}
