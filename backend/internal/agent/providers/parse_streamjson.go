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
	// Usage is the token usage / cost reported by the CLI. For "result"
	// messages this is the *authoritative final total* for the whole run
	// (including CostUSD, from total_cost_usd). For "assistant" messages this
	// is that single message's *own turn* usage only (CostUSD always zero —
	// the CLI never reports cost per-message) — callers that want a running
	// total across the run must sum these themselves (see IsResult).
	// Nil for every other message type.
	Usage *runUsage
	// IsResult is true when this event came from a terminal "result" message,
	// distinguishing Usage's authoritative-final-total meaning from an
	// "assistant" message's per-turn-incremental meaning above.
	IsResult bool
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
	// Parsed is true when the line was valid stream-json (any event type,
	// including an unrecognized "type"). False only when json.Unmarshal
	// failed, i.e. the line wasn't JSON at all.
	//
	// Callers use this to scope agent.ClassifyLine's raw-line sniffing to
	// lines that failed to parse as structured JSON. A successfully-parsed
	// assistant/tool_use/tool_result event has already been classified (or
	// deliberately left ClassNone) by this typed path — its Content is the
	// agent's own prose or the contents of a file it read/wrote, and
	// re-sniffing that payload with ClassifyLine is pure false-positive
	// surface (a diff hunk header containing "429", a "timeout" identifier
	// in source code, etc. — see issue #335). Only lines that never parsed
	// as JSON (interleaved plain-text CLI output) should still be sniffed.
	Parsed bool
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
		// Pass the *raw* line through — display shaping (extracting text,
		// summarizing tool calls) is the frontend's job (parseAgentLog.ts).
		// We only classify the line so the log renders with the right treatment:
		// LogToolCall when it carries a tool_use block, LogStdout for prose.
		logType := agent.LogStdout
		if assistantHasToolUse(raw) {
			logType = agent.LogToolCall
		}
		return streamEvent{
			Entry:     agent.LogEntry{Type: logType, Content: line, At: time.Now()},
			SessionID: sessionID,
			// Per-message usage (no total_cost_usd — that only appears on the
			// terminal "result" event). Used by the cost watchdog (see
			// providers/cost_watchdog.go) to project mid-run cost; the caller
			// is responsible for summing across assistant messages since each
			// one reports only its own turn's usage, not a running total.
			Usage:  extractAssistantUsage(raw),
			Parsed: true,
		}
	case "tool_use":
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogToolCall, Content: line, At: time.Now()}, SessionID: sessionID, Parsed: true}
	case "tool_result":
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, SessionID: sessionID, Parsed: true}
	case "user":
		// Claude SDK wraps tool results in a user message: {"type":"user","message":{"role":"user","content":[{"type":"tool_result",...}]}}
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogToolResult, Content: line, At: time.Now()}, SessionID: sessionID, Parsed: true}
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
			case "error":
				outcome = "failure"
				// error_max_turns is intentionally NOT mapped to a "failure"
				// outcome here: it is signalled structurally via Class
				// (ClassMaxTurns, from classifyResultMessage below) / the
				// provider-level agent.ErrMaxTurns instead, so pool.go can
				// escalate to waiting_human rather than resolving a normal
				// completed+failure outcome that fires the workflow's failure
				// edge and re-dispatches with a fresh turn budget.
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
			IsResult:       true,
			Class:          classifyResultMessage(raw),
			SessionID:      sessionID,
			ResultText:     resultText,
			APIErrorStatus: apiErrorStatus,
			Parsed:         true,
		}
	default:
		return streamEvent{Entry: agent.LogEntry{Type: agent.LogStdout, Content: line, At: time.Now()}, SessionID: sessionID, Parsed: true}
	}
}

// classifyResultMessage derives a failure Classification from a claude/qwen
// stream-json "result" envelope — a *typed* terminal event — so the providers
// can prefer it over sniffing arbitrary log lines. Returns ClassNone for a
// successful result, or for an error whose text carries no recognizable
// infra/auth/rate-limit/max-turns signal.
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
	// carries no "429"/"rate limit" substring at all). Checked ahead of the
	// max-turns subtype below so a result that carries BOTH a 429 and
	// error_max_turns still classifies as a rate limit (back off, don't
	// escalate).
	if v, ok := raw["api_error_status"]; ok {
		var status int
		if err := json.Unmarshal(v, &status); err == nil && status == 429 {
			return agent.ClassRateLimit
		}
	}
	// error_max_turns is a structural signal (the subtype itself), not text
	// to sniff: the agent hit its configured turn cap. Escalate rather than
	// treat as a genuine task failure — re-dispatching would silently hand
	// the next run a fresh turn budget.
	if subtype == "error_max_turns" {
		return agent.ClassMaxTurns
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

// extractResultUsage parses the usage/total_cost_usd/num_turns fields from a
// claude/qwen CLI stream-json "result" message. num_turns is the CLI's own
// count of internal agent turns the run consumed, compared against the
// configured max_turns cap. Returns nil if none of the fields are present.
func extractResultUsage(raw map[string]json.RawMessage) *runUsage {
	var parsed struct {
		Usage *struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
		TotalCostUSD *float64 `json:"total_cost_usd"`
		NumTurns     *int64   `json:"num_turns"`
	}
	// usage/total_cost_usd/num_turns live at the top level of the result
	// envelope, alongside type/subtype/result.
	if v, ok := raw["usage"]; ok {
		_ = json.Unmarshal(v, &parsed.Usage)
	}
	if v, ok := raw["total_cost_usd"]; ok {
		_ = json.Unmarshal(v, &parsed.TotalCostUSD)
	}
	if v, ok := raw["num_turns"]; ok {
		_ = json.Unmarshal(v, &parsed.NumTurns)
	}
	if parsed.Usage == nil && parsed.TotalCostUSD == nil && parsed.NumTurns == nil {
		return nil
	}
	u := &runUsage{}
	if parsed.NumTurns != nil {
		u.Turns = *parsed.NumTurns
	}
	if parsed.Usage != nil {
		u.InputTokens = parsed.Usage.InputTokens
		u.OutputTokens = parsed.Usage.OutputTokens
	}
	if parsed.TotalCostUSD != nil {
		u.CostUSD = *parsed.TotalCostUSD
	}
	return u
}

// extractAssistantUsage parses the per-message usage block from a claude/qwen
// CLI stream-json "assistant" message:
//
//	{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":N,"output_tokens":N,"cache_creation_input_tokens":N,"cache_read_input_tokens":N},...}}
//
// This is *per-turn* usage (each assistant message reports only its own
// turn), not a running total — the caller (claude.go/qwen.go's cost watchdog
// wiring) must sum across every assistant message in the run to get
// cumulative token counts. There is no total_cost_usd on this event; that
// only appears on the terminal "result" message (see extractResultUsage) once
// it's too late for a mid-run kill switch to act on it, which is why the
// watchdog derives cost from tokens via the pricing table instead.
//
// Cache tokens (cache_creation_input_tokens/cache_read_input_tokens) are
// folded into InputTokens for cost-projection purposes: the pricing table
// (pricing.go) has no separate cache read/write rate today, so this is an
// approximation — cache reads in particular are typically priced well below
// full input-token rate, so a watchdog projection that includes them at full
// input price is conservative (overestimates cost, never under-warns).
//
// Returns nil if the message carries no usage block at all (e.g. a
// tool_use-only continuation in some CLI versions).
func extractAssistantUsage(raw map[string]json.RawMessage) *runUsage {
	var msg struct {
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw["message"], &msg); err != nil || msg.Usage == nil {
		return nil
	}
	return &runUsage{
		InputTokens:  msg.Usage.InputTokens + msg.Usage.CacheCreationInputTokens + msg.Usage.CacheReadInputTokens,
		OutputTokens: msg.Usage.OutputTokens,
	}
}

// assistantHasToolUse reports whether an assistant stream-json message carries
// at least one tool_use content block. Used to classify the raw line as
// LogToolCall (vs LogStdout for prose) so the frontend renders it with the
// right treatment — the raw line is passed through either way.
func assistantHasToolUse(raw map[string]json.RawMessage) bool {
	var msg struct {
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw["message"], &msg); err != nil {
		return false
	}
	for _, c := range msg.Content {
		if c.Type == "tool_use" {
			return true
		}
	}
	return false
}
