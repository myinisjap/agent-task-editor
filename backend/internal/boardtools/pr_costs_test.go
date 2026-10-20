package boardtools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreatePR(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{
		"POST /api/v1/tasks/t1/pr": `{"pr_url":"https://github.com/o/r/pull/7","git_state":"pr_open"}`,
	})
	text, isErr := c.Call(Select(RemoteTools), "create_pr", json.RawMessage(`{"task_id":"t1"}`))
	if isErr || !strings.Contains(text, "https://github.com/o/r/pull/7") {
		t.Fatalf("got %q (err=%v)", text, isErr)
	}
	if len(*calls) != 1 {
		t.Errorf("calls = %d", len(*calls))
	}
	if text, isErr := c.Call(Select(RemoteTools), "create_pr", json.RawMessage(`{}`)); !isErr || !strings.Contains(text, "task_id") {
		t.Errorf("missing task_id: %q", text)
	}
	if text, isErr := c.Call(Select(RemoteTools), "create_pr", json.RawMessage(`{"task_id":"nope"}`)); !isErr || !strings.Contains(text, "create_pr failed") {
		t.Errorf("api error: %q", text)
	}
}

func TestGetCosts(t *testing.T) {
	c, _ := stubAPI(t, map[string]string{
		"GET /api/v1/dashboard": `{"cost_total":{"input_tokens":100,"output_tokens":50,"cost_usd":3.5},
			"cost_by_provider":[{"provider":"claude","input_tokens":100,"output_tokens":50,"cost_usd":3.5,"run_count":4}],
			"cost_by_task":[{"task_id":"b","task_title":"Big one"}]}`,
		"GET /api/v1/dashboard/cost-by-task": `[{"task_id":"a","cost_usd":0.5},{"task_id":"b","cost_usd":3},{"task_id":"c","cost_usd":0.1}]`,
	})
	tools := Select(ReadOnlyRemoteTools)
	text, isErr := c.Call(tools, "get_costs", json.RawMessage(`{"limit":2}`))
	if isErr || !strings.Contains(text, "$3.50") || !strings.Contains(text, "claude") ||
		!strings.Contains(text, "1. b: $3.00") || !strings.Contains(text, "Big one") || strings.Contains(text, "c:") {
		t.Fatalf("got %q (err=%v)", text, isErr)
	}
	text, isErr = c.Call(tools, "get_costs", json.RawMessage(`{"task_id":"a"}`))
	if isErr || !strings.HasPrefix(text, "a: $0.50") {
		t.Errorf("task_id: %q", text)
	}
	text, _ = c.Call(tools, "get_costs", json.RawMessage(`{"task_id":"zzz"}`))
	if !strings.Contains(text, "No recorded") {
		t.Errorf("unknown task: %q", text)
	}
}
