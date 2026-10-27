package reviewbrief

import (
	"context"
	"strings"
	"time"

	"github.com/myinisjap/agent-task-editor/backend/internal/storage/gen"
)

const (
	summaryLimit = 1500
	tailLines    = 20
	tailChars    = 2000
)

// VerificationSummary is the latest verify-command result for the task.
type VerificationSummary struct {
	Status     string    `json:"status"`
	Command    string    `json:"command"`
	ExitCode   *int64    `json:"exit_code"`
	DurationMs int64     `json:"duration_ms"`
	OutputTail string    `json:"output_tail"`
	Truncated  bool      `json:"truncated"`
	CreatedAt  time.Time `json:"created_at"`
}

// RunSummary aggregates run counts and cost for the task.
type RunSummary struct {
	Total       int     `json:"total"`
	Failed      int     `json:"failed"`
	ReworkLoops int     `json:"rework_loops"`
	CostUsd     float64 `json:"cost_usd"`
	CostUnknown bool    `json:"cost_unknown"`
}

// AgentSummary is the latest agent-written summary (store_info or notes).
type AgentSummary struct {
	Source    string `json:"source"` // stored_info | agent_notes
	RunID     string `json:"run_id"`
	AgentName string `json:"agent_name"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// Brief is the full review brief.
type Brief struct {
	Changed       ChangeSummary
	Flags         []Flag
	Verification  *VerificationSummary
	Runs          RunSummary
	AgentSummary  *AgentSummary
	DiffAvailable bool
	Truncated     bool
	ComputedAt    time.Time
}

// CountRework counts transitions into a label the task already visited. This
// is the same definition as the outcome-quality rework metric
// (handlers/outcome_quality.go).
func CountRework(h []gen.TaskLabelHistory) int {
	visited := map[string]bool{}
	n := 0
	for _, e := range h {
		if visited[e.ToLabel] {
			n++
		}
		visited[e.ToLabel] = true
	}
	return n
}

func tail(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	out := strings.Join(lines, "\n")
	if r := []rune(out); len(r) > tailChars {
		out = string(r[len(r)-tailChars:])
	}
	return out
}

// Build assembles the brief. The diff/flags part is cached; the DB sections
// are cheap reads done on every call.
func (c *Cache) Build(ctx context.Context, q *gen.Queries, task gen.Task) (Brief, error) {
	d, err := c.Diff(ctx, q, task)
	if err != nil {
		return Brief{}, err
	}
	b := Brief{
		Changed: d.Changed, Flags: d.Flags, DiffAvailable: d.Available,
		Truncated: d.Truncated, ComputedAt: c.now().UTC(),
	}
	if b.Flags == nil {
		b.Flags = []Flag{}
	}
	if b.Changed.TopFiles == nil {
		b.Changed.TopFiles = []FileStat{}
	}

	if vs, err := q.ListTaskVerifications(ctx, gen.ListTaskVerificationsParams{TaskID: task.ID, Limit: 1}); err == nil && len(vs) > 0 {
		v := vs[0]
		b.Verification = &VerificationSummary{
			Status: v.Status, Command: v.Command, ExitCode: v.ExitCode, DurationMs: v.DurationMs,
			OutputTail: tail(v.Output), Truncated: v.Truncated != 0, CreatedAt: v.CreatedAt,
		}
	} else if err != nil {
		return Brief{}, err
	}

	runs, err := q.ListAgentRuns(ctx, task.ID)
	if err != nil {
		return Brief{}, err
	}
	b.Runs.Total = len(runs)
	for _, r := range runs {
		if r.Status == "failed" {
			b.Runs.Failed++
		}
	}
	hist, err := q.ListTaskLabelHistory(ctx, task.ID)
	if err != nil {
		return Brief{}, err
	}
	b.Runs.ReworkLoops = CountRework(hist)
	if b.Runs.CostUsd, err = q.SumTaskCost(ctx, task.ID); err != nil {
		return Brief{}, err
	}
	unknown, err := q.CountTaskCostUnknownRuns(ctx, task.ID)
	if err != nil {
		return Brief{}, err
	}
	b.Runs.CostUnknown = unknown > 0

	for _, r := range runs { // newest first
		if r.StoredInfo == nil || strings.TrimSpace(*r.StoredInfo) == "" {
			continue
		}
		text, trunc := firstN(strings.TrimSpace(*r.StoredInfo), summaryLimit)
		as := &AgentSummary{Source: "stored_info", RunID: r.ID, Text: text, Truncated: trunc}
		if r.AgentConfigID != nil {
			if cfg, err := q.GetAgentConfig(ctx, *r.AgentConfigID); err == nil {
				as.AgentName = cfg.Name
			}
		}
		b.AgentSummary = as
		break
	}
	if b.AgentSummary == nil {
		if notes := strings.TrimSpace(task.AgentNotes); notes != "" {
			r := []rune(notes)
			trunc := len(r) > summaryLimit
			if trunc {
				r = r[len(r)-summaryLimit:]
			}
			b.AgentSummary = &AgentSummary{Source: "agent_notes", Text: string(r), Truncated: trunc}
		}
	}
	return b, nil
}

func firstN(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]), true
}

// OneLine returns the first non-empty line of s with any leading markdown
// marker stripped, capped at n runes (with an ellipsis when cut).
func OneLine(s string, n int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#-*> "))
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > n {
			return strings.TrimSpace(string(r[:n])) + "…"
		}
		return line
	}
	return ""
}
