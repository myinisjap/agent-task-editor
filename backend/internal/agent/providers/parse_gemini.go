package providers

import (
	"encoding/json"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// classifyGeminiJSON parses one NDJSON line from `gemini --output-format
// stream-json`. Gemini's event schema (confirmed by reading the installed
// CLI's bundled source, v0.49.0) is:
//
//	{"type":"init","session_id":...,"model":...}
//	{"type":"message","role":"user"|"assistant","content":...,"delta":true}
//	{"type":"tool_use","tool_name":...,"tool_id":...,"parameters":...}
//	{"type":"tool_result","tool_id":...,"status":"success"|"error","output":...,"error":{...}}
//	{"type":"result","status":"success"|"error","stats":{...},"error":{...}}
//
// This is intentionally NOT the same envelope as claude/qwen's stream-json
// (different type names: "message" vs "assistant", "result.status" vs
// "result.subtype"/"is_error"), so it is parsed independently rather than
// reusing classifyStreamJSON.
//
// Returns the log entry, an optional outcome ("success"/"failure") parsed
// from an OUTCOME marker in the final assistant text (Gemini's JSON output
// has no separate free-text summary field to scan, unlike claude/qwen's
// "result" message text), token usage/cost (non-nil only for the terminal
// "result" event, when stats are present), a failure Classification derived
// from the typed terminal "result"/"error" event, and the session_id carried
// on the "init" event (Gemini does not repeat session_id on every event the
// way claude/qwen do).
func classifyGeminiJSON(line string) (agent.LogEntry, string, *runUsage, agent.Classification, string) {
	var envelope struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		Status    string `json:"status"`
		Error     *struct {
			Message string `json:"message"`
		} `json:"error"`
		Stats *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	}

	switch envelope.Type {
	case "init":
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "", nil, agent.ClassNone, envelope.SessionID
	case "message":
		if envelope.Role != "assistant" {
			return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
		}
		return agent.LogEntry{Type: agent.LogStdout, Content: envelope.Content, At: time.Now()}, extractOutcome(envelope.Content), nil, agent.ClassNone, ""
	case "tool_use":
		return agent.LogEntry{Type: agent.LogToolCall, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	case "tool_result":
		class := agent.ClassNone
		if envelope.Status == "error" && envelope.Error != nil {
			class = agent.ClassifyLine(envelope.Error.Message)
		}
		return agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, "", nil, class, ""
	case "error":
		// Non-fatal stream-level error (e.g. a mid-run reconnect notice); still
		// worth classifying since some of these carry the real 429/auth signal.
		msg := ""
		if envelope.Error != nil {
			msg = envelope.Error.Message
		}
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "", nil, agent.ClassifyLine(msg), ""
	case "result":
		var usage *runUsage
		if envelope.Stats != nil {
			usage = &runUsage{InputTokens: envelope.Stats.InputTokens, OutputTokens: envelope.Stats.OutputTokens}
			// Gemini's stream-json "result" event does not report a cost figure
			// (only token counts) — CostUSD is left at zero here rather than
			// estimated. See docs/providers/gemini_cli.md.
		}
		if envelope.Status != "error" {
			return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "success", usage, agent.ClassNone, ""
		}
		class := agent.ClassGenuine
		if envelope.Error != nil {
			if c := agent.ClassifyLine(envelope.Error.Message); c != agent.ClassNone {
				class = c
			}
		}
		return agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()}, "failure", usage, class, ""
	default:
		return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, "", nil, agent.ClassNone, ""
	}
}
