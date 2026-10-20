package boardtools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTemplateTools(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{
		"GET /api/v1/templates":    `[{"id":"t1","name":"Triage"}]`,
		"POST /api/v1/templates":   `{"id":"t1","name":"Triage","type":"chore"}`,
		"GET /api/v1/templates/t1": `{"id":"t1","name":"Triage","title":"Triage issues","description":"d","type":"chore","created_at":"x"}`,
		"PUT /api/v1/templates/t1": `{"id":"t1","name":"Triage","title":"New","type":"chore"}`,
	})
	tools := Select(AdminRemoteTools)
	if text, isErr := c.Call(tools, "list_templates", nil); isErr || !strings.Contains(text, "Triage") {
		t.Fatalf("list_templates: %q", text)
	}
	text, isErr := c.Call(tools, "create_template", json.RawMessage(`{"name":"Triage","type":"chore","bogus":1}`))
	if isErr || !strings.Contains(text, "Template created") {
		t.Fatalf("create_template: %q", text)
	}
	if b := (*calls)[1].body; b["name"] != "Triage" || b["type"] != "chore" || len(b) != 2 {
		t.Errorf("create body = %v", b)
	}
	text, isErr = c.Call(tools, "update_template", json.RawMessage(`{"template_id":"t1","title":"New"}`))
	if isErr || !strings.Contains(text, "Template updated") {
		t.Fatalf("update_template: %q", text)
	}
	b := (*calls)[len(*calls)-1].body
	if b["title"] != "New" || b["name"] != "Triage" || b["description"] != "d" || b["type"] != "chore" || len(b) != 4 {
		t.Errorf("update body = %v", b)
	}
	for _, tc := range []struct{ tool, args string }{
		{"create_template", `{"title":"x"}`},
		{"update_template", `{"title":"x"}`},
	} {
		if _, isErr := c.Call(tools, tc.tool, json.RawMessage(tc.args)); !isErr {
			t.Errorf("%s accepted %s", tc.tool, tc.args)
		}
	}
}

func TestScheduleTools(t *testing.T) {
	c, calls := stubAPI(t, map[string]string{
		"GET /api/v1/schedules":    `[{"id":"s1"}]`,
		"POST /api/v1/schedules":   `{"id":"s1"}`,
		"GET /api/v1/schedules/s1": `{"id":"s1","template_id":"t1","repo_id":"r1","cron_expr":"0 9 * * 1","target_label":"not_ready","enabled":true}`,
		"PUT /api/v1/schedules/s1": `{"id":"s1","enabled":false}`,
	})
	tools := Select(AdminRemoteTools)
	if text, isErr := c.Call(tools, "list_schedules", nil); isErr || !strings.Contains(text, "s1") {
		t.Fatalf("list_schedules: %q", text)
	}
	text, isErr := c.Call(tools, "create_schedule", json.RawMessage(`{"template_id":"t1","repo_id":"r1","cron_expr":"0 9 * * 1","enabled":false}`))
	if isErr || !strings.Contains(text, "Schedule created") {
		t.Fatalf("create_schedule: %q", text)
	}
	if b := (*calls)[1].body; b["template_id"] != "t1" || b["enabled"] != false || b["target_label"] != nil {
		t.Errorf("create body = %v", b)
	}
	// Pausing keeps the cron and target label.
	text, isErr = c.Call(tools, "update_schedule", json.RawMessage(`{"schedule_id":"s1","enabled":false}`))
	if isErr || !strings.Contains(text, "Schedule updated") {
		t.Fatalf("update_schedule: %q", text)
	}
	b := (*calls)[len(*calls)-1].body
	if b["enabled"] != false || b["cron_expr"] != "0 9 * * 1" || b["target_label"] != "not_ready" || len(b) != 3 {
		t.Errorf("update body = %v", b)
	}
	// Changing only the cron keeps it enabled.
	c.Call(tools, "update_schedule", json.RawMessage(`{"schedule_id":"s1","cron_expr":"0 8 * * *"}`))
	b = (*calls)[len(*calls)-1].body
	if b["enabled"] != true || b["cron_expr"] != "0 8 * * *" {
		t.Errorf("cron update body = %v", b)
	}
	for _, tc := range []struct{ tool, args string }{
		{"create_schedule", `{"template_id":"t1","repo_id":"r1"}`},
		{"update_schedule", `{"cron_expr":"* * * * *"}`},
	} {
		if _, isErr := c.Call(tools, tc.tool, json.RawMessage(tc.args)); !isErr {
			t.Errorf("%s accepted %s", tc.tool, tc.args)
		}
	}
}
