package providers

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// classifyOpencodeJSON parses one NDJSON line from opencode run --format json.
//
// Usage/cost: the "step_finish" event's part carries both a "cost" (float,
// USD) and a "tokens" object ({input,output,reasoning,cache:{read,write}})
// (verified against opencode-ai v1.18.6). Only input/output/cost are read
// here — see extractOpencodeUsage — mirroring extractResultUsage's
// nil-if-neither-present guard in parse_streamjson.go.
//
// step_finish fires once per *step*, not once per run, so there may be
// several per run. opencode's own SQLite "session" table stores a single
// cumulative cost/tokens_input/tokens_output row per session (not a
// per-step delta), which strongly suggests the values reported on each
// step_finish event are themselves cumulative-to-date rather than
// per-step. Based on that evidence, the caller (OpencodeRunner.Run)
// *takes the last* step_finish's usage rather than summing across steps.
// This assumption has not been independently verified against a real
// multi-step authenticated run; if that's ever done and the values turn
// out to be per-step deltas instead, switch the caller to sum them.
//
// The CLI stamps a top-level "sessionID" field onto every emitted event
// (verified against opencode-ai v1.18.6), so it's extracted here regardless
// of event type and returned alongside the log entry/outcome so the caller
// can persist it for session resume (see #283).
//
// The final return value, parsedJSON, is true when the line was valid JSON
// matching the opencode envelope (any type, including unrecognized), false
// only when json.Unmarshal failed. Callers use it to scope
// is429Line/isTransientLine raw-line sniffing to lines that never parsed as
// JSON — a successfully-parsed event's Content is the agent's own prose/tool
// output, and re-sniffing it is pure false-positive surface (see issue
// #335). Note opencode has no typed error classification today (unlike
// claude/qwen's Class from the "result" envelope, or codex's from
// "turn.failed"/"error"), so scoping the fallback this way means a rate
// limit reported only inside a structured event body is no longer detected
// on stdout — mitigated by the fact that opencode CLI errors also surface on
// stderr (still unconditionally sniffed) and via non-zero exit.
func classifyOpencodeJSON(line string) (agent.LogEntry, string, *runUsage, string, bool) {
	var raw struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		Part      struct {
			Type   string   `json:"type"`
			Text   string   `json:"text"`
			Reason string   `json:"reason"`
			Cost   *float64 `json:"cost"`
			Tokens *struct {
				Input  int64 `json:"input"`
				Output int64 `json:"output"`
			} `json:"tokens"`
		} `json:"part"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, "", nil, "", false
	}

	switch raw.Type {
	case "text":
		outcome := extractOutcome(raw.Part.Text)
		return agent.LogEntry{Type: agent.LogStdout, Content: raw.Part.Text, At: time.Now()}, outcome, nil, raw.SessionID, true
	case "tool_use":
		return agent.LogEntry{Type: agent.LogToolCall, Content: line, At: time.Now()}, "", nil, raw.SessionID, true
	case "tool_result":
		return agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, "", nil, raw.SessionID, true
	case "step_finish":
		usage := extractOpencodeUsage(raw.Part.Cost, raw.Part.Tokens)
		// step_finish with reason="stop" means the agent is done
		if raw.Part.Reason == "stop" {
			return agent.LogEntry{Type: agent.LogSystem, Content: "step finished", At: time.Now()}, "", usage, raw.SessionID, true
		}
		return agent.LogEntry{Type: agent.LogSystem, Content: fmt.Sprintf("step finished: %s", raw.Part.Reason), At: time.Now()}, "", usage, raw.SessionID, true
	case "step_start":
		return agent.LogEntry{Type: agent.LogSystem, Content: "step started", At: time.Now()}, "", nil, raw.SessionID, true
	default:
		// Log unknown types as raw stdout for debuggability
		text := raw.Part.Text
		if text == "" {
			text = line
		}
		return agent.LogEntry{Type: agent.LogStdout, Content: text, At: time.Now()}, "", nil, raw.SessionID, true
	}
}

// extractOpencodeUsage builds a *runUsage from a step_finish part's cost/
// tokens fields, mirroring extractResultUsage's guard in
// parse_streamjson.go: returns nil unless at least one of cost/tokens is
// present, so a step_finish that genuinely carries neither stays at zero
// rather than recording a spurious $0.
func extractOpencodeUsage(cost *float64, tokens *struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}) *runUsage {
	if cost == nil && tokens == nil {
		return nil
	}
	u := &runUsage{}
	if tokens != nil {
		u.InputTokens = tokens.Input
		u.OutputTokens = tokens.Output
	}
	if cost != nil {
		u.CostUSD = *cost
	}
	return u
}
