package boardtools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const (
	defaultCostTasks = 5
	maxCostTasks     = 50
)

func init() {
	register(Tool{
		Name:        "get_costs",
		Description: "Answer \"what have agents spent?\": total tokens and USD across finished runs, the split by provider, and the most expensive tasks. The board's cost endpoints take no date or repo filters, so the only options are how many top tasks to list or a single task_id.",
		InputSchema: obj(map[string]any{
			"limit":   map[string]any{"type": "integer", "description": fmt.Sprintf("Top tasks by cost to list (default %d, max %d)", defaultCostTasks, maxCostTasks)},
			"task_id": str("Only report this one task's cost instead of the top tasks"),
		}),
		ReadOnly: true,
		call:     (*Client).getCosts,
	})
}

type costUsage struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

func (u costUsage) String() string {
	return fmt.Sprintf("$%.2f (%d in / %d out tokens)", u.CostUSD, u.InputTokens, u.OutputTokens)
}

func (c *Client) getCosts(args json.RawMessage) (string, bool) {
	var a struct {
		Limit  int    `json:"limit"`
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(args, &a)
	a.TaskID = strings.TrimSpace(a.TaskID)
	if a.Limit <= 0 {
		a.Limit = defaultCostTasks
	}
	a.Limit = min(a.Limit, maxCostTasks)

	var dash struct {
		CostTotal      costUsage `json:"cost_total"`
		CostByProvider []struct {
			Provider string `json:"provider"`
			costUsage
			RunCount int `json:"run_count"`
		} `json:"cost_by_provider"`
		CostByTask []struct {
			TaskID    string `json:"task_id"`
			TaskTitle string `json:"task_title"`
		} `json:"cost_by_task"`
	}
	if status, err := c.do(http.MethodGet, "/api/v1/dashboard", nil, &dash); err != nil {
		return fmt.Sprintf("get_costs failed (%d): %s", status, err.Error()), true
	}
	var tasks []struct {
		TaskID string `json:"task_id"`
		costUsage
	}
	if status, err := c.do(http.MethodGet, "/api/v1/dashboard/cost-by-task", nil, &tasks); err != nil {
		return fmt.Sprintf("get_costs failed (%d): %s", status, err.Error()), true
	}
	titles := make(map[string]string, len(dash.CostByTask))
	for _, t := range dash.CostByTask {
		titles[t.TaskID] = t.TaskTitle
	}
	line := func(i int, id string, u costUsage) string {
		s := fmt.Sprintf("%d. %s: %s", i, id, u)
		if t := titles[id]; t != "" {
			s += " - " + t
		}
		return s
	}

	var b strings.Builder
	if a.TaskID != "" {
		for _, t := range tasks {
			if t.TaskID == a.TaskID {
				return line(1, t.TaskID, t.costUsage)[3:], false
			}
		}
		return fmt.Sprintf("No recorded agent cost for task %s.", a.TaskID), false
	}
	fmt.Fprintf(&b, "Total (finished runs): %s\n", dash.CostTotal)
	if len(dash.CostByProvider) > 0 {
		b.WriteString("By provider:\n")
		for _, p := range dash.CostByProvider {
			fmt.Fprintf(&b, "- %s: %s, %d runs\n", p.Provider, p.costUsage, p.RunCount)
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].CostUSD > tasks[j].CostUSD })
	if len(tasks) > a.Limit {
		tasks = tasks[:a.Limit]
	}
	if len(tasks) == 0 {
		b.WriteString("No per-task costs recorded.")
		return b.String(), false
	}
	fmt.Fprintf(&b, "Top %d tasks by cost (all runs):\n", len(tasks))
	for i, t := range tasks {
		b.WriteString(line(i+1, t.TaskID, t.costUsage) + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), false
}
