package providers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/agent"
)

// classifyOpencodeJSON parses one NDJSON line from opencode run --format json.
//
// Usage/cost: the "step_finish" event's part carries both a "cost" (float,
// USD) and a "tokens" object ({input,output,reasoning,cache:{read,write}})
// (verified against opencode-ai v1.19.1). cost/input/output/cache.read/
// cache.write are read here — see extractOpencodeUsage — mirroring
// extractResultUsage's nil-if-neither-present guard in parse_streamjson.go.
//
// step_finish fires once per *step*, not once per run, so there may be
// several per run, and each one's cost/tokens is a *per-step delta*, not a
// cumulative-to-date snapshot: opencode-ai v1.19.1's session processor does
// `assistantMessage.cost += step.cost` as each step completes and emits the
// step-finish part with that step's own `cost`/`tokens` only (confirmed by
// reading the processor source, not inferred). The caller (OpencodeRunner.Run)
// therefore *sums* cost/tokens across every step_finish in the run rather
// than taking the last one, which would otherwise undercount a multi-step
// run down to just its final step.
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
			Type   string          `json:"type"`
			Text   string          `json:"text"`
			Reason string          `json:"reason"`
			Cost   *float64        `json:"cost"`
			Tokens *opencodeTokens `json:"tokens"`
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

// opencodeTokens mirrors the "tokens" object on a step_finish part:
// {input,output,reasoning,cache:{read,write}} (verified against opencode-ai
// v1.19.1).
type opencodeTokens struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
	Cache  struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

// extractOpencodeUsage builds a *runUsage from a step_finish part's cost/
// tokens fields, mirroring extractResultUsage's guard in
// parse_streamjson.go: returns nil unless at least one of cost/tokens is
// present, so a step_finish that genuinely carries neither stays at zero
// rather than recording a spurious $0. cache.read/cache.write are folded
// into InputTokens so the input-token total (and therefore any downstream
// pricing based on it) isn't missing the prompt-cache portion of the step's
// usage; reasoning tokens are still not tracked.
func extractOpencodeUsage(cost *float64, tokens *opencodeTokens) *runUsage {
	if cost == nil && tokens == nil {
		return nil
	}
	u := &runUsage{}
	if tokens != nil {
		u.InputTokens = tokens.Input + tokens.Cache.Read + tokens.Cache.Write
		u.OutputTokens = tokens.Output
	}
	if cost != nil {
		u.CostUSD = *cost
	}
	return u
}

// sumRunUsage adds b's cost/tokens onto a and returns the result. Used to
// accumulate opencode's per-step step_finish usage across an entire run
// (see classifyOpencodeJSON's doc comment) rather than keeping only the
// last step's values. a may be nil (first step_finish observed); b is
// never nil (callers only invoke this when a new usage was just parsed).
func sumRunUsage(a, b *runUsage) *runUsage {
	if a == nil {
		cp := *b
		return &cp
	}
	return &runUsage{
		InputTokens:       a.InputTokens + b.InputTokens,
		OutputTokens:      a.OutputTokens + b.OutputTokens,
		CachedInputTokens: a.CachedInputTokens + b.CachedInputTokens,
		CostUSD:           a.CostUSD + b.CostUSD,
		Turns:             a.Turns + b.Turns,
	}
}

// opencodeIsStepFinish reports whether line is a "step_finish" event.
// opencode has no CLI turn-cap flag of its own analogous to claude/qwen's
// --max-turns, so OpencodeRunner counts these runner-side as a backstop to
// the config-side agent.<name>.steps cap (see opencodeConfigContent and
// opencodeMaxStepsMessage) — one step_finish is opencode's closest
// equivalent of a claude/qwen agent turn (mirrors codexCompletedToolStep's
// role for the codex provider). A cheap substring check gates the
// json.Unmarshal, mirroring codexCompletedToolStep's shape.
func opencodeIsStepFinish(line string) bool {
	if !strings.Contains(line, `"step_finish"`) {
		return false
	}
	var ev struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil {
		return false
	}
	return ev.Type == "step_finish"
}
