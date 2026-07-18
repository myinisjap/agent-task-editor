package providers

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
)

// ClaudePlugin describes a plugin installed on this machine, as discovered
// from ~/.claude/plugins/installed_plugins.json. ID is the raw key used by
// Claude Code ("<name>@<marketplace>"); Name/Marketplace are split out for
// display purposes.
type ClaudePlugin struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Marketplace string `json:"marketplace"`
}

// installedPluginsFile mirrors the shape of ~/.claude/plugins/installed_plugins.json.
// The value type of "plugins" is irrelevant to us — we only need the keys — but
// we decode into json.RawMessage to tolerate whatever shape is present without
// needing to fully model it.
type installedPluginsFile struct {
	Plugins map[string]json.RawMessage `json:"plugins"`
}

// ListInstalledClaudePlugins returns the plugins installed in the current
// user's Claude home directory (~/.claude/plugins/installed_plugins.json).
// Returns an empty (not error) slice if the file is missing or unparseable,
// so environments without the Claude plugin system still work.
func ListInstalledClaudePlugins() ([]ClaudePlugin, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	return listInstalledClaudePluginsFrom(home), nil
}

// listInstalledClaudePluginsFrom is the testable core of ListInstalledClaudePlugins,
// accepting an explicit home directory.
func listInstalledClaudePluginsFrom(home string) []ClaudePlugin {
	data, err := os.ReadFile(home + "/.claude/plugins/installed_plugins.json")
	if err != nil {
		return []ClaudePlugin{}
	}
	var parsed installedPluginsFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return []ClaudePlugin{}
	}
	plugins := make([]ClaudePlugin, 0, len(parsed.Plugins))
	for id := range parsed.Plugins {
		name, marketplace := splitPluginID(id)
		plugins = append(plugins, ClaudePlugin{ID: id, Name: name, Marketplace: marketplace})
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	return plugins
}

// splitPluginID splits a "<name>@<marketplace>" plugin ID on the last "@".
// If there is no "@", the whole ID is returned as the name with an empty marketplace.
func splitPluginID(id string) (name, marketplace string) {
	idx := strings.LastIndex(id, "@")
	if idx < 0 {
		return id, ""
	}
	return id[:idx], id[idx+1:]
}

// claudeJSONFile mirrors the top-level shape of ~/.claude.json that we care about:
// the global (user-level) mcpServers map. Project-scoped servers under
// projects[path].mcpServers are intentionally not read here.
type claudeJSONFile struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

// ListAvailableClaudeMCPServers returns the names of user-level MCP servers
// configured in ~/.claude.json. Returns an empty (not error) slice if the
// file is missing or unparseable.
func ListAvailableClaudeMCPServers() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	return listAvailableClaudeMCPServersFrom(home), nil
}

// listAvailableClaudeMCPServersFrom is the testable core of
// ListAvailableClaudeMCPServers, accepting an explicit home directory.
func listAvailableClaudeMCPServersFrom(home string) []string {
	data, err := os.ReadFile(home + "/.claude.json")
	if err != nil {
		return []string{}
	}
	var parsed claudeJSONFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return []string{}
	}
	names := make([]string, 0, len(parsed.MCPServers))
	for name := range parsed.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// rawMCPServerConfigsFrom reads the global mcpServers map from ~/.claude.json
// and returns the raw JSON entries for the requested server names (skipping
// any name not found, or named "task-editor" to avoid colliding with the
// task-editor sidecar entry).
func rawMCPServerConfigsFrom(home string, names []string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if len(names) == 0 {
		return out
	}
	data, err := os.ReadFile(home + "/.claude.json")
	if err != nil {
		return out
	}
	var parsed claudeJSONFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		return out
	}
	for _, name := range names {
		if name == "task-editor" {
			continue
		}
		if entry, ok := parsed.MCPServers[name]; ok {
			out[name] = entry
		}
	}
	return out
}
