package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// executeLLMTool runs a single tool call for LLMRunner and AnthropicRunner.
// Returns (output, nil) for file/shell tools, or (output, *Result) for
// signal_complete and request_human which terminate the run.
func executeLLMTool(repoPath, name string, args map[string]string) (string, *Result) {
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
		cmd := exec.Command("sh", "-c", args["command"])
		cmd.Dir = repoPath // run inside the repo, not the server's cwd
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("exit error: %v\n%s", err, out), nil
		}
		return string(out), nil

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
		label := args["next_label"]
		msg := args["summary"]
		return "acknowledged", &Result{Status: "completed", NextLabel: &label, Message: &msg}

	case "request_human":
		msg := args["message"]
		return "pausing for human input", &Result{Status: "waiting_human", Message: &msg}

	default:
		return fmt.Sprintf("unknown tool: %s", name), nil
	}
}

// safeRepoPath joins repoPath and rel, then verifies the result is still
// inside repoPath to prevent path traversal attacks.
func safeRepoPath(repoPath, rel string) (string, error) {
	// filepath.Join with an absolute second arg discards the first in Go,
	// so we must clean relative to a "/" prefix then re-root.
	clean := filepath.Join(repoPath, filepath.FromSlash(rel))
	// Resolve symlinks not needed — just check the prefix.
	root := filepath.Clean(repoPath) + string(os.PathSeparator)
	if clean != filepath.Clean(repoPath) && !strings.HasPrefix(clean, root) {
		return "", fmt.Errorf("path %q escapes repository root", rel)
	}
	return clean, nil
}
