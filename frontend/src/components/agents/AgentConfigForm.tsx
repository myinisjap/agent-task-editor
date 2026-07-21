import type { Dispatch, SetStateAction } from 'react'
import { Link } from 'react-router-dom'
import type { AgentConfig, ClaudeOptions, ProviderConfig } from '../../api/client'
import Field from './Field'
import PluginMcpPicker from './PluginMcpPicker'
import CommandFilterEditor from './CommandFilterEditor'
import LabelPicker from './LabelPicker'

export type FormState = Omit<AgentConfig, 'id' | 'created_at' | 'updated_at' | 'enabled' | 'provider_config'>

export default function AgentConfigForm({
  selected,
  form,
  setForm,
  availableLabels,
  providerConfigs,
  claudeOptions,
  saving,
  deleting,
  onSave,
  onDelete,
  onToggleEnabled,
}: {
  selected: AgentConfig | null
  form: FormState
  setForm: Dispatch<SetStateAction<FormState>>
  availableLabels: string[]
  providerConfigs: ProviderConfig[]
  claudeOptions: ClaudeOptions | null
  saving: boolean
  deleting: boolean
  onSave: () => void
  onDelete: () => void
  onToggleEnabled: () => void
}) {
  const isEnabled = selected ? selected.enabled : true
  // The claude-specific plugin/MCP picker and the command-filter hints are
  // both driven by the *selected provider config's* provider string, not a
  // form field — provider/model/env now live on the referenced ProviderConfig.
  const selectedProviderConfig = providerConfigs.find((pc) => pc.id === form.provider_config_id)
  const providerStr = selectedProviderConfig?.provider ?? ''

  return (
    <div className="flex-1 overflow-y-auto p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-base font-semibold text-slate-100">
          {selected ? `Edit: ${selected.name}` : 'New Agent Config'}
        </h2>
        {selected && (
          <div className="flex items-center gap-2">
            <span className={`text-xs ${isEnabled ? 'text-green-400' : 'text-slate-500'}`}>
              {isEnabled ? 'Active' : 'Disabled'}
            </span>
            <button
              onClick={onToggleEnabled}
              disabled={saving}
              className={`relative w-9 h-5 rounded-full transition-colors ${isEnabled ? 'bg-green-600' : 'bg-slate-700'} disabled:opacity-50`}
            >
              <span className={`absolute left-0 top-1/2 w-4 h-4 rounded-full bg-white shadow transition-transform -translate-y-1/2 ${isEnabled ? 'translate-x-5' : 'translate-x-0.5'}`} />
            </button>
          </div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-5 max-w-2xl">
        <Field label="Name">
          <input
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            className="input"
            placeholder="e.g. Code Agent"
          />
        </Field>

        <Field label="Provider config" className="col-span-2" hint="Which provider/model/API key this agent runs against. Manage provider configs (and their env vars) on the Providers page — they can be shared across agent configs and chat sessions.">
          <select
            value={form.provider_config_id}
            onChange={(e) => setForm((f) => ({ ...f, provider_config_id: e.target.value }))}
            className="input"
          >
            <option value="">Select a provider config…</option>
            {providerConfigs.map((pc) => (
              <option key={pc.id} value={pc.id}>{pc.name} ({pc.provider}{pc.model ? `/${pc.model}` : ''})</option>
            ))}
          </select>
          <p className="mt-1 text-xs text-slate-500">
            <Link to="/providers" className="text-indigo-400 hover:text-indigo-300">Manage provider configs →</Link>
          </p>
        </Field>

        <Field label="Timeout (secs)">
          <input
            type="number"
            value={form.timeout_secs}
            onChange={(e) => setForm((f) => ({ ...f, timeout_secs: Number(e.target.value) }))}
            className="input"
            min={30}
            max={3600}
          />
        </Field>

        <Field label="Max tokens">
          <input
            type="number"
            value={form.max_tokens}
            onChange={(e) => setForm((f) => ({ ...f, max_tokens: Number(e.target.value) }))}
            className="input"
            min={256}
            max={200000}
          />
        </Field>

        <Field label="Max turns">
          <input
            type="number"
            value={form.max_turns}
            onChange={(e) => setForm((f) => ({ ...f, max_turns: Number(e.target.value) }))}
            className="input"
            min={1}
            max={200}
          />
        </Field>

        <Field label="Max retries" hint="Auto-retries for transient errors (rate limits, network blips). 0 disables auto-retry.">
          <input
            type="number"
            value={form.max_retries}
            onChange={(e) => setForm((f) => ({ ...f, max_retries: Number(e.target.value) }))}
            className="input"
            min={0}
            max={10}
          />
        </Field>

        <Field label="Priority" hint="Lower runs first; higher-priority-number configs on the same label act as backups when the primary is rate-limited.">
          <input
            type="number"
            value={form.priority ?? 0}
            onChange={(e) => setForm((f) => ({ ...f, priority: Number(e.target.value) }))}
            className="input"
          />
        </Field>

        <Field label="Retry backoff (secs)" hint="Base backoff before a retry is re-dispatched; doubles each attempt, capped at 10 min.">
          <input
            type="number"
            value={form.retry_backoff_secs}
            onChange={(e) => setForm((f) => ({ ...f, retry_backoff_secs: Number(e.target.value) }))}
            className="input"
            min={1}
            max={600}
          />
        </Field>

        <Field label="Max cost per run (USD)" hint="Advisory per-task budget cap in USD, checked by the dispatcher before each dispatch against the task's cumulative run cost so far. 0 disables the cap (unlimited). Not a mid-run kill switch — costs are only known after a run completes.">
          <input
            type="number"
            step="0.01"
            value={form.max_cost_usd}
            onChange={(e) => setForm((f) => ({ ...f, max_cost_usd: Number(e.target.value) }))}
            className="input"
            min={0}
          />
        </Field>

        <Field label="Resume sessions" hint="Claude provider only: re-runs on the same task continue the previous run's session (full prior context) instead of starting cold. Turn off for stages that should review with fresh eyes.">
          <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
            <input
              type="checkbox"
              checked={form.resume_sessions ?? true}
              onChange={(e) => setForm((f) => ({ ...f, resume_sessions: e.target.checked }))}
            />
            Resume previous session on re-runs
          </label>
        </Field>

        <Field label="Subtasks" hint="claude/qwen_code/gemini_cli/codex_cli only: expose the create_subtask tool so this agent (typically the planner) can decompose its task into child tasks. Children branch off the parent's branch and merge back automatically. Off by default.">
          <label className="flex items-center gap-2 text-sm text-slate-300 cursor-pointer">
            <input
              type="checkbox"
              checked={form.subtasks_enabled ?? false}
              onChange={(e) => setForm((f) => ({ ...f, subtasks_enabled: e.target.checked }))}
            />
            Allow this agent to create subtasks
          </label>
          {form.subtasks_enabled && (
            <label className="flex items-center gap-2 text-xs text-slate-400 mt-2">
              Max subtasks per task
              <input
                type="number"
                min={1}
                value={form.max_subtasks ?? 10}
                onChange={(e) => setForm((f) => ({ ...f, max_subtasks: Number(e.target.value) }))}
                className="w-20 bg-slate-800 border border-slate-600 rounded px-2 py-1 text-slate-100"
              />
            </label>
          )}
        </Field>

        <Field label="Labels" className="col-span-2">
          <LabelPicker
            selected={(() => { try { return JSON.parse(form.labels) } catch { return [] } })()}
            available={availableLabels}
            onChange={(lbls) => setForm((f) => ({ ...f, labels: JSON.stringify(lbls) }))}
          />
        </Field>

        {providerStr === 'claude' && (
          <PluginMcpPicker
            claudeOptions={claudeOptions}
            enabledPlugins={form.enabled_plugins ?? '[]'}
            enabledMcpServers={form.enabled_mcp_servers ?? '[]'}
            onPluginsChange={(json) => setForm((f) => ({ ...f, enabled_plugins: json }))}
            onMcpServersChange={(json) => setForm((f) => ({ ...f, enabled_mcp_servers: json }))}
          />
        )}

        <Field label="System prompt" className="col-span-2">
          <textarea
            value={form.system_prompt}
            onChange={(e) => setForm((f) => ({ ...f, system_prompt: e.target.value }))}
            rows={8}
            className="input resize-none font-mono text-xs"
            placeholder="You are an expert software engineer…"
          />
        </Field>

        <CommandFilterEditor
          provider={providerStr}
          allowlist={form.command_allowlist ?? '[]'}
          denylist={form.command_denylist ?? '[]'}
          onAllowlistChange={(v) => setForm((f) => ({ ...f, command_allowlist: v }))}
          onDenylistChange={(v) => setForm((f) => ({ ...f, command_denylist: v }))}
        />
      </div>

      <div className="flex gap-3 mt-6">
        <button
          onClick={onSave}
          disabled={saving || !form.name.trim() || !form.provider_config_id}
          className="px-5 py-2 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
        >
          {saving ? 'Saving…' : selected ? 'Update' : 'Create'}
        </button>
        {selected && (
          <button
            onClick={onDelete}
            disabled={deleting}
            className="px-5 py-2 text-sm font-medium rounded bg-red-800 hover:bg-red-700 text-white disabled:opacity-50"
          >
            {deleting ? 'Deleting…' : 'Delete'}
          </button>
        )}
      </div>
    </div>
  )
}
