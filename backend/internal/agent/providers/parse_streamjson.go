package providers

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// streamEvent carries everything classifyStreamJSON extracted from one
// NDJSON line, so callers needing more than the log entry (runAttempt's
// rate-limit handling in particular) don't force every call site to grow a
// positional-return tuple.
type streamEvent struct {
	Entry agent.LogEntry
	// Outcome is "success"/"failure" parsed from an OUTCOME marker or the
	// result subtype (only set for "result" messages).
	Outcome string
	// Usage is the token usage / cost reported by the CLI (non-nil for
	// "result" messages only).
	Usage *runUsage
	// Class is the failure Classification derived from the *structured*
	// terminal "result" event (ClassNone for every non-result message and
	// for a clean success). Lets the CLI providers prefer the typed error
	// event over sniffing arbitrary log lines — see errclass.go.
	Class agent.Classification
	// SessionID is the conversation session_id carried on the envelope
	// (empty for non-stream-json lines).
	SessionID string
	// ResultText is the raw "result" field text of a "result" event (empty
	// otherwise) — e.g. Claude's session-limit message "You've hit your
	// session limit · resets 6pm (America/Chicago)". Used by the claude
	// provider to derive an exact rate-limit reset time.
	ResultText string
	// APIErrorStatus is the "result" event's api_error_status field (0 if
	// absent/not a result event) — the structured HTTP status code
	// Anthropic returns alongside the human-readable result text. Preferred
	// over text-sniffing when present since it's authoritative and immune
	// to wording changes.
	APIErrorStatus int
}

// classifyStreamJSON parses one NDJSON line from claude --output-format
// stream-json into a streamEvent. See streamEvent's field docs for what each
// field means and when it's populated. Also used by qwen (see
// parse_qwen.go), which reuses the same stream-json envelope.
func classifyStreamJSON(line string) streamEvent {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}}
	}

	// Every stream-json event (init, assistant, result, …) carries the
	// conversation's session_id at the top level of the envelope.
	var sessionID string
	if v, ok := raw["session_id"]; ok {
		_ = json.Unmarshal(v, &sessionID)
	}

	msgType := strings.Trim(string(raw["type"]), `"`)
	switch msgType {
	case "assistant":
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogStdout, Content: extractAssistantText(raw), At: time.Now()}, SessionID: sessionID}
	case "tool_use":
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogToolCall, Content: line, At: time.Now()}, SessionID: sessionID}
	case "tool_result":
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, SessionID: sessionID}
	case "user":
		// Claude SDK wraps tool results in a user message: {"type":"user","message":{"role":"user","content":[{"type":"tool_result",...}]}}
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, SessionID: sessionID}
	case "result":
		// Parse OUTCOME: success|failure from the result text; fall back to subtype.
		var resultText string
		if resultRaw, ok := raw["result"]; ok {
			_ = json.Unmarshal(resultRaw, &resultText)
		}
		outcome := extractOutcome(resultText)
		if outcome == "" {
			subtype := strings.Trim(string(raw["subtype"]), `"`)
			switch subtype {
			case "success":
				outcome = "success"
			case "error_max_turns", "error":
				outcome = "failure"
			}
		}
		var apiErrorStatus int
		if v, ok := raw["api_error_status"]; ok {
			_ = json.Unmarshal(v, &apiErrorStatus)
		}
		usage := extractResultUsage(raw)
		return streamEvent{
			Entry:          agent.LogEntry{Type: agent.LogSystem, Content: line, At: time.Now()},
			Outcome:        outcome,
			Usage:          usage,
			Class:          classifyResultMessage(raw),
			SessionID:      sessionID,
			ResultText:     resultText,
			APIErrorStatus: apiErrorStatus,
		}
	default:
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, SessionID: sessionID}
	}
}

// classifyResultMessage derives a failure Classification from a claude/qwen
// stream-json "result" envelope — a *typed* terminal event — so the providers
// can prefer it over sniffing arbitrary log lines. Returns ClassNone for a
// successful result, or for an error whose text carries no recognizable
// infra/auth/rate-limit signal (a genuine failure such as error_max_turns).
func classifyResultMessage(raw map[string]json.RawMessage) agent.Classification {
	subtype := strings.Trim(string(raw["subtype"]), `"`)
	isErr := false
	if v, ok := raw["is_error"]; ok {
		_ = json.Unmarshal(v, &isErr)
	}
	// A clean success carries no failure signal.
	if !isErr && subtype != "error" && subtype != "error_max_turns" {
		return agent.ClassNone
	}
	// The structured api_error_status is authoritative when present — more
	// robust than sniffing the human-readable result text, which can change
	// wording across CLI releases (e.g. Claude's session-limit message
	// carries no "429"/"rate limit" substring at all).
	if v, ok := raw["api_error_status"]; ok {
		var status int
		if err := json.Unmarshal(v, &status); err == nil && status == 429 {
			return agent.ClassRateLimit
		}
	}
	// Classify the structured error text, if any.
	if v, ok := raw["result"]; ok {
		var text string
		if err := json.Unmarshal(v, &text); err == nil {
			return agent.ClassifyLine(text)
		}
	}
	return agent.ClassNone
}

// extractResultUsage parses the usage/total_cost_usd fields from a claude/qwen
// CLI stream-json "result" message. Returns nil if neither field is present.
func extractResultUsage(raw map[string]json.RawMessage) *runUsage {
	var parsed struct {
		Usage *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
		TotalCostUSD *float64 `json:"total_cost_usd"`
	}
	// usage/total_cost_usd live at the top level of the result envelope,
	// alongside type/subtype/result.
	if v, ok := raw["usage"]; ok {
		_ = json.Unmarshal(v, &parsed.Usage)
	}
	if v, ok := raw["total_cost_usd"]; ok {
		_ = json.Unmarshal(v, &parsed.TotalCostUSD)
	}
	if parsed.Usage == nil && parsed.TotalCostUSD == nil {
		return nil
	}
	u := &runUsage{}
	if parsed.Usage != nil {
		u.InputTokens = parsed.Usage.InputTokens
		u.OutputTokens = parsed.Usage.OutputTokens
	}
	if parsed.TotalCostUSD != nil {
		u.CostUSD = *parsed.TotalCostUSD
	}
	return u
}

func extractAssistantText(raw map[string]json.RawMessage) string {
	var msg struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw["message"], &msg.Message); err != nil {
		return string(raw["message"])
	}
	var parts []string
	for _, c := range msg.Message.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, " ")
}
