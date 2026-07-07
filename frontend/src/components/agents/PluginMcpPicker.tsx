import type { ClaudeOptions } from '../../api/client'
import ChipPicker from '../shared/ChipPicker'
import Field from './Field'

// PluginMcpPicker renders the "Plugins" and "MCP servers" pickers driven by
// claudeOptions. Only meaningful for the claude provider — the parent decides
// whether to render this at all (it doesn't need to know about `provider`).
export default function PluginMcpPicker({
  claudeOptions,
  enabledPlugins,
  enabledMcpServers,
  onPluginsChange,
  onMcpServersChange,
}: {
  claudeOptions: ClaudeOptions | null
  enabledPlugins: string
  enabledMcpServers: string
  onPluginsChange: (json: string) => void
  onMcpServersChange: (json: string) => void
}) {
  const selectedPlugins = (() => { try { return JSON.parse(enabledPlugins ?? '[]') } catch { return [] } })()
  const selectedMcpServers = (() => { try { return JSON.parse(enabledMcpServers ?? '[]') } catch { return [] } })()

  return (
    <>
      <Field label="Plugins" className="col-span-2">
        <ChipPicker
          selected={selectedPlugins}
          available={(claudeOptions?.plugins ?? []).map((p) => ({ value: p.id, label: p.marketplace ? `${p.name} (${p.marketplace})` : p.name }))}
          onChange={(ids) => onPluginsChange(JSON.stringify(ids))}
          emptyMessage="No plugins found in ~/.claude/plugins/installed_plugins.json."
        />
        <p className="mt-1 text-xs text-slate-500">Discovered from your Claude home dir. Off by default — toggle to enable per agent.</p>
      </Field>

      <Field label="MCP servers" className="col-span-2">
        <ChipPicker
          selected={selectedMcpServers}
          available={(claudeOptions?.mcp_servers ?? []).map((name) => ({ value: name, label: name }))}
          onChange={(names) => onMcpServersChange(JSON.stringify(names))}
          emptyMessage="No user-level MCP servers found in ~/.claude.json."
        />
        <p className="mt-1 text-xs text-slate-500">Only global (user-level) MCP servers are listed; project-scoped servers aren't included.</p>
      </Field>
    </>
  )
}
