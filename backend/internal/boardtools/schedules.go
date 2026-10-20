package boardtools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Recurring work: task templates and the cron schedules that fire them. The
// list tools are in every remote profile; the write tools only in
// AdminRemoteTools, since a schedule creates agent work unattended. There are
// deliberately no delete tools.

const cronHelp = "Standard 5-field cron: minute hour day-of-month month day-of-week, e.g. \"0 9 * * 1\" = 09:00 every Monday."

const targetLabelHelp = "Label the created task starts on. Empty/omitted = the repo workflow's human-gate label (e.g. \"not_ready\"), so a human promotes it. Setting a label an enabled agent works on (e.g. \"work\") makes the schedule fully unattended."

var templateTypes = []string{"feature", "bug", "chore", "spike"}

func init() {
	register(Tool{
		Name:        "list_templates",
		Description: "List task templates (name, title, description, type): reusable shapes of work that a schedule instantiates as tasks. Use it to find the template_id for create_schedule.",
		InputSchema: obj(map[string]any{}),
		ReadOnly:    true,
		call: func(c *Client, _ json.RawMessage) (string, bool) {
			return c.getRaw("/api/v1/templates", "list_templates")
		},
	})
	register(Tool{
		Name:        "list_schedules",
		Description: "List task schedules: which template is instantiated in which repo, on what cron expression, starting on which label, whether it is enabled, and when it last fired.",
		InputSchema: obj(map[string]any{}),
		ReadOnly:    true,
		call: func(c *Client, _ json.RawMessage) (string, bool) {
			return c.getRaw("/api/v1/schedules", "list_schedules")
		},
	})
	register(Tool{
		Name:        "create_template",
		Description: "Create a task template: the title, description and type a scheduled task is created with. The name must be unique. Then use create_schedule to run it on a cron.",
		InputSchema: obj(templateProps(true), "name"),
		call:        (*Client).createTemplate,
	})
	register(Tool{
		Name:        "update_template",
		Description: "Change a task template. Only the fields you pass change. Schedules using it pick up the change on their next firing.",
		InputSchema: obj(templateProps(false), "template_id"),
		call:        (*Client).updateTemplate,
	})
	register(Tool{
		Name:        "create_schedule",
		Description: "Create a schedule that creates a task from a template in a repo on a cron expression (e.g. every Monday, triage open issues). " + cronHelp + " It skips a firing while an open task from an earlier firing of the same schedule exists. " + targetLabelHelp,
		InputSchema: obj(map[string]any{
			"template_id":  str("Template ID (from list_templates)"),
			"repo_id":      str("Repo ID (from list_repos); cannot be changed later"),
			"cron_expr":    str(cronHelp),
			"target_label": str(targetLabelHelp),
			"enabled":      map[string]any{"type": "boolean", "description": "Default true; pass false to create it paused"},
		}, "template_id", "repo_id", "cron_expr"),
		call: (*Client).createSchedule,
	})
	register(Tool{
		Name:        "update_schedule",
		Description: "Change a schedule. Only the fields you pass change. Pass enabled=false to pause it and enabled=true to resume. The template and repo cannot be changed. " + cronHelp,
		InputSchema: obj(map[string]any{
			"schedule_id":  str("Schedule ID (from list_schedules)"),
			"cron_expr":    str(cronHelp),
			"target_label": str(targetLabelHelp + " Pass an empty string to reset to the default."),
			"enabled":      map[string]any{"type": "boolean", "description": "false pauses the schedule, true resumes it"},
		}, "schedule_id"),
		call: (*Client).updateSchedule,
	})
}

func templateProps(create bool) map[string]any {
	p := map[string]any{
		"name":        str("Unique template name"),
		"title":       str("Title of the tasks created from it"),
		"description": str("Description of the tasks created from it (markdown supported)"),
		"type":        map[string]any{"type": "string", "enum": templateTypes, "description": "Task type (default feature)"},
	}
	if !create {
		p["template_id"] = str("Template ID (from list_templates)")
	}
	return p
}

// fieldArgs parses the tool arguments into raw fields.
func fieldArgs(args json.RawMessage) (map[string]json.RawMessage, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return in, nil
}

// idArg reads a required string id argument.
func idArg(in map[string]json.RawMessage, key string) (string, string) {
	var id string
	_ = json.Unmarshal(in[key], &id)
	if strings.TrimSpace(id) == "" {
		return "", key + " is required"
	}
	return id, ""
}

// mergeFields overwrites base[k] with the argument for each key in keys that
// was passed (and isn't null).
func mergeFields(base map[string]any, in map[string]json.RawMessage, keys ...string) {
	for _, k := range keys {
		if v, ok := in[k]; ok && string(v) != "null" {
			base[k] = v
		}
	}
}

func (c *Client) createTemplate(args json.RawMessage) (string, bool) {
	in, err := fieldArgs(args)
	if err != nil {
		return err.Error(), true
	}
	body := map[string]any{}
	mergeFields(body, in, "name", "title", "description", "type")
	var name string
	_ = json.Unmarshal(in["name"], &name)
	if strings.TrimSpace(name) == "" {
		return "name is required", true
	}
	var raw json.RawMessage
	if status, err := c.do(http.MethodPost, "/api/v1/templates", body, &raw); err != nil {
		return fmt.Sprintf("create_template failed (%d): %s", status, err.Error()), true
	}
	return "Template created.\n" + truncate(string(raw)), false
}

func (c *Client) updateTemplate(args json.RawMessage) (string, bool) {
	in, err := fieldArgs(args)
	if err != nil {
		return err.Error(), true
	}
	id, msg := idArg(in, "template_id")
	if msg != "" {
		return msg, true
	}
	path := "/api/v1/templates/" + url.PathEscape(id)
	// The API replaces the whole record, so start from the current one.
	var cur map[string]any
	if status, err := c.do(http.MethodGet, path, nil, &cur); err != nil {
		return fmt.Sprintf("update_template failed (%d): %s", status, err.Error()), true
	}
	body := map[string]any{}
	for _, k := range []string{"name", "title", "description", "type"} {
		body[k] = cur[k]
	}
	mergeFields(body, in, "name", "title", "description", "type")
	var raw json.RawMessage
	if status, err := c.do(http.MethodPut, path, body, &raw); err != nil {
		return fmt.Sprintf("update_template failed (%d): %s", status, err.Error()), true
	}
	return "Template updated.\n" + truncate(string(raw)), false
}

func (c *Client) createSchedule(args json.RawMessage) (string, bool) {
	in, err := fieldArgs(args)
	if err != nil {
		return err.Error(), true
	}
	for _, k := range []string{"template_id", "repo_id", "cron_expr"} {
		if _, msg := idArg(in, k); msg != "" {
			return msg, true
		}
	}
	body := map[string]any{}
	mergeFields(body, in, "template_id", "repo_id", "cron_expr", "target_label", "enabled")
	var raw json.RawMessage
	if status, err := c.do(http.MethodPost, "/api/v1/schedules", body, &raw); err != nil {
		return fmt.Sprintf("create_schedule failed (%d): %s", status, err.Error()), true
	}
	return "Schedule created.\n" + truncate(string(raw)), false
}

func (c *Client) updateSchedule(args json.RawMessage) (string, bool) {
	in, err := fieldArgs(args)
	if err != nil {
		return err.Error(), true
	}
	id, msg := idArg(in, "schedule_id")
	if msg != "" {
		return msg, true
	}
	path := "/api/v1/schedules/" + url.PathEscape(id)
	// The API treats a missing enabled as true and an empty target_label as
	// "reset to default", so always send the current values for what isn't
	// being changed.
	var cur map[string]any
	if status, err := c.do(http.MethodGet, path, nil, &cur); err != nil {
		return fmt.Sprintf("update_schedule failed (%d): %s", status, err.Error()), true
	}
	body := map[string]any{}
	for _, k := range []string{"cron_expr", "target_label", "enabled"} {
		body[k] = cur[k]
	}
	mergeFields(body, in, "cron_expr", "target_label", "enabled")
	var raw json.RawMessage
	if status, err := c.do(http.MethodPut, path, body, &raw); err != nil {
		return fmt.Sprintf("update_schedule failed (%d): %s", status, err.Error()), true
	}
	return "Schedule updated.\n" + truncate(string(raw)), false
}
