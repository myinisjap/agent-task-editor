package boardtools

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const agentJSON = `{"id":"a1","name":"Coder","provider_config_id":"p1","system_prompt":"be good","labels":"[\"work\"]","max_tokens":4096,"timeout_secs":900,"max_turns":30,"enabled":true,"command_allowlist":"[\"rm\"]","provider_config":{"id":"p1","provider":"claude","model":"opus","env":"{\"KEY\":\"secret\"}"}}`

func TestAgentTools(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{
		"GET /api/v1/provider-configs": `[{"id":"p1","name":"Claude","provider":"claude","model":"opus","env":"{\"KEY\":\"secret\"}"}]`,
		"GET /api/v1/agents/a1":        agentJSON,
		"PUT /api/v1/agents/a1":        agentJSON,
		"POST /api/v1/agents":          `{"id":"a1","name":"Coder","enabled":true}`,
	})
	tools := Select(AdminRemoteTools)

	text, isErr := c.Call(tools, "list_provider_configs", json.RawMessage(`{}`))
	if isErr || !strings.Contains(text, `"model":"opus"`) || strings.Contains(text, "env") || strings.Contains(text, "secret") {
		t.Errorf("list_provider_configs: %q", text)
	}
	text, isErr = c.Call(tools, "get_agent", json.RawMessage(`{"agent_id":"a1"}`))
	if isErr || !strings.Contains(text, "be good") || strings.Contains(text, "secret") || strings.Contains(text, `"env"`) {
		t.Errorf("get_agent: %q", text)
	}

	n := len(*calls)
	text, isErr = c.Call(tools, "update_agent", json.RawMessage(`{"agent_id":"a1","labels":["work","review"],"priority":2,"note":"why","command_allowlist":"[]","enabled_plugins":"x"}`))
	if isErr || !strings.Contains(text, "Agent updated") || strings.Contains(text, "secret") {
		t.Fatalf("update_agent: %q", text)
	}
	put := (*calls)[n+1]
	if put.method != http.MethodPut {
		t.Fatalf("calls = %+v", (*calls)[n:])
	}
	b := put.body
	if b["note"] != "why" || b["labels"] != `["work","review"]` || b["priority"] != float64(2) || b["name"] != "Coder" ||
		b["system_prompt"] != "be good" || b["max_tokens"] != float64(4096) || b["timeout_secs"] != float64(900) || b["max_turns"] != float64(30) {
		t.Errorf("update_agent body = %v", b)
	}
	for _, k := range []string{"command_allowlist", "enabled_plugins", "enabled", "agent_id"} {
		if _, ok := b[k]; ok {
			t.Errorf("update_agent forwarded %s", k)
		}
	}

	text, isErr = c.Call(tools, "create_agent", json.RawMessage(`{"name":"Coder","provider_config_id":"p1","labels":["work"],"command_denylist":"x"}`))
	if isErr || !strings.Contains(text, "Agent created") {
		t.Fatalf("create_agent: %q", text)
	}
	post := (*calls)[len(*calls)-1]
	if post.method != http.MethodPost || post.body["labels"] != `["work"]` || post.body["command_denylist"] != nil {
		t.Errorf("create_agent sent %+v", post)
	}

	for _, tc := range []struct{ tool, args string }{
		{"get_agent", `{}`},
		{"update_agent", `{"agent_id":"a1"}`},
		{"update_agent", `{"labels":["x"]}`},
		{"update_agent", `{"agent_id":"a1","labels":"work"}`},
		{"create_agent", `{"name":"x"}`},
		{"create_agent", `{"provider_config_id":"p1"}`},
	} {
		if _, isErr := c.Call(tools, tc.tool, json.RawMessage(tc.args)); !isErr {
			t.Errorf("%s accepted %s", tc.tool, tc.args)
		}
	}
	for _, name := range []string{"create_agent", "update_agent"} {
		for _, tl := range Select(RemoteTools) {
			if tl.Name == name {
				t.Errorf("%s in full profile", name)
			}
		}
	}
}
