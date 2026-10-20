package boardtools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Operating tools: "what is waiting on me?", stopping a run and re-running a
// task. Thin wrappers over the REST API.

func init() {
	register(Tool{
		Name:        "list_needs_me",
		Description: "List the tasks waiting on a human (agent questions, reviews, failures), oldest first, with task id, title, label and reason. A good first call to see what needs your attention.",
		InputSchema: obj(map[string]any{}),
		ReadOnly:    true,
		call:        func(c *Client, _ json.RawMessage) (string, bool) { return c.listNeedsMe() },
	})
	register(Tool{
		Name:        "cancel_run",
		Description: "Stop a running agent run. Defaults to the task's active run. Fails if the run is not currently running.",
		InputSchema: obj(map[string]any{
			"task_id": str("Task ID"),
			"run_id":  str("The run's ID; defaults to the task's active run"),
		}, "task_id"),
		call: (*Client).cancelRun,
	})
	register(Tool{
		Name:        "rerun_task",
		Description: "Re-run a task: clears its active run and resets its consecutive-failure count so an agent can pick it up again from its current label.",
		InputSchema: obj(map[string]any{"task_id": str("Task ID")}, "task_id"),
		call:        (*Client).rerunTask,
	})
}

func (c *Client) listNeedsMe() (string, bool) {
	var res struct {
		Items []struct {
			TaskID    string `json:"task_id"`
			TaskTitle string `json:"task_title"`
			Label     string `json:"label"`
			Reason    string `json:"reason"`
			RunID     string `json:"run_id"`
		} `json:"items"`
	}
	if _, err := c.do(http.MethodGet, "/api/v1/needs-me", nil, &res); err != nil {
		return "failed to list needs-me: " + err.Error(), true
	}
	type item struct {
		TaskID string `json:"task_id"`
		Title  string `json:"title"`
		Label  string `json:"label"`
		Reason string `json:"reason"`
		RunID  string `json:"run_id,omitempty"`
	}
	out := make([]item, 0, len(res.Items))
	for _, it := range res.Items {
		out = append(out, item{it.TaskID, it.TaskTitle, it.Label, it.Reason, it.RunID})
	}
	b, _ := json.Marshal(out)
	return truncate(string(b)), false
}

func (c *Client) cancelRun(args json.RawMessage) (string, bool) {
	var a struct {
		TaskID string `json:"task_id"`
		RunID  string `json:"run_id"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.TaskID) == "" {
		return "task_id is required", true
	}
	if a.RunID == "" {
		var task struct {
			ActiveAgentRunID *string `json:"active_agent_run_id"`
		}
		if _, err := c.do(http.MethodGet, "/api/v1/tasks/"+url.PathEscape(a.TaskID), nil, &task); err != nil {
			return "failed to load task: " + err.Error(), true
		}
		if task.ActiveAgentRunID == nil {
			return "task has no active run to cancel", true
		}
		a.RunID = *task.ActiveAgentRunID
	}
	path := fmt.Sprintf("/api/v1/tasks/%s/runs/%s/cancel", url.PathEscape(a.TaskID), url.PathEscape(a.RunID))
	if status, err := c.do(http.MethodPost, path, nil, nil); err != nil {
		return fmt.Sprintf("cancel_run failed (%d): %s", status, err.Error()), true
	}
	return fmt.Sprintf("Cancelling run %s.", a.RunID), false
}

func (c *Client) rerunTask(args json.RawMessage) (string, bool) {
	id, errMsg := taskArg(args)
	if errMsg != "" {
		return errMsg, true
	}
	if status, err := c.do(http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/rerun", nil, nil); err != nil {
		return fmt.Sprintf("rerun_task failed (%d): %s", status, err.Error()), true
	}
	return fmt.Sprintf("Task %s will be picked up again.", id), false
}
