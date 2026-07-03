package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const bashOutputLimit = 1 << 20 // 1 MB

// runAccumulators holds the cross-turn state that both AnthropicRunner and
// LLMRunner thread through their agentic loops: info stored via store_info,
// notes written via update_task_notes, and token usage summed across every
// turn (each turn is a separate API call with its own usage).
type runAccumulators struct {
	storedInfo string
	taskNotes  string

	// model is the model ID used for this run, set once at Run() start so
	// attach() can compute an estimated cost from accumulated tokens.
	model string

	inputTokens  int64
	outputTokens int64
}

// addUsage accumulates per-turn token usage.
func (a *runAccumulators) addUsage(inputTokens, outputTokens int64) {
	a.inputTokens += inputTokens
	a.outputTokens += outputTokens
}

// applySpecialTool handles the two provider-agnostic tools (store_info,
// update_task_notes) that mutate run state rather than touching the repo.
// rawArgs is the unparsed argument JSON (needed to read the boolean "append"
// flag). Returns (output, true) if it handled the tool, ("", false) otherwise.
func (a *runAccumulators) applySpecialTool(name string, args map[string]string, rawArgs []byte) (string, bool) {
	switch name {
	case "store_info":
		a.storedInfo = args["info"]
		return "stored", true
	case "update_task_notes":
		var appendNote bool
		var m map[string]json.RawMessage
		_ = json.Unmarshal(rawArgs, &m)
		if v, ok := m["append"]; ok {
			_ = json.Unmarshal(v, &appendNote)
		}
		if appendNote && a.taskNotes != "" {
			a.taskNotes = a.taskNotes + "\n\n" + args["notes"]
		} else {
			a.taskNotes = args["notes"]
		}
		return "Task notes updated", true
	default:
		return "", false
	}
}

// attach copies any accumulated stored info / task notes / token usage onto
// a Result, computing an estimated cost from the accumulated tokens and the
// model set on the accumulator.
func (a *runAccumulators) attach(res *Result) {
	if a.storedInfo != "" {
		res.StoredInfo = &a.storedInfo
	}
	if a.taskNotes != "" {
		res.Notes = &a.taskNotes
	}
	res.InputTokens = a.inputTokens
	res.OutputTokens = a.outputTokens
	res.CostUSD = estimateCostUSD(a.model, a.inputTokens, a.outputTokens)
}

// CommandPolicy holds the optional per-agent-config command allow/deny patterns
// enforced before executing a run_bash tool call. Patterns support "*" as a
// wildcard matching any substring; matching is against the full command string.
// This is best-effort defense-in-depth, not a sandbox — it does not prevent
// e.g. shell metacharacter tricks that construct a denied command indirectly.
type CommandPolicy struct {
	Allowlist []string
	Denylist  []string
}

// Allowed reports whether cmd may run under this policy: denylist match => false
// (denylist always wins); if Allowlist is non-empty, cmd must match at least one
// allow pattern; empty Allowlist + no denylist match => allowed.
func (p CommandPolicy) Allowed(cmd string) (bool, string) {
	cmd = strings.TrimSpace(cmd)
	for _, pat := range p.Denylist {
		if matchCommandPattern(pat, cmd) {
			return false, fmt.Sprintf("command denied by policy: matches denylist pattern %q", pat)
		}
	}
	if len(p.Allowlist) == 0 {
		return true, ""
	}
	for _, pat := range p.Allowlist {
		if matchCommandPattern(pat, cmd) {
			return true, ""
		}
	}
	return false, "command denied by policy: does not match any allowlist pattern"
}

// matchCommandPattern does simple "*"-wildcard matching of pattern against the
// full string s (case-sensitive, no regex, no anchoring quirks from path.Match).
func matchCommandPattern(pattern, s string) bool {
	// Split pattern on "*", require each segment to appear in order; first/last
	// segments must anchor to start/end unless pattern starts/ends with "*".
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return pattern == s
	}
	segs := strings.Split(pattern, "*")
	pos := 0
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		idx := strings.Index(s[pos:], seg)
		if idx == -1 {
			return false
		}
		if i == 0 && !strings.HasPrefix(pattern, "*") && idx != 0 {
			return false
		}
		pos += idx + len(seg)
	}
	if !strings.HasSuffix(pattern, "*") && !strings.HasSuffix(s, segs[len(segs)-1]) && segs[len(segs)-1] != "" {
		return false
	}
	return true
}

// executeLLMTool runs a single tool call for LLMRunner and AnthropicRunner.
// Returns (output, nil) for file/shell tools, or (output, *Result) for
// signal_complete and request_human which terminate the run.
func executeLLMTool(ctx context.Context, repoPath string, policy CommandPolicy, name string, args map[string]string) (string, *Result) {
	switch name {
	case "read_file":
		path, err := safeRepoPath(repoPath, args["path"])
		if err != nil {
			return fmt.Sprintf("error: %v", err), nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("error: %v", err), nil
		}
		return string(data), nil

	case "write_file":
		path, err := safeRepoPath(repoPath, args["path"])
		if err != nil {
			return fmt.Sprintf("error: %v", err), nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Sprintf("error creating dirs: %v", err), nil
		}
		if err := os.WriteFile(path, []byte(args["content"]), 0644); err != nil {
			return fmt.Sprintf("error: %v", err), nil
		}
		return "ok", nil

	case "run_bash":
		command := args["command"]
		if ok, reason := policy.Allowed(command); !ok {
			return fmt.Sprintf("error: %s", reason), nil
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = repoPath
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Sprintf("error: %v", err), nil
		}
		cmd.Stderr = cmd.Stdout // combined
		if err := cmd.Start(); err != nil {
			return fmt.Sprintf("error starting: %v", err), nil
		}
		out, _ := io.ReadAll(io.LimitReader(pipe, bashOutputLimit))
		err = cmd.Wait()
		result := string(out)
		if len(out) == bashOutputLimit {
			result += "\n[output truncated at 1 MB]"
		}
		if err != nil {
			return fmt.Sprintf("exit error: %v\n%s", err, result), nil
		}
		return result, nil

	case "list_files":
		dir := repoPath
		if p := args["path"]; p != "" {
			var err error
			dir, err = safeRepoPath(repoPath, p)
			if err != nil {
				return fmt.Sprintf("error: %v", err), nil
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Sprintf("error: %v", err), nil
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return strings.Join(names, "\n"), nil

	case "signal_complete":
		outcome := args["outcome"]
		msg := args["summary"]
		return "acknowledged", &Result{Status: "completed", Outcome: outcome, Message: &msg}

	case "request_human":
		msg := args["message"]
		return "pausing for human input", &Result{Status: "waiting_human", Message: &msg}

	default:
		return fmt.Sprintf("unknown tool: %s", name), nil
	}
}

// safeRepoPath joins repoPath and rel, then verifies the result is still
// inside repoPath, including symlink resolution to prevent traversal via symlinks.
func safeRepoPath(repoPath, rel string) (string, error) {
	// filepath.Join with an absolute second arg discards the first in Go,
	// so use filepath.FromSlash to keep it relative, then prefix-check.
	clean := filepath.Join(repoPath, filepath.FromSlash(rel))
	root := filepath.Clean(repoPath) + string(os.PathSeparator)
	if clean != filepath.Clean(repoPath) && !strings.HasPrefix(clean, root) {
		return "", fmt.Errorf("path %q escapes repository root", rel)
	}
	// Resolve symlinks to prevent a symlink inside the repo pointing outside.
	// Skip if the path doesn't exist yet (write_file creates new files).
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		realRoot, rerr := filepath.EvalSymlinks(filepath.Clean(repoPath))
		if rerr != nil {
			realRoot = filepath.Clean(repoPath)
		}
		rootWithSep := realRoot + string(os.PathSeparator)
		if real != realRoot && !strings.HasPrefix(real, rootWithSep) {
			return "", fmt.Errorf("path %q escapes repository root via symlink", rel)
		}
	}
	return clean, nil
}
