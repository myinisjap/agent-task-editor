package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// --- Subprocess lifecycle tests (generalized from claude_test.go's
// CLAUDE_TEST_HELPER re-exec harness — see TestMain in claude_test.go).
// opencode previously had 0% coverage of Run/binary — only classifyOpencodeJSON
// (parse_opencode_test.go) was tested directly. ---

func opencodeHelperInput(mode string) agent.RunInput {
	return agent.RunInput{
		RunID:       "opencode-test-run",
		Task:        agent.Task{ID: "task-1", Title: "test task"},
		AgentConfig: agent.AgentConfig{Env: map[string]string{"OPENCODE_TEST_HELPER": mode}, TimeoutSecs: 10},
		RepoPath:    os.TempDir(),
	}
}

func TestOpencodeRunner_Binary_DefaultsAndOverrides(t *testing.T) {
	var r OpencodeRunner
	if got := r.binary(); got != "opencode" {
		t.Errorf("binary() = %q, want default %q", got, "opencode")
	}
	r.BinaryPath = "/opt/opencode"
	if got := r.binary(); got != "/opt/opencode" {
		t.Errorf("binary() = %q, want override %q", got, "/opt/opencode")
	}
}

func TestOpencodeRunner_Run_Exit0Success(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), opencodeHelperInput("exit0_success"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", result.Outcome)
	}
	if result.SessionID != "oc-1" {
		t.Errorf("SessionID = %q, want oc-1", result.SessionID)
	}
}

// TestOpencodeRunner_Run_UsageSumsAllStepFinish verifies the per-step-delta
// accumulation fix: when multiple step_finish events are emitted (one per
// step), the final Result's usage/cost must be the *sum* across every
// step_finish, not just the last one — see classifyOpencodeJSON's doc
// comment on why each step_finish's cost/tokens is a per-step delta.
func TestOpencodeRunner_Run_UsageSumsAllStepFinish(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), opencodeHelperInput("exit0_success_with_usage"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.InputTokens != 110 {
		t.Errorf("InputTokens = %d, want 110 (10 + 100, summed across both step_finish events)", result.InputTokens)
	}
	if result.OutputTokens != 220 {
		t.Errorf("OutputTokens = %d, want 220 (20 + 200, summed across both step_finish events)", result.OutputTokens)
	}
	if diff := result.CostUSD - 0.06; diff < -1e-9 || diff > 1e-9 {
		t.Errorf("CostUSD = %v, want ~0.06 (0.01 + 0.05, summed across both step_finish events)", result.CostUSD)
	}
}

// TestOpencodeRunner_Run_SystemPromptReachesConfigContentEnv verifies the
// fix for the system prompt never being sent to opencode: Run must deliver
// buildSystemPrompt(input) — which carries the OUTCOME: success/failure
// instruction, opencode's only way to report an outcome since it has no
// MCP sidecar — to the CLI via OPENCODE_CONFIG_CONTENT. The fake binary
// (OPENCODE_TEST_HELPER=assert_outcome_in_config) fails unless it finds
// that instruction in its own OPENCODE_CONFIG_CONTENT env var.
func TestOpencodeRunner_Run_SystemPromptReachesConfigContentEnv(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	logs := make([]agent.LogEntry, 0)
	done := make(chan struct{})
	go func() {
		for e := range logCh {
			logs = append(logs, e)
		}
		close(done)
	}()
	result, err := runner.Run(context.Background(), opencodeHelperInput("assert_outcome_in_config"), logCh)
	close(logCh)
	<-done
	if err != nil {
		t.Fatalf("Run: unexpected error: %v (logs: %v)", err, logs)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
	if result.Outcome != "success" {
		t.Errorf("Outcome = %q, want success", result.Outcome)
	}
}

// TestOpencodeConfigContent_CarriesSystemPromptOnBuildAgent verifies the
// OPENCODE_CONFIG_CONTENT JSON shape directly: the system prompt must land
// on the "build" agent's "prompt" field (the agent opencode run uses by
// default), since opencode run has no --system-prompt flag of its own.
func TestOpencodeConfigContent_CarriesSystemPromptOnBuildAgent(t *testing.T) {
	content, err := opencodeConfigContent("be careful.\n\nOUTCOME: success", 50, nil, "", nil, nil)
	if err != nil {
		t.Fatalf("opencodeConfigContent: unexpected error: %v", err)
	}
	var parsed struct {
		Agent map[string]struct {
			Prompt string `json:"prompt"`
			Steps  int64  `json:"steps"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not valid JSON: %v\ncontent: %s", err, content)
	}
	build, ok := parsed.Agent["build"]
	if !ok {
		t.Fatalf("want an agent.build entry, got agents: %v", parsed.Agent)
	}
	if build.Prompt != "be careful.\n\nOUTCOME: success" {
		t.Errorf("agent.build.prompt = %q, want the full system prompt", build.Prompt)
	}
	if build.Steps != 50 {
		t.Errorf("agent.build.steps = %d, want 50", build.Steps)
	}
}

// TestOpencodeConfigContent_OmitsMCPWhenUnconfigured verifies the no-MCP
// behavior (r.MCP == nil, or ServerBinary == "") is unchanged: no "mcp" key
// at all in OPENCODE_CONFIG_CONTENT.
func TestOpencodeConfigContent_OmitsMCPWhenUnconfigured(t *testing.T) {
	content, err := opencodeConfigContent("prompt", 50, nil, "", nil, nil)
	if err != nil {
		t.Fatalf("opencodeConfigContent: unexpected error: %v", err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := parsed["mcp"]; ok {
		t.Errorf("want no \"mcp\" key when mcpCfg is nil, got: %s", content)
	}
}

// TestOpencodeConfigContent_IncludesMCPServer verifies the inline mcp block
// is built from MCPRunConfig.Env (the same env map MCPManager.Prepare uses
// for the file-based providers), carrying the sidecar's scoped token/
// RESULT_FILE handoff per ADR 0004.
func TestOpencodeConfigContent_IncludesMCPServer(t *testing.T) {
	mcpCfg := &MCPRunConfig{
		Env: map[string]string{
			"RUN_ID":           "run-1",
			"RESULT_FILE":      "/tmp/result.json",
			"TRANSITIONS":      "[]",
			"REVIEW_COMMENTS":  "[]",
			"SUBTASKS_ENABLED": "1",
			"BACKEND_URL":      "http://localhost:8080",
			"TASK_ID":          "task-1",
			"API_TOKEN":        "scoped-token",
			"MAX_SUBTASKS":     "3",
		},
	}
	content, err := opencodeConfigContent("prompt", 50, mcpCfg, "/opt/mcp-server", nil, nil)
	if err != nil {
		t.Fatalf("opencodeConfigContent: unexpected error: %v", err)
	}
	var parsed struct {
		MCP map[string]struct {
			Type        string            `json:"type"`
			Command     []string          `json:"command"`
			Enabled     bool              `json:"enabled"`
			Environment map[string]string `json:"environment"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\ncontent: %s", err, content)
	}
	te, ok := parsed.MCP["task-editor"]
	if !ok {
		t.Fatalf("want an mcp.task-editor entry, got: %v", parsed.MCP)
	}
	if te.Type != "local" {
		t.Errorf("mcp.task-editor.type = %q, want local", te.Type)
	}
	if len(te.Command) == 0 || te.Command[0] != "/opt/mcp-server" {
		t.Errorf("mcp.task-editor.command = %v, want [/opt/mcp-server]", te.Command)
	}
	if !te.Enabled {
		t.Error("mcp.task-editor.enabled = false, want true")
	}
	for _, key := range []string{"RUN_ID", "RESULT_FILE", "TRANSITIONS", "REVIEW_COMMENTS", "SUBTASKS_ENABLED", "BACKEND_URL", "TASK_ID", "API_TOKEN", "MAX_SUBTASKS"} {
		if te.Environment[key] == "" {
			t.Errorf("mcp.task-editor.environment[%q] missing/empty, got: %v", key, te.Environment)
		}
	}
}

// TestOpencodeConfigContent_PermissionPolicy verifies command_denylist maps
// to "deny" and command_allowlist maps to "allow" plus a catch-all "*":
// "deny" default, with denylist winning on an overlapping pattern, and
// empty-string patterns skipped.
func TestOpencodeConfigContent_PermissionPolicy(t *testing.T) {
	t.Run("denylist only", func(t *testing.T) {
		content, err := opencodeConfigContent("p", 50, nil, "", nil, []string{"rm -rf *", ""})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed struct {
			Permission struct {
				Bash map[string]string `json:"bash"`
			} `json:"permission"`
		}
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			t.Fatalf("not valid JSON: %v", err)
		}
		if parsed.Permission.Bash["rm -rf *"] != "deny" {
			t.Errorf("permission.bash[rm -rf *] = %q, want deny", parsed.Permission.Bash["rm -rf *"])
		}
		if _, ok := parsed.Permission.Bash[""]; ok {
			t.Error("empty-string pattern should be skipped")
		}
		if _, ok := parsed.Permission.Bash["*"]; ok {
			t.Error("denylist-only should not add a catch-all \"*\" entry")
		}
	})

	t.Run("allowlist adds default deny", func(t *testing.T) {
		content, err := opencodeConfigContent("p", 50, nil, "", []string{"git status", ""}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed struct {
			Permission struct {
				Bash map[string]string `json:"bash"`
			} `json:"permission"`
		}
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			t.Fatalf("not valid JSON: %v", err)
		}
		if parsed.Permission.Bash["git status"] != "allow" {
			t.Errorf("permission.bash[git status] = %q, want allow", parsed.Permission.Bash["git status"])
		}
		if parsed.Permission.Bash["*"] != "deny" {
			t.Errorf("permission.bash[*] = %q, want deny (default)", parsed.Permission.Bash["*"])
		}
	})

	t.Run("denylist wins on overlap", func(t *testing.T) {
		content, err := opencodeConfigContent("p", 50, nil, "", []string{"rm -rf *"}, []string{"rm -rf *"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed struct {
			Permission struct {
				Bash map[string]string `json:"bash"`
			} `json:"permission"`
		}
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			t.Fatalf("not valid JSON: %v", err)
		}
		if parsed.Permission.Bash["rm -rf *"] != "deny" {
			t.Errorf("permission.bash[rm -rf *] = %q, want deny (denylist wins)", parsed.Permission.Bash["rm -rf *"])
		}
	})

	t.Run("no permission block when both empty", func(t *testing.T) {
		content, err := opencodeConfigContent("p", 50, nil, "", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			t.Fatalf("not valid JSON: %v", err)
		}
		if _, ok := parsed["permission"]; ok {
			t.Errorf("want no \"permission\" key when allowlist/denylist are both empty, got: %s", content)
		}
	})
}

func TestOpencodeRunner_Run_Exit1NoOutputIsFailed(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	result, err := runner.Run(context.Background(), opencodeHelperInput("exit1"), logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestOpencodeRunner_Run_TimeoutKillsProcess verifies the context-cancel/
// timeout branch (shared by every CLI provider) actually kills a hung
// process and returns *agent.ErrTransient instead of hanging.
func TestOpencodeRunner_Run_TimeoutKillsProcess(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := opencodeHelperInput("")
	input.AgentConfig.Env["HANG_TEST_HELPER"] = "30"
	input.AgentConfig.TimeoutSecs = 1

	start := time.Now()
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Fatalf("Run took %s, want the 1s timeout to kill the process quickly", elapsed)
	}
	var te *agent.ErrTransient
	if !errors.As(err, &te) {
		t.Fatalf("want *agent.ErrTransient, got %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestOpencodeRunner_Run_ModelAndResumeFlags is a subprocess smoke test for
// a Model/ResumeSessionID-bearing input: the actual arg construction is now
// covered directly (and much more precisely) by the buildOpencodeArgs unit
// tests below, which don't need a subprocess at all. This test remains as a
// regression guard that such an input doesn't panic or otherwise break the
// fake-binary run end-to-end.
func TestOpencodeRunner_Run_ModelAndResumeFlags(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)
	go func() {
		for range logCh {
		}
	}()
	input := opencodeHelperInput("exit0_success")
	input.AgentConfig.Model = "some-model"
	input.ResumeSessionID = "sess-99"
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	if err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
}

// TestOpencodeRunner_Run_OversizedLineTruncation mirrors
// TestClaude_OversizedLine_Truncation in claude_test.go — proves the shared
// scanLines helper (scan.go) fixes the silent-truncation bug for opencode
// too: a stdout line exceeding the scan buffer cap must surface a visible
// LogSystem warning and end the run with a non-"completed" status, promptly
// (not wedged until the outer timeout).
func TestOpencodeRunner_Run_OversizedLineTruncation(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	logCh := make(chan agent.LogEntry, 256)

	type outcome struct {
		r   agent.Result
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		r, err := runner.Run(context.Background(), opencodeHelperInput("oversized_line"), logCh)
		close(logCh)
		ch <- outcome{r, err}
	}()
	logs := drainLogs(logCh)

	var res outcome
	select {
	case res = <-ch:
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for Run to return — oversized line likely wedged the run instead of failing promptly")
	}

	if res.r.Status == "completed" {
		t.Errorf("want Status != completed (output was truncated), got %q", res.r.Status)
	}
	if res.err == nil {
		t.Error("want a non-nil error signalling the truncated run, got nil")
	}

	var sawTruncationLog bool
	for _, e := range logs {
		if e.Type == agent.LogSystem && (contains(e.Content, "truncated") || contains(e.Content, "scan limit")) {
			sawTruncationLog = true
		}
	}
	if !sawTruncationLog {
		t.Errorf("expected a LogSystem entry warning about the truncated output stream, got logs: %v", logs)
	}
}

// TestOpencodeRunner_Run_MaxTurnsStopsRun verifies the runner-side
// step_finish counter backstop: once stepCount exceeds AgentConfig.MaxTurns
// the process is stopped and *agent.ErrMaxTurns is returned with the step
// count reported as turns used, well under the run timeout (mirrors
// TestCodexRunner_Run_MaxTurnsStopsRun).
func TestOpencodeRunner_Run_MaxTurnsStopsRun(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	input := opencodeHelperInput("max_turns")
	input.AgentConfig.MaxTurns = 2
	input.AgentConfig.TimeoutSecs = 20
	logCh := make(chan agent.LogEntry, 256)
	wait := collectLogs(logCh)
	start := time.Now()
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	wait()

	var mt *agent.ErrMaxTurns
	if !errors.As(err, &mt) || mt.MaxTurns != 2 {
		t.Fatalf("err = %v, want *agent.ErrMaxTurns{2}", err)
	}
	if result.TurnsUsed < 3 {
		t.Errorf("TurnsUsed = %d, want >= 3 (stdout is buffered; all step_finish lines the helper printed before sleeping may be scanned before stopProc takes effect)", result.TurnsUsed)
	}
	if result.SessionID != "oc-mt" {
		t.Errorf("SessionID = %q, want oc-mt", result.SessionID)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("run took %v; the cap should stop it immediately", elapsed)
	}
}

// TestOpencodeRunner_Run_MaxStepsMessageDetected verifies the config-side
// detector: opencode's own "maximum steps for this agent have been
// reached" text (even on a clean exit 0) must also escalate to
// *agent.ErrMaxTurns, independent of the runner-side step_finish counter.
func TestOpencodeRunner_Run_MaxStepsMessageDetected(t *testing.T) {
	runner := &OpencodeRunner{BinaryPath: os.Args[0]}
	input := opencodeHelperInput("max_steps_message")
	input.AgentConfig.MaxTurns = 50
	logCh := make(chan agent.LogEntry, 256)
	wait := collectLogs(logCh)
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	wait()

	var mt *agent.ErrMaxTurns
	if !errors.As(err, &mt) {
		t.Fatalf("err = %v, want *agent.ErrMaxTurns", err)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want failed", result.Status)
	}
}

// TestOpencodeRunner_Run_WithMCP_PassesInlineConfig verifies that when an
// MCPManager is configured, Run prepares the sidecar and delivers its env
// via OPENCODE_CONFIG_CONTENT's inline "mcp" block (there's no --mcp-config
// flag for opencode, unlike claude/qwen).
func TestOpencodeRunner_Run_WithMCP_PassesInlineConfig(t *testing.T) {
	runner := &OpencodeRunner{
		BinaryPath: os.Args[0],
		MCP:        &MCPManager{ServerBinary: "/opt/mcp-server"},
	}
	input := opencodeHelperInput("assert_mcp_config")
	logCh := make(chan agent.LogEntry, 256)
	logs := make([]agent.LogEntry, 0)
	done := make(chan struct{})
	go func() {
		for e := range logCh {
			logs = append(logs, e)
		}
		close(done)
	}()
	result, err := runner.Run(context.Background(), input, logCh)
	close(logCh)
	<-done
	if err != nil {
		t.Fatalf("Run: unexpected error: %v (logs: %v)", err, logs)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want completed", result.Status)
	}
}

// --- buildOpencodeArgs unit tests (no subprocess needed) ---

// TestBuildOpencodeArgs_BaseFlagsAndSeparator verifies the always-present
// `run --format json` flags, that "--" is present, and that the prompt is
// the very last argument — the guard against -f (a variadic/array-type
// yargs option) swallowing the prompt as an extra file.
func TestBuildOpencodeArgs_BaseFlagsAndSeparator(t *testing.T) {
	input := agent.RunInput{
		Task:        agent.Task{Title: "t"},
		AgentConfig: agent.AgentConfig{},
	}
	args := buildOpencodeArgs(input)
	if len(args) < 3 || args[0] != "run" || args[1] != "--format" || args[2] != "json" {
		t.Fatalf("expected leading `run --format json`, got %v", args)
	}
	sepIdx := slices.Index(args, "--")
	if sepIdx < 0 {
		t.Fatalf("expected a -- separator before the prompt, args=%v", args)
	}
	if args[len(args)-1] != buildPrompt(input) {
		t.Fatalf("expected the prompt as the final argument, got %v", args)
	}
	if sepIdx != len(args)-2 {
		t.Fatalf("expected -- to immediately precede the prompt, args=%v", args)
	}
}

// TestBuildOpencodeArgs_ModelAndResume verifies -m/--session are present
// when set and absent when unset.
func TestBuildOpencodeArgs_ModelAndResume(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		args := buildOpencodeArgs(agent.RunInput{
			Task:            agent.Task{Title: "t"},
			AgentConfig:     agent.AgentConfig{Model: "some-model"},
			ResumeSessionID: "sess-99",
		})
		if got := findFlagValue(args, "-m"); got != "some-model" {
			t.Fatalf("expected -m some-model, got %q (args=%v)", got, args)
		}
		if got := findFlagValue(args, "--session"); got != "sess-99" {
			t.Fatalf("expected --session sess-99, got %q (args=%v)", got, args)
		}
	})
	t.Run("unset", func(t *testing.T) {
		args := buildOpencodeArgs(agent.RunInput{
			Task:        agent.Task{Title: "t"},
			AgentConfig: agent.AgentConfig{},
		})
		if containsArg(args, "-m") {
			t.Fatalf("did not expect -m flag when Model is unset, args=%v", args)
		}
		if containsArg(args, "--session") {
			t.Fatalf("did not expect --session flag when ResumeSessionID is unset, args=%v", args)
		}
	})
}

// TestBuildOpencodeArgs_Variant verifies AgentConfig.Effort maps to
// --variant unchanged (pass-through, no clamping — see effort.go), and is
// omitted when unset or unrecognized.
func TestBuildOpencodeArgs_Variant(t *testing.T) {
	t.Run("unset omits --variant", func(t *testing.T) {
		args := buildOpencodeArgs(agent.RunInput{
			Task:        agent.Task{Title: "t"},
			AgentConfig: agent.AgentConfig{},
		})
		if containsArg(args, "--variant") {
			t.Fatalf("did not expect --variant flag when Effort is unset, args=%v", args)
		}
	})

	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		t.Run(level, func(t *testing.T) {
			args := buildOpencodeArgs(agent.RunInput{
				Task:        agent.Task{Title: "t"},
				AgentConfig: agent.AgentConfig{Effort: level},
			})
			if got := findFlagValue(args, "--variant"); got != level {
				t.Fatalf("expected --variant %s, got %q (args=%v)", level, got, args)
			}
		})
	}

	t.Run("unrecognized value omits --variant", func(t *testing.T) {
		args := buildOpencodeArgs(agent.RunInput{
			Task:        agent.Task{Title: "t"},
			AgentConfig: agent.AgentConfig{Effort: "bogus"},
		})
		if containsArg(args, "--variant") {
			t.Fatalf("did not expect --variant flag for an unrecognized Effort value, args=%v", args)
		}
	})
}

// TestBuildOpencodeArgs_Files verifies task attachments copied into the
// worktree's .task_attachments/ are passed as one -f <path> per file,
// de-duplicated, missing copies skipped, and that the prompt stays the
// final argument (not swallowed as a file by the variadic -f).
func TestBuildOpencodeArgs_Files(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".task_attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.png", "b.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, ".task_attachments", f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	input := agent.RunInput{
		Task: agent.Task{
			Title:       "t",
			Attachments: []string{"task1/a.png", "task1/missing.png", "task1/b.jpg", "task1/a.png"},
		},
		AgentConfig: agent.AgentConfig{},
		RepoPath:    dir,
	}
	args := buildOpencodeArgs(input)

	var files []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-f" && i+1 < len(args) {
			files = append(files, args[i+1])
		}
	}
	want := []string{".task_attachments/a.png", ".task_attachments/b.jpg"}
	if !slices.Equal(files, want) {
		t.Fatalf("file args = %v, want %v (all args %v)", files, want, args)
	}
	if args[len(args)-1] != buildPrompt(input) {
		t.Fatalf("expected prompt as final arg (not swallowed by -f), args=%v", args)
	}
	if strings.HasPrefix(args[len(args)-1], ".task_attachments") {
		t.Fatalf("expected prompt as final arg, args=%v", args)
	}
}

// TestBuildOpencodeArgs_NoAttachmentsNoFileFlag verifies no -f flag is
// added when there are no attachments.
func TestBuildOpencodeArgs_NoAttachmentsNoFileFlag(t *testing.T) {
	args := buildOpencodeArgs(agent.RunInput{Task: agent.Task{Title: "t"}, RepoPath: t.TempDir()})
	if containsArg(args, "-f") {
		t.Fatalf("did not expect a -f arg without attachments, args=%v", args)
	}
}
