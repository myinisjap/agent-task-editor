package boardtools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Agent config tools: reading provider configs and agents, and creating or
// editing the agents that pick up tasks on a label. The reads are in every
// remote profile; create_agent/update_agent only in AdminRemoteTools.
// Secrets never pass through: provider env is dropped from every output, and
// the command lists, plugins and MCP servers are not settable here.

func init() {
	register(Tool{
		Name:        "list_provider_configs",
		Description: "List the provider configs (id, name, provider, model) an agent can run on. Use an id as provider_config_id in create_agent/update_agent.",
		InputSchema: obj(map[string]any{}),
		ReadOnly:    true,
		call:        (*Client).listProviderConfigs,
	})
	register(Tool{
		Name:        "get_agent",
		Description: "Get one agent config in full, including its system prompt. Provider environment values are not returned. Includes running_experiment (share, tasks per arm) when an A/B experiment is running.",
		InputSchema: obj(map[string]any{"agent_id": str("Agent ID (from list_agents)")}, "agent_id"),
		ReadOnly:    true,
		call:        (*Client).getAgent,
	})
	register(Tool{
		Name:        "create_agent",
		Description: "Create an agent config. An enabled agent whose labels include a workflow label picks up tasks that arrive on it (see list_agents for what already covers a label). Needs name and provider_config_id (from list_provider_configs). If another agent already lists one of the labels, the new one starts disabled unless you pass enabled.",
		InputSchema: obj(agentProps(), "name", "provider_config_id"),
		call:        (*Client).createAgent,
	})
	register(Tool{
		Name:        "update_agent",
		Description: "Change an agent config. Only the fields you pass change; the rest keep their current values. labels, fallback_provider_config_ids and escalation_ladder replace the whole list.",
		InputSchema: obj(withAgentID(agentProps()), "agent_id"),
		call:        (*Client).updateAgent,
	})
}

func agentProps() map[string]any {
	return map[string]any{
		"name":                         str("Display name"),
		"provider_config_id":           str("Provider config the agent runs on (from list_provider_configs)"),
		"system_prompt":                str("Instructions given to the agent on every run"),
		"labels":                       map[string]any{"type": "array", "items": str("Workflow label name"), "description": "Labels this agent works on, e.g. [\"work\"]"},
		"enabled":                      map[string]any{"type": "boolean", "description": "Whether the agent picks up tasks"},
		"max_turns":                    map[string]any{"type": "integer", "description": "Max agent turns per run (default 50)"},
		"timeout_secs":                 map[string]any{"type": "integer", "description": "Run timeout in seconds (default 600)"},
		"max_cost_usd":                 map[string]any{"type": "number", "description": "Cost cap per run in USD; 0 means no cap"},
		"priority":                     map[string]any{"type": "integer", "description": "Higher priority agents are tried first when several share a label"},
		"effort":                       map[string]any{"type": "string", "enum": []string{"", "low", "medium", "high", "xhigh", "max"}},
		"permission_mode":              map[string]any{"type": "string", "enum": []string{"", "default", "auto", "acceptEdits", "bypassPermissions"}},
		"fallback_provider_config_ids": map[string]any{"type": "array", "items": str("Provider config ID"), "description": "Ordered providers to fall back to; must not include the primary"},
		"escalation_ladder":            map[string]any{"type": "array", "description": "Escalate on failure: rung N (1-based) is used after N consecutive failed attempts or review bounce-backs on this agent's stage; rung 0 is the agent's own provider/effort. Empty = never escalate", "items": map[string]any{"type": "object", "required": []string{"provider_config_id"}, "properties": map[string]any{"provider_config_id": str("Provider config for this rung"), "effort": map[string]any{"type": "string", "enum": []string{"", "low", "medium", "high", "xhigh", "max"}}, "fallback_provider_config_ids": map[string]any{"type": "array", "items": str("Provider config ID")}}}},
		"note":                         str("Optional change note recorded in the agent's version history"),
	}
}

func withAgentID(p map[string]any) map[string]any {
	p["agent_id"] = str("Agent ID (from list_agents)")
	return p
}

// agentFields are the create_agent/update_agent arguments forwarded to the API.
var agentFields = []string{
	"name", "provider_config_id", "system_prompt", "labels", "enabled", "max_turns", "timeout_secs",
	"max_cost_usd", "priority", "effort", "permission_mode", "fallback_provider_config_ids", "escalation_ladder", "note",
}

// agentBody copies the whitelisted fields out of the arguments. labels is
// sent as the JSON-array string the API stores.
func agentBody(args json.RawMessage) (map[string]any, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	body := map[string]any{}
	for _, k := range agentFields {
		v, ok := in[k]
		if !ok || string(v) == "null" {
			continue
		}
		if k == "labels" {
			var labels []string
			if err := json.Unmarshal(v, &labels); err != nil {
				return nil, fmt.Errorf("labels must be an array of label names")
			}
			enc, _ := json.Marshal(labels)
			body[k] = string(enc)
			continue
		}
		body[k] = v
	}
	return body, nil
}

func (c *Client) listProviderConfigs(_ json.RawMessage) (string, bool) {
	var cfgs []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if _, err := c.do(http.MethodGet, "/api/v1/provider-configs", nil, &cfgs); err != nil {
		return "failed to list provider configs: " + err.Error(), true
	}
	out := make([]map[string]any, 0, len(cfgs))
	for _, p := range cfgs {
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "provider": p.Provider, "model": p.Model})
	}
	return marshal(out)
}

func agentArg(args json.RawMessage) (string, string) {
	var a struct {
		AgentID string `json:"agent_id"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.AgentID) == "" {
		return "", "agent_id is required"
	}
	return a.AgentID, ""
}

// redactAgent drops provider_config.env from an agent API response.
func redactAgent(raw json.RawMessage) (string, bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "failed to decode agent: " + err.Error(), true
	}
	if pc, ok := m["provider_config"].(map[string]any); ok {
		delete(pc, "env")
	}
	return marshal(m)
}

func (c *Client) getAgent(args json.RawMessage) (string, bool) {
	id, errMsg := agentArg(args)
	if errMsg != "" {
		return errMsg, true
	}
	var raw json.RawMessage
	if status, err := c.do(http.MethodGet, "/api/v1/agents/"+url.PathEscape(id), nil, &raw); err != nil {
		return fmt.Sprintf("get_agent failed (%d): %s", status, err.Error()), true
	}
	return redactAgent(raw)
}

func (c *Client) createAgent(args json.RawMessage) (string, bool) {
	body, err := agentBody(args)
	if err != nil {
		return err.Error(), true
	}
	if s, _ := body["name"].(json.RawMessage); strings.TrimSpace(strings.Trim(string(s), `"`)) == "" || body["provider_config_id"] == nil {
		return "name and provider_config_id are required", true
	}
	// The create endpoint has no enabled field; apply it afterwards.
	wantEnabled, hasEnabled := body["enabled"]
	delete(body, "enabled")
	var raw json.RawMessage
	if status, err := c.do(http.MethodPost, "/api/v1/agents", body, &raw); err != nil {
		return fmt.Sprintf("create_agent failed (%d): %s", status, err.Error()), true
	}
	if hasEnabled {
		var cur struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		var want bool
		if json.Unmarshal(raw, &cur) == nil && json.Unmarshal(wantEnabled.(json.RawMessage), &want) == nil && cur.Enabled != want {
			var upd json.RawMessage
			if status, err := c.putAgent(cur.ID, map[string]any{"enabled": want}, &upd); err != nil {
				return fmt.Sprintf("agent created (%s) but setting enabled failed (%d): %s", cur.ID, status, err.Error()), true
			}
			raw = upd
		}
	}
	out, isErr := redactAgent(raw)
	if isErr {
		return out, true
	}
	return "Agent created.\n" + out, false
}

func (c *Client) updateAgent(args json.RawMessage) (string, bool) {
	id, errMsg := agentArg(args)
	if errMsg != "" {
		return errMsg, true
	}
	changes, err := agentBody(args)
	if err != nil {
		return err.Error(), true
	}
	if len(changes) == 0 {
		return "nothing to change: pass at least one field besides agent_id", true
	}
	var raw json.RawMessage
	if status, err := c.putAgent(id, changes, &raw); err != nil {
		return fmt.Sprintf("update_agent failed (%d): %s", status, err.Error()), true
	}
	out, isErr := redactAgent(raw)
	if isErr {
		return out, true
	}
	return "Agent updated.\n" + out, false
}

// putAgent merges changes onto the agent's current config and PUTs the result.
// PUT overwrites name, prompt, labels and the limits with whatever it is sent
// (zero included), so these are always filled in from the current config;
// the other fields keep their stored value when omitted.
func (c *Client) putAgent(id string, changes map[string]any, out *json.RawMessage) (int, error) {
	path := "/api/v1/agents/" + url.PathEscape(id)
	var cur map[string]json.RawMessage
	if status, err := c.do(http.MethodGet, path, nil, &cur); err != nil {
		return status, err
	}
	body := map[string]any{}
	for _, k := range []string{"name", "provider_config_id", "system_prompt", "labels", "max_tokens", "timeout_secs", "max_turns"} {
		if v, ok := cur[k]; ok {
			body[k] = v
		}
	}
	for k, v := range changes {
		body[k] = v
	}
	return c.do(http.MethodPut, path, body, out)
}
