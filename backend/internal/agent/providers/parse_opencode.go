package providers

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// classifyOpencodeJSON parses one NDJSON line from opencode run --format json.
//
// Known gap: opencode's `run --format json` NDJSON output (text/tool_use/
// tool_result/step_finish/step_start message types) does not currently
// include a token usage or cost field in any of the shapes we've observed,
// so InputTokens/OutputTokens/CostUSD are left at zero (not estimated) for
// this provider rather than guessing. If a future opencode version adds
// usage reporting to one of these message types, wire it up the same way
// parse_streamjson.go's classifyStreamJSON does for the "result" message.
func classifyOpencodeJSON(line string) (agent.LogEntry, string) {
	var raw struct {
		Type string `json:"type"`
		Part struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Reason string `json:"reason"`
		} `json:"part"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, ""
	}

	switch raw.Type {
	case "text":
		outcome := extractOutcome(raw.Part.Text)
		return agent.LogEntry{Type: agent.LogStdout, Content: raw.Part.Text, At: time.Now()}, outcome
	case "tool_use":
		return agent.LogEntry{Type: agent.LogToolCall, Content: line, At: time.Now()}, ""
	case "tool_result":
		return agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, ""
	case "step_finish":
		// step_finish with reason="stop" means the agent is done
		if raw.Part.Reason == "stop" {
			return agent.LogEntry{Type: agent.LogSystem, Content: "step finished", At: time.Now()}, ""
		}
		return agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("step finished: %s", raw.Part.Reason), At: time.Now()}, ""
	case "step_start":
		return agent.LogEntry{Type: agent.LogSystem, Content: "step started", At: time.Now()}, ""
	default:
		// Log unknown types as raw stdout for debuggability
		text := raw.Part.Text
		if text == "" {
			text = line
		}
		return agent.LogEntry{Type: agent.LogStdout, Content: text, At: time.Now()}, ""
	}
}
