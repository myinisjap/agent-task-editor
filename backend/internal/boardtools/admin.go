package boardtools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Board configuration tools: reading and editing workflows, the agents bound
// to labels, and onboarding repos. The read tools are in every remote profile;
// the write tools only in AdminRemoteTools.

// workflowYAMLHelp describes the YAML shape shared by get_workflow,
// create_workflow and update_workflow (the same document as the UI's YAML tab
// and GET /workflows/{id}/export.yaml).
const workflowYAMLHelp = `YAML shape:
name: My Workflow
description: optional
labels:            # columns, left to right by sort_order
  - name: backlog
    color: "#6B7280"     # #RRGGBB
    sort_order: 0
    agent_ignore: true   # agents never move tasks here
  - name: work           # an enabled agent whose labels include "work" picks tasks up here (see list_agents)
    color: "#3B82F6"
    sort_order: 1
  - name: review
    color: "#EC4899"
    display_name: Human review   # optional column title shown in the UI; defaults to a humanized name
    sort_order: 2
    create_pr: true      # entering it pushes the branch and opens a PR (at most one label)
    wip_limit: 5         # optional; wip_limit_hard: true also stops dispatch into it when full
    autonomy:            # optional, human-gate labels only (all exits human-triggered); absent = ask
      mode: on_flags     # ask | on_flags | auto
      ask_on: [migration, auth_or_security, dependencies, tests_removed, ci_config]
      hold: 10m
      max_cost_usd: 5
  - name: done
    color: "#10B981"
    sort_order: 3
    is_terminal: true
transitions:       # the only moves allowed
  - {from: backlog, to: work, trigger: human}
  - {from: work, to: review, trigger: agent, path: success}
  - {from: review, to: work, trigger: human, path: failure}   # reject_task
  - {from: review, to: done, trigger: human, path: success}   # approve_task
trigger is agent, human or both; path (success|failure) is the outcome approve/reject and agents follow.
Labels may also set display_name (UI title, max 64 chars; name stays the identifier) and verify_mode (custom|disabled) with verify_command to override the repo's verify command.`

func init() {
	register(Tool{
		Name:        "get_workflow",
		Description: "Get one workflow as an editable YAML document (labels with their settings, and transitions). Read it before update_workflow and send back the whole edited document.",
		InputSchema: obj(map[string]any{"workflow_id": str("Workflow ID (from list_workflows)")}, "workflow_id"),
		ReadOnly:    true,
		call:        (*Client).getWorkflow,
	})
	register(Tool{
		Name:        "list_agents",
		Description: "List the agent configs: which labels each one works on, whether it is enabled, and its provider and model. A label only gets agent runs if an enabled agent lists it, so check this when designing a workflow.",
		InputSchema: obj(map[string]any{}),
		ReadOnly:    true,
		call:        (*Client).listAgents,
	})
	register(Tool{
		Name:        "get_repo",
		Description: "Get one repo's full settings: path, remote, workflow, clone_status/clone_error (a repo added from a remote_url clones in the background), verify command, runtime pins and issue sync.",
		InputSchema: obj(map[string]any{"repo_id": str("Repo ID (from list_repos)")}, "repo_id"),
		ReadOnly:    true,
		call: func(c *Client, args json.RawMessage) (string, bool) {
			id, errMsg := repoArg(args)
			if errMsg != "" {
				return errMsg, true
			}
			return c.getRaw("/api/v1/repos/"+url.PathEscape(id), "get_repo")
		},
	})
	register(Tool{
		Name:        "detect_repo_runtime",
		Description: "Suggest toolchain pins (go, node, python, rust, ruby, java, godot) for a repo from its manifest files (go.mod, .nvmrc, .python-version, ...). Changes nothing; pass the suggestions to update_repo as runtime_languages to apply them.",
		InputSchema: obj(map[string]any{"repo_id": str("Repo ID (from list_repos)")}, "repo_id"),
		ReadOnly:    true,
		call:        (*Client).detectRepoRuntime,
	})
	register(Tool{
		Name:        "create_workflow",
		Description: "Create a new workflow (board columns and the moves allowed between them) from a YAML document. The name must be unique. Assign it to a repo with update_repo, or pass its id to create_task.\n\n" + workflowYAMLHelp,
		InputSchema: obj(map[string]any{"yaml": str("The workflow as a YAML document")}, "yaml"),
		call:        (*Client).createWorkflow,
	})
	register(Tool{
		Name:        "update_workflow",
		Description: "Replace a workflow's name, description, labels and transitions with the given YAML document. It is a full replacement: start from get_workflow and send back the whole edited document. Refused if it would drop a label that still holds tasks; move or archive those tasks first.\n\n" + workflowYAMLHelp,
		InputSchema: obj(map[string]any{
			"workflow_id": str("Workflow ID (from list_workflows)"),
			"yaml":        str("The complete workflow as a YAML document"),
		}, "workflow_id", "yaml"),
		Destructive: true,
		call:        (*Client).updateWorkflow,
	})
	register(Tool{
		Name:        "add_repo",
		Description: "Onboard a repository onto the board. Pass remote_url (https:// or git@) to clone it into the server's repo directory in the background, or path for a checkout already on the server. Then poll get_repo until clone_status is \"ready\", and consider detect_repo_runtime and a verify_command (e.g. \"go test ./...\") so agents' work is checked.",
		InputSchema: obj(repoProps(true)),
		call:        (*Client).addRepo,
	})
	register(Tool{
		Name:        "update_repo",
		Description: "Change a repo's settings. Only the fields you pass change. Use it to assign a workflow, set the verify command, pin toolchains, cap concurrent runs, or turn on GitHub issue import.",
		InputSchema: obj(withRepoID(repoProps(false)), "repo_id"),
		call:        (*Client).updateRepo,
	})
}

// repoProps is the input schema shared by add_repo and update_repo. The keys
// are the REST API's own field names, so arguments are forwarded as-is.
func repoProps(create bool) map[string]any {
	p := map[string]any{
		"name":                   str("Display name (derived from a GitHub/Gitea remote_url when omitted)"),
		"remote_url":             str("Git remote, https:// or git@ (on update, empty string clears it)"),
		"workflow_id":            str("Workflow new tasks in this repo use (from list_workflows; on update, empty string clears it)"),
		"verify_command":         str("Shell command run in the task's worktree after an agent reports success, e.g. \"make test\"; non-zero exit sends the work back. Empty disables it."),
		"verify_timeout_seconds": map[string]any{"type": "integer", "description": "Verify command timeout, 1-3600 (default 600)"},
		"preview_command":        str("Shell command that starts the app's dev server in the task worktree; must listen on $PORT (127.0.0.1). With preview_routes it enables screenshots in the review brief."),
		"preview_routes":         str("Newline-separated paths to screenshot, e.g. \"/\\n/tasks\""),
		"preview_paths_glob":     str("Newline-separated globs deciding when a diff is a UI change (empty = frontend/**, web/**, src/**/*.{tsx,jsx,vue,svelte,css})"),
		"max_concurrent_runs":    map[string]any{"type": "integer", "description": "Max agent runs at once in this repo (default: the server's MAX_WORKERS)"},
		"runtime_languages": map[string]any{
			"type":        "array",
			"description": "Toolchain pins agents run with, e.g. [{\"id\":\"go\",\"version\":\"1.26\"}] (see detect_repo_runtime). On update this replaces the pins; [] clears them.",
			"items": obj(map[string]any{
				"id":      map[string]any{"type": "string", "enum": []string{"go", "node", "python", "rust", "ruby", "java", "godot"}},
				"version": str("mise version string, e.g. \"22\" or \"3.12\""),
			}, "id", "version"),
		},
		"issue_sync_enabled": map[string]any{"type": "boolean", "description": "Import the repo's GitHub issues as tasks (needs a GitHub remote_url)"},
		"issue_sync_label":   str("Only import issues carrying this GitHub label (empty = all open issues)"),
	}
	if create {
		p["path"] = str("Absolute path of an existing checkout on the server (omit to clone remote_url instead)")
	}
	return p
}

func withRepoID(p map[string]any) map[string]any {
	p["repo_id"] = str("Repo ID (from list_repos)")
	return p
}

// repoFields are the add_repo/update_repo arguments forwarded to the API.
var repoFields = []string{
	"name", "path", "remote_url", "workflow_id", "verify_command", "verify_timeout_seconds",
	"preview_command", "preview_routes", "preview_paths_glob",
	"max_concurrent_runs", "runtime_languages", "issue_sync_enabled", "issue_sync_label",
}

// repoBody copies the known repo fields out of the tool arguments, leaving
// out anything not passed so PATCH keeps the repo's other values.
func repoBody(args json.RawMessage, allowPath bool) (map[string]json.RawMessage, error) {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	body := map[string]json.RawMessage{}
	for _, k := range repoFields {
		if k == "path" && !allowPath {
			continue
		}
		if v, ok := in[k]; ok && string(v) != "null" {
			body[k] = v
		}
	}
	return body, nil
}

func repoArg(args json.RawMessage) (string, string) {
	var a struct {
		RepoID string `json:"repo_id"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.RepoID) == "" {
		return "", "repo_id is required"
	}
	return a.RepoID, ""
}

// repoSummary is what add_repo/update_repo report back.
type repoSummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	RemoteURL   *string `json:"remote_url"`
	WorkflowID  *string `json:"workflow_id"`
	CloneStatus string  `json:"clone_status"`
	CloneError  string  `json:"clone_error,omitempty"`
}

func (c *Client) addRepo(args json.RawMessage) (string, bool) {
	body, err := repoBody(args, true)
	if err != nil {
		return err.Error(), true
	}
	if body["path"] == nil && body["remote_url"] == nil {
		return "pass remote_url to clone a repository, or path for a checkout already on the server", true
	}
	var r repoSummary
	if status, err := c.do(http.MethodPost, "/api/v1/repos", body, &r); err != nil {
		return fmt.Sprintf("add_repo failed (%d): %s", status, err.Error()), true
	}
	out, isErr := marshal(r)
	if isErr {
		return out, true
	}
	if r.CloneStatus == "cloning" {
		return "Repo added; it is cloning in the background. Call get_repo until clone_status is \"ready\" before creating tasks.\n" + out, false
	}
	return "Repo added.\n" + out, false
}

func (c *Client) updateRepo(args json.RawMessage) (string, bool) {
	id, errMsg := repoArg(args)
	if errMsg != "" {
		return errMsg, true
	}
	body, err := repoBody(args, false)
	if err != nil {
		return err.Error(), true
	}
	if len(body) == 0 {
		return "nothing to change: pass at least one setting besides repo_id", true
	}
	var r repoSummary
	if status, err := c.do(http.MethodPatch, "/api/v1/repos/"+url.PathEscape(id), body, &r); err != nil {
		return fmt.Sprintf("update_repo failed (%d): %s", status, err.Error()), true
	}
	out, isErr := marshal(r)
	if isErr {
		return out, true
	}
	return "Repo updated.\n" + out, false
}

func (c *Client) detectRepoRuntime(args json.RawMessage) (string, bool) {
	id, errMsg := repoArg(args)
	if errMsg != "" {
		return errMsg, true
	}
	var raw json.RawMessage
	if status, err := c.do(http.MethodPost, "/api/v1/repos/"+url.PathEscape(id)+"/runtime/detect", nil, &raw); err != nil {
		return fmt.Sprintf("detect_repo_runtime failed (%d): %s", status, err.Error()), true
	}
	return truncate(string(raw)), false
}

func (c *Client) listAgents(_ json.RawMessage) (string, bool) {
	var agents []struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Labels         string `json:"labels"`
		Enabled        bool   `json:"enabled"`
		ProviderConfig *struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"provider_config"`
	}
	if _, err := c.do(http.MethodGet, "/api/v1/agents", nil, &agents); err != nil {
		return "failed to list agents: " + err.Error(), true
	}
	out := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		e := map[string]any{"id": a.ID, "name": a.Name, "labels": a.Labels, "enabled": a.Enabled}
		if a.ProviderConfig != nil {
			e["provider"], e["model"] = a.ProviderConfig.Provider, a.ProviderConfig.Model
		}
		out = append(out, e)
	}
	return marshal(out)
}

func (c *Client) getWorkflow(args json.RawMessage) (string, bool) {
	id, errMsg := workflowArg(args)
	if errMsg != "" {
		return errMsg, true
	}
	data, status, err := c.send(http.MethodGet, "/api/v1/workflows/"+url.PathEscape(id)+"/export.yaml", "", nil)
	if err != nil {
		return fmt.Sprintf("get_workflow failed (%d): %s", status, err.Error()), true
	}
	return truncate(fmt.Sprintf("Workflow %s:\n%s", id, data)), false
}

func (c *Client) createWorkflow(args json.RawMessage) (string, bool) {
	var a struct {
		YAML string `json:"yaml"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.YAML) == "" {
		return "yaml is required", true
	}
	data, status, err := c.send(http.MethodPost, "/api/v1/workflows/import", "application/yaml", []byte(a.YAML))
	if err != nil {
		return fmt.Sprintf("create_workflow failed (%d): %s", status, err.Error()), true
	}
	return workflowSummary("Created", data)
}

func (c *Client) updateWorkflow(args json.RawMessage) (string, bool) {
	var a struct {
		WorkflowID string `json:"workflow_id"`
		YAML       string `json:"yaml"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.WorkflowID) == "" || strings.TrimSpace(a.YAML) == "" {
		return "workflow_id and yaml are required", true
	}
	if msg := c.droppedLabelsInUse(a.WorkflowID, a.YAML); msg != "" {
		return msg, true
	}
	path := "/api/v1/workflows/" + url.PathEscape(a.WorkflowID) + "/yaml"
	data, status, err := c.send(http.MethodPut, path, "application/yaml", []byte(a.YAML))
	if err != nil {
		return fmt.Sprintf("update_workflow failed (%d): %s", status, err.Error()), true
	}
	return workflowSummary("Updated", data)
}

// droppedLabelsInUse returns a refusal when the new document removes a label
// that tasks in this workflow are still on: the update recreates labels by
// name, so those tasks would sit on a column that no longer exists.
func (c *Client) droppedLabelsInUse(workflowID, doc string) string {
	var next struct {
		Labels []struct {
			Name string `yaml:"name"`
		} `yaml:"labels"`
	}
	if err := yaml.Unmarshal([]byte(doc), &next); err != nil {
		return "invalid yaml: " + err.Error()
	}
	keep := map[string]bool{}
	for _, l := range next.Labels {
		keep[l.Name] = true
	}
	var cur struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if status, err := c.do(http.MethodGet, "/api/v1/workflows/"+url.PathEscape(workflowID), nil, &cur); err != nil {
		return fmt.Sprintf("update_workflow failed (%d): %s", status, err.Error())
	}
	var busy []string
	for _, l := range cur.Labels {
		if keep[l.Name] {
			continue
		}
		var tasks []struct {
			WorkflowID string `json:"workflow_id"`
		}
		qs := url.Values{"label": {l.Name}, "archived": {"all"}, "limit": {"500"}}
		if _, err := c.do(http.MethodGet, "/api/v1/tasks?"+qs.Encode(), nil, &tasks); err != nil {
			return "update_workflow: failed to check tasks on label " + l.Name + ": " + err.Error()
		}
		n := 0
		for _, t := range tasks {
			if t.WorkflowID == workflowID {
				n++
			}
		}
		if n > 0 {
			busy = append(busy, fmt.Sprintf("%q (%d tasks)", l.Name, n))
		}
	}
	if len(busy) == 0 {
		return ""
	}
	slices.Sort(busy)
	return "update_workflow refused: it removes labels that still hold tasks: " + strings.Join(busy, ", ") +
		". Keep those labels, or move or archive their tasks first."
}

func workflowSummary(verb string, data []byte) (string, bool) {
	var wf struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		Transitions []json.RawMessage `json:"transitions"`
	}
	if err := json.Unmarshal(data, &wf); err != nil {
		return "failed to decode workflow: " + err.Error(), true
	}
	names := make([]string, 0, len(wf.Labels))
	for _, l := range wf.Labels {
		names = append(names, l.Name)
	}
	return fmt.Sprintf("%s workflow %q (%s) with labels [%s] and %d transitions.",
		verb, wf.Name, wf.ID, strings.Join(names, ", "), len(wf.Transitions)), false
}

func workflowArg(args json.RawMessage) (string, string) {
	var a struct {
		WorkflowID string `json:"workflow_id"`
	}
	_ = json.Unmarshal(args, &a)
	if strings.TrimSpace(a.WorkflowID) == "" {
		return "", "workflow_id is required"
	}
	return a.WorkflowID, ""
}
