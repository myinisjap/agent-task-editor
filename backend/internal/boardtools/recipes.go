package boardtools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Automation recipes (admin profile only): bundled one-click presets that
// expand into a template plus a schedule and/or an intake endpoint + rule.
// There is deliberately no remove tool; use the Settings UI.

func init() {
	register(Tool{
		Name:        "list_recipes",
		Description: "List automation recipes (CI red on main, error-tracker alert, weekly dependency updates, stale TODO/flaky sweep, docs drift check) with their install parameters and whether each is installed.",
		InputSchema: obj(map[string]any{}),
		ReadOnly:    true,
		call: func(c *Client, _ json.RawMessage) (string, bool) {
			return c.getRaw("/api/v1/automation-recipes", "list_recipes")
		},
	})
	register(Tool{
		Name: "install_recipe",
		Description: "Install an automation recipe: creates its template plus a schedule and/or a signed intake webhook endpoint with a routing rule, in one step. " +
			"Leave target_label empty so created tasks land parked for a human to promote; a label an enabled agent works on needs confirm_auto_start=true. " +
			"For webhook recipes the result contains the endpoint URL and secret (shown once; do not repeat it in chat) and a paste-ready sender snippet.",
		InputSchema: obj(map[string]any{
			"recipe_id":          str("Recipe id (from list_recipes)"),
			"repo_id":            str("Repo ID (from list_repos)"),
			"workflow_id":        str("Optional workflow ID; defaults to the repo's workflow"),
			"target_label":       str(targetLabelHelp),
			"cron_expr":          str("Schedule recipes only. " + cronHelp),
			"branch":             str("CI recipe only: the default branch name (default main)"),
			"confirm_auto_start": map[string]any{"type": "boolean", "description": "Required when target_label lets agents start without human review"},
		}, "recipe_id", "repo_id"),
		call: (*Client).installRecipe,
	})
}

func (c *Client) installRecipe(args json.RawMessage) (string, bool) {
	in, err := fieldArgs(args)
	if err != nil {
		return err.Error(), true
	}
	id, msg := idArg(in, "recipe_id")
	if msg != "" {
		return msg, true
	}
	if _, msg := idArg(in, "repo_id"); msg != "" {
		return msg, true
	}
	params := map[string]string{}
	for _, k := range []string{"repo_id", "workflow_id", "target_label", "cron_expr", "branch"} {
		var s string
		if json.Unmarshal(in[k], &s) == nil && s != "" {
			params[k] = s
		}
	}
	confirm := false
	_ = json.Unmarshal(in["confirm_auto_start"], &confirm)
	body := map[string]any{"params": params, "confirm_auto_start": confirm}
	var raw json.RawMessage
	path := "/api/v1/automation-recipes/" + strings.ReplaceAll(id, "/", "") + "/install"
	if status, err := c.do(http.MethodPost, path, body, &raw); err != nil {
		return fmt.Sprintf("install_recipe failed (%d): %s", status, err.Error()), true
	}
	return "Recipe installed.\n" + truncate(string(raw)), false
}
