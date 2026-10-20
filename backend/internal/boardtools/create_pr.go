package boardtools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

func init() {
	register(Tool{
		Name:        "create_pr",
		Description: "Push a task's branch and open a GitHub pull request for it, returning the PR URL. Idempotent: if a PR already exists for the branch, that PR's URL is returned. Needs a task with a provisioned branch and a repo with a GitHub remote.",
		InputSchema: obj(map[string]any{"task_id": str("Task ID (from list_tasks)")}, "task_id"),
		call:        (*Client).createPR,
	})
}

func (c *Client) createPR(args json.RawMessage) (string, bool) {
	id, errMsg := taskArg(args)
	if errMsg != "" {
		return errMsg, true
	}
	var out struct {
		PRURL    string `json:"pr_url"`
		GitState string `json:"git_state"`
	}
	if status, err := c.do(http.MethodPost, "/api/v1/tasks/"+url.PathEscape(id)+"/pr", nil, &out); err != nil {
		return fmt.Sprintf("create_pr failed (%d): %s", status, err.Error()), true
	}
	if out.PRURL == "" {
		return fmt.Sprintf("create_pr: no PR URL returned for task %s", id), true
	}
	if out.GitState != "" {
		return fmt.Sprintf("%s (%s)", out.PRURL, out.GitState), false
	}
	return out.PRURL, false
}
