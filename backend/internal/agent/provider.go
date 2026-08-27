// Package agent implements the agent runtime core: the Provider interface,
// bounded worker pool, and dispatcher. Concrete provider backends
// (ClaudeRunner, AnthropicRunner, LLMRunner, QwenRunner,
// CodexRunner, OpencodeRunner) live in the sibling providers package.
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
	// ResolvedComments lists review comments the agent claims to have addressed
	// (via the MCP sidecar's resolve_comment tool). The pool marks them resolved
	// in the database only when the run completes successfully.
	ResolvedComments []ResolvedComment
	// InputTokens/OutputTokens are the total tokens consumed across the run
	// (summed across every turn of a multi-turn agentic loop, where
	// applicable). Zero if the provider does not report usage.
	InputTokens int64
	// OutputTokens is the total output/completion tokens for the run.
	OutputTokens int64
	// CostUSD is the (estimated, unless otherwise noted) USD cost of the
	// run. For the `claude` CLI provider this is the CLI's own authoritative
	// total_cost_usd figure; for anthropic/llm providers it is computed from
	// InputTokens/OutputTokens via the internal pricing table. Zero if
	// unknown/unreported — not necessarily a free run.
	CostUSD float64
	// CostUnknown is true only when the anthropic/llm providers consumed
	// tokens but no price entry (DB-backed or hardcoded fallback) matched
	// the model, so CostUSD was left at 0 as a placeholder rather than a
	// computed figure. Always false for claude/qwen_code (and any other
	// provider that reports its own authoritative cost or none at all), so a
	// legitimate $0 (e.g. under a Claude Max subscription) is never
	// confused with an unpriced model.
	CostUnknown bool
	// SessionID is the provider-side conversation session for this run (the
	// claude/qwen CLI stream-json envelope's session_id). Persisted on the run
	// so a later run on the same task can resume the session with full prior
	// context. Empty for providers/runs without a session.
	SessionID string
	// CostWarned is true when the provider's mid-run cost watchdog (see
	// providers/cost_watchdog.go) observed projected cost cross the warning
	// threshold (RunInput.CostWarnRatio) at some point during this run, even
	// if the run ultimately completed under budget. The pool publishes
	// task.cost_warning when this is set. Always false for providers that
	// don't implement the watchdog.
	CostWarned bool
	// TurnsUsed is the number of internal agent turns the run actually
	// consumed, for comparison against the configured AgentConfig.MaxTurns
	// cap. 0 means "not reported" — only providers that expose a real count
	// set it (claude/qwen_code from the stream-json result event's num_turns;
	// anthropic/llm from their own agentic loop counter). codex_cli and
	// opencode report no comparable figure and leave it 0; it is never
	// estimated, since an invented count is worse than an absent one.
	TurnsUsed int64
}

// RunInput carries everything an agent needs to start work.
type RunInput struct {
	RunID         string
	Task          Task
	AgentConfig   AgentConfig
	RepoPath      string
	RepoRemoteURL string // empty if no remote configured
	// Available transitions from the task's current label, passed to the MCP sidecar.
	Transitions []TransitionHint
	// Human rejection note from a prior run, injected at the top of the prompt
	Feedback *string
	// Output from the plan stage, injected for later stages
	PriorPlan *string
	// Open inline diff review comments on the task, injected into the prompt
	// so the agent addresses (and resolves) each one.
	OpenReviewComments []ReviewComment
	// SourceComments are comments ingested from the external item this task was
	// imported from (e.g. its GitHub issue thread). Untrusted external content —
	// rendered into the prompt inside explicit delimiters, never as instructions.
	SourceComments []SourceComment
	// Absolute paths of attachment images on the server filesystem
	AttachmentAbsPaths []string
	// ResumeSessionID, if non-empty, asks the provider to resume this prior
	// conversation session instead of starting cold. Currently honored by the
	// `claude` provider (--resume); other providers ignore it. Providers that
	// resume fall back to a cold start if the session no longer exists.
	ResumeSessionID string
	// HumanReply is a human's textual answer to the agent's request_human
	// question, injected into the prompt (and, when combined with
	// ResumeSessionID, delivered as the next message of the resumed session).
	HumanReply *string
	// SubtaskConflicts, when set, is a rendered description of subtask branches
	// that failed to merge back into this (parent) task's branch. It is injected
	// into the prompt so the parent's work agent resolves the conflicts.
	SubtaskConflicts *string
	// CostBudgetUSD is the task's effective cost budget in USD (see
	// dispatcher.effectiveBudget — the lower nonzero of the task's and its
	// matched agent config's max_cost_usd). 0 means no cap. Providers that
	// implement a mid-run cost watchdog (claude, qwen — see
	// providers/cost_watchdog.go) use this, together with CostSpentUSD, to
	// decide when to cancel an in-flight run. Providers that don't support
	// the watchdog simply ignore it — the dispatcher's pre-dispatch
	// checkCostBudget guard is unaffected either way.
	CostBudgetUSD float64
	// CostSpentUSD is the task's cumulative recorded cost from every prior
	// run (see storage.SumTaskCost), passed in so the watchdog can project
	// *total* task cost (prior spend + this run's incremental usage) against
	// CostBudgetUSD rather than just this run's own cost in isolation.
	CostSpentUSD float64
	// CostWarnRatio is the fraction (0..1) of CostBudgetUSD at which the
	// watchdog should surface an early warning (Result.CostWarned) before the
	// hard kill at 1.0. 0 or CostBudgetUSD<=0 disables the warning. Defaults
	// to 0.8 (see dispatcher.defaultCostWarnRatio).
	CostWarnRatio float64
}

// TransitionHint describes an available transition for the MCP sidecar.
type TransitionHint struct {
	ToLabel string `json:"to_label"`
	Path    string `json:"path"` // success | failure | either
}

// ReviewComment is a minimal copy of storage's task_review_comments row —
// an inline, file/line-anchored comment left by a human on the task's diff.
type ReviewComment struct {
	ID         string `json:"id"`
	FilePath   string `json:"file_path"`
	Side       string `json:"side"` // old | new
	StartLine  int64  `json:"start_line"`
	EndLine    int64  `json:"end_line"`
	QuotedText string `json:"quoted_text"`
	Body       string `json:"body"`
}

// ResolvedComment records an agent's claim (via the resolve_comment MCP tool)
// that a specific review comment has been addressed.
type ResolvedComment struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

// SourceComment is a minimal copy of storage's task_source_comments row — a
// human comment ingested from the external item (e.g. a GitHub issue) this
// task was imported from. Untrusted external content: rendered into the
// prompt inside explicit delimiters and never treated as instructions. Unlike
// ReviewComment, it is not resolvable — there is no resolve_comment analogue —
// so it is intentionally not passed to the MCP sidecar's Prepare calls.
type SourceComment struct {
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
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
	// ParentID is the parent task id when this task is a subtask, else "".
	// The pool reads it to skip pushing child branches (children merge back into
	// the parent's branch instead) and to trigger merge-back on the parent.
	ParentID string
	// RepoPath is the repo's main clone path (not the worktree). Set so the pool
	// can drive parent worktree merges after a run.
	RepoPath string
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
	// CommandAllowlist, if non-empty, restricts run_bash/Bash commands to those
	// matching at least one glob pattern (see agent.matchCommandPattern). Denylist
	// is still checked and always wins. Best-effort string matching, not a sandbox.
	CommandAllowlist []string
	// CommandDenylist blocks any run_bash/Bash command matching a pattern here,
	// regardless of CommandAllowlist. Checked before the allowlist.
	CommandDenylist []string
	// ResumeSessions controls whether new runs for a task resume the previous
	// run's provider session (claude provider only; on by default). Off means
	// every run starts cold — useful for stages that want fresh eyes.
	ResumeSessions bool
	// SubtasksEnabled exposes the create_subtask MCP tool to this config's runs
	// (claude/qwen_code only). Off by default — decomposition is a deliberate
	// capability granted to a specific agent (typically the planning agent).
	SubtasksEnabled bool
	// MaxSubtasks caps how many children a single parent may have; enforced at
	// the create endpoint. Defaults to 10.
	MaxSubtasks int64
	// MaxCostUSD is an advisory per-task cost budget cap in USD, checked by
	// the dispatcher before each sweep-dispatch against the task's
	// cumulative recorded run cost so far (see Dispatcher.dispatch). 0
	// disables the cap (unlimited).
	//
	// Providers with mid-run priced usage (claude, qwen — see
	// providers/cost_watchdog.go) additionally enforce this as a mid-run kill
	// switch: they project cost from incremental token usage as the run
	// progresses and cancel the in-flight subprocess once the projection
	// crosses the budget, escalating to waiting_human instead of letting a
	// single runaway run blow arbitrarily far past the cap. Providers without
	// mid-run priced usage (codex, gemini, opencode) do not support this —
	// the guard only prevents the *next* dispatch for them, same as before.
	// See docs/agents.md's "Cost Budgets" section for the full picture,
	// including why the mid-run figure is an *estimate* (derived from the
	// pricing table), not the provider's own authoritative billed cost.
	MaxCostUSD float64
	// Effort is the optional reasoning effort level for providers that
	// support tuning it. Empty means unset (provider default). Valid values
	// are "", "low", "medium", "high", "xhigh", "max" — enforced at the API
	// layer (see handlers/agents.go). Currently honored by the claude
	// provider (--effort) and codex_cli (model_reasoning_effort config
	// override, with xhigh/max clamped down to high). Other providers
	// ignore it. See providers/effort.go.
	Effort string
	// PermissionMode is the optional Claude CLI permission mode for headless
	// task runs. Empty means unset: no --permission-mode flag is passed, so
	// the claude CLI falls back to its own default ("auto" — a cloud safety
	// classifier). Valid values are "", "default", "auto", "acceptEdits",
	// "bypassPermissions" — enforced at the API layer (see handlers/agents.go).
	// Claude-provider only; other providers ignore it (codex_cli and
	// qwen_code always run in their own always-bypass modes regardless).
	PermissionMode string
}

// Provider is the interface all agent backends must satisfy.
type Provider interface {
	Run(ctx context.Context, input RunInput, logCh chan<- LogEntry) (Result, error)
}
