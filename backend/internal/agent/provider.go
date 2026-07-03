// Package agent implements the agent runtime: provider interface, bounded pool,
// dispatcher, and concrete backends (ClaudeRunner, LLMRunner, QwenRunner).
package agent

import (
	"context"
	"time"
)

// LogType classifies a single streamed log line.
type LogType string

const (
	LogStdout     LogType = "stdout"
	LogStderr     LogType = "stderr"
	LogSystem     LogType = "system"
	LogToolCall   LogType = "tool_call"
	LogToolResult LogType = "tool_result"
)

// LogEntry is a single streamed output line from an agent run.
type LogEntry struct {
	Type    LogType
	Content string
	At      time.Time
}

// Result is what an agent returns when its run ends.
type Result struct {
	// completed | failed | waiting_human
	Status string
	// success | failure — resolved to a label by the pool via workflow transitions
	Outcome string
	// Summary message or human help request
	Message *string
	// Agent-written notes to persist on the task (from MCP sidecar)
	Notes *string
	// Structured info stored on the run for later inspection
	StoredInfo *string
}

// RunInput carries everything an agent needs to start work.
type RunInput struct {
	RunID       string
	Task        Task
	AgentConfig AgentConfig
	RepoPath      string
	RepoRemoteURL string // empty if no remote configured
	// Available transitions from the task's current label, passed to the MCP sidecar.
	Transitions []TransitionHint
	// Human rejection note from a prior run, injected at the top of the prompt
	Feedback  *string
	// Output from the plan stage, injected for later stages
	PriorPlan *string
	// Absolute paths of attachment images on the server filesystem
	AttachmentAbsPaths []string
}

// Task is a minimal copy of storage.Task to avoid import cycles.
type Task struct {
	ID          string
	Title       string
	Description string
	Type        string
	Label       string
	WorkflowID  string
	AgentNotes  string
	Branch      string
	// Attachments is a JSON array of relative paths (e.g. ["<task_id>/abc.png"])
	Attachments []string
}

// AgentConfig is a minimal copy of storage.AgentConfig.
type AgentConfig struct {
	ID           string
	Name         string
	Provider     string
	Model        string
	SystemPrompt string
	MaxTokens    int64
	TimeoutSecs  int64
	MaxTurns     int64
	// MaxRetries is the number of automatic consecutive transient-error
	// retries allowed for a task before it is left failed (or escalated to
	// waiting_human) for a human to intervene. 0 disables auto-retry.
	MaxRetries int64
	// RetryBackoffSecs is the base backoff (in seconds) before a
	// transient-error retry is eligible for re-dispatch; exponential backoff
	// is applied on top of this base (see BackoffDurationWithBase).
	RetryBackoffSecs int64
	Env              map[string]string
	// EnabledPlugins is the list of Claude plugin IDs ("<name>@<marketplace>")
	// the user has explicitly enabled for this agent config. Claude-provider only.
	EnabledPlugins []string
	// EnabledMCPServers is the list of user-level Claude MCP server names
	// (from ~/.claude.json's global mcpServers) enabled for this agent config.
	// Claude-provider only.
	EnabledMCPServers []string
}

// Provider is the interface all agent backends must satisfy.
type Provider interface {
	Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error)
}
