package providers

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Codex writes every thread to a JSONL "rollout" file under
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<timestamp>-<thread_id>.jsonl
// (verified against codex 0.163.0). The `codex exec --json` stream carries
// neither the model a run used nor whether its usage is per-run or
// cumulative, so CodexRunner reads two facts back from that file, both
// best-effort (a missing or unreadable file just leaves today's behavior):
//
//   - the model of the last turn_context entry, to price a run whose agent
//     config leaves the model unset (codex then picks its own default);
//   - the session's cumulative token usage from the last token_count event,
//     read before and after a resumed run, so the resumed run's own usage
//     can be separated from turns earlier runs already paid for (see
//     codexRunUsage).

// codexHomeDir returns the CODEX_HOME the codex subprocess will use, from its
// final environment: CODEX_HOME if set, else $HOME/.codex.
func codexHomeDir(env []string) string {
	var home string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok && v != "" {
			return v
		}
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

// findCodexRollout returns the rollout file for threadID under codexHome, or
// "" when none is found.
func findCodexRollout(codexHome, threadID string) string {
	if codexHome == "" || threadID == "" || strings.ContainsAny(threadID, `/\*?[`) {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(codexHome, "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

// codexRolloutInfo is what readCodexRollout extracts from a rollout file.
type codexRolloutInfo struct {
	// Model is the model of the last turn_context entry ("" if none).
	Model string
	// Total is the last token_count event's cumulative session usage (nil if
	// the file has none).
	Total *runUsage
}

// readCodexRollout scans a rollout file for the last turn_context model and
// the last cumulative token_count usage. Lines it can't parse are skipped.
func readCodexRollout(path string) codexRolloutInfo {
	var info codexRolloutInfo
	if path == "" {
		return info
	}
	f, err := os.Open(path)
	if err != nil {
		return info
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxScanLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		// Cheap pre-filter: most rollout lines are messages and tool output.
		if !strings.Contains(string(line), `"turn_context"`) && !strings.Contains(string(line), `"token_count"`) {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Payload struct {
				Type  string `json:"type"`
				Model string `json:"model"`
				Info  *struct {
					Total *struct {
						InputTokens       int64 `json:"input_tokens"`
						CachedInputTokens int64 `json:"cached_input_tokens"`
						OutputTokens      int64 `json:"output_tokens"`
					} `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		switch {
		case entry.Type == "turn_context" && entry.Payload.Model != "":
			info.Model = entry.Payload.Model
		case entry.Type == "event_msg" && entry.Payload.Type == "token_count" && entry.Payload.Info != nil && entry.Payload.Info.Total != nil:
			t := entry.Payload.Info.Total
			info.Total = &runUsage{InputTokens: t.InputTokens, CachedInputTokens: t.CachedInputTokens, OutputTokens: t.OutputTokens}
		}
	}
	return info
}

// codexRunUsage returns this run's own usage. u is what codex reported at
// turn.completed; baseline and final are the session's cumulative token_count
// totals from the rollout before and after the run (nil when unreadable).
// If u equals the final cumulative total, codex reported a running session
// total, so the baseline earlier runs already paid for is subtracted.
// Otherwise (or without both rollout reads) u is taken to cover only this
// run and is returned unchanged.
//
// With no turn.completed at all (u == nil: the run was stopped at max_turns,
// timed out or crashed mid-turn), the rollout's final total stands in for it,
// so the tokens the run did spend are still priced.
func codexRunUsage(u, baseline, final *runUsage) *runUsage {
	if u == nil {
		if final == nil {
			return nil
		}
		return usageSince(final, baseline)
	}
	if baseline == nil || final == nil {
		return u
	}
	if u.InputTokens != final.InputTokens || u.OutputTokens != final.OutputTokens {
		return u
	}
	return usageSince(u, baseline)
}

// usageSince returns total minus baseline, or total unchanged when there is
// no baseline or total is not a superset of it (codex did not carry the
// earlier runs' usage into this session's total).
func usageSince(total, baseline *runUsage) *runUsage {
	out := *total
	if baseline == nil || total.InputTokens < baseline.InputTokens || total.OutputTokens < baseline.OutputTokens || total.CachedInputTokens < baseline.CachedInputTokens {
		return &out
	}
	out.InputTokens -= baseline.InputTokens
	out.CachedInputTokens -= baseline.CachedInputTokens
	out.OutputTokens -= baseline.OutputTokens
	return &out
}
