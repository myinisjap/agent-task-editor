package providers

import (
	"encoding/json"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// classifyCodexJSON parses one JSONL event from `codex exec --json`. Codex's
// event schema (confirmed against codex-rs/exec/src/exec_events.rs upstream
// and a live invocation) is:
//
//	{"type":"thread.started","thread_id":"..."}
//	{"type":"turn.started"}
//	{"type":"turn.completed","usage":{"input_tokens":...,"cached_input_tokens":...,"output_tokens":...,"reasoning_output_tokens":...}}
//	{"type":"turn.failed","error":{"message":"..."}}
//	{"type":"item.started","item":{"id":"...","type":"agent_message"|"reasoning"|"command_execution"|"file_change"|"mcp_tool_call"|"web_search"|"todo_list"|"error",...}}
//	{"type":"item.updated","item":{...}}
//	{"type":"item.completed","item":{...}}
//	{"type":"error","message":"..."}
//
// This is a completely different vendor/schema from claude/qwen/gemini's
// stream formats, so it is parsed independently.
//
// Returns the log entry, an optional outcome ("success"/"failure") parsed
// from an OUTCOME marker in the final agent_message item's text (Codex's
// JSON has no separate free-text summary field, so the terminal
// agent_message item.completed is scanned the same way claude/qwen scan
// their "result" message text), token usage (non-nil only for
// "turn.completed", which carries a cost-free token count — no total-cost
// figure is reported by Codex's JSON output, so CostUSD is left at zero, not
// estimated), a failure Classification derived from "turn.failed"/"error"
// events, and the session/thread_id carried on "thread.started".
func classifyCodexJSON(line string) (agent.LogEntry, string, *runUsage, agent.Classification, string) {
	var envelope struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Usage    struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string             `json:"message"`
		Item    *codexItemEnvelope `json:"item"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		// Codex interleaves plain-text diagnostic lines (e.g. Rust `tracing`
		// ERROR logs) with the JSONL event stream on stdout; these aren't
		// events, just raw log noise worth keeping for debuggability.
		return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	}

	switch envelope.Type {
	case "thread.started":
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "", nil, agent.ClassNone, envelope.ThreadID
	case "turn.started":
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	case "turn.completed":
		usage := &runUsage{InputTokens: envelope.Usage.InputTokens, OutputTokens: envelope.Usage.OutputTokens}
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "", usage, agent.ClassNone, ""
	case "turn.failed":
		msg := ""
		if envelope.Error != nil {
			msg = envelope.Error.Message
		}
		class := agent.ClassifyLine(msg)
		if class == agent.ClassNone {
			class = agent.ClassGenuine
		}
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "failure", nil, class, ""
	case "item.started", "item.updated":
		return classifyCodexItem(envelope.Item, line, false)
	case "item.completed":
		return classifyCodexItem(envelope.Item, line, true)
	case "error":
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "", nil, agent.ClassifyLine(envelope.Message), ""
	default:
		return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	}
}

// codexItemEnvelope is the shape of the "item" field on item.started/
// item.updated/item.completed events (see ThreadItem/ThreadItemDetails in
// codex-rs/exec/src/exec_events.rs).
type codexItemEnvelope struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// AgentMessage / Reasoning
	Text string `json:"text"`
	// CommandExecution
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	Status           string `json:"status"`
	// McpToolCall
	Server string `json:"server"`
	Tool   string `json:"tool"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// classifyCodexItem renders a single thread item (agent_message, reasoning,
// command_execution, mcp_tool_call, file_change, web_search, todo_list,
// error) as a LogEntry, mapping tool-shaped items (command_execution,
// mcp_tool_call) to LogToolCall/LogToolResult and everything else to
// LogStdout/LogSystem. completed indicates this is a terminal item.completed
// event (vs. item.started/item.updated, which are progress notifications).
func classifyCodexItem(item *codexItemEnvelope, line string, completed bool) (agent.LogEntry, string, *runUsage, agent.Classification, string) {
	if item == nil {
		return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	}

	switch item.Type {
	case "agent_message":
		outcome := ""
		if completed {
			outcome = extractOutcome(item.Text)
		}
		return agent.LogEntry{Type: agent.LogStdout, Content: item.Text, At: time.Now()}, outcome, nil, agent.ClassNone, ""
	case "reasoning":
		return agent.LogEntry{Type: agent.LogStdout, Content: item.Text, At: time.Now()}, "", nil, agent.ClassNone, ""
	case "command_execution":
		if !completed {
			return agent.LogEntry{Type: agent.LogToolCall, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
		}
		class := agent.ClassNone
		if item.Status == "failed" {
			class = agent.ClassifyLine(item.AggregatedOutput)
		}
		return agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, "", nil, class, ""
	case "mcp_tool_call":
		if !completed {
			return agent.LogEntry{Type: agent.LogToolCall, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
		}
		class := agent.ClassNone
		if item.Status == "failed" && item.Error != nil {
			class = agent.ClassifyLine(item.Error.Message)
		}
		return agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, "", nil, class, ""
	case "file_change", "web_search", "todo_list":
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	case "error":
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "", nil, agent.ClassifyLine(item.Text), ""
	default:
		return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	}
}
