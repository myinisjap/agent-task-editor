import type { Dispatch, ReactNode, SetStateAction } from 'react'
import type { ModelList, ProviderConfig } from '../../api/client'
import { PROVIDERS } from '../../lib/agentTemplates'
import { PROVIDER_CAPABILITIES, type Capability } from '../../lib/providerCapabilities'
import Field from '../agents/Field'
import ModelSelector from '../agents/ModelSelector'

export type FormState = Omit<ProviderConfig, 'id' | 'created_at' | 'updated_at'>

// Display labels for the capability-gap summary shown under the provider
// dropdown. Subset of Capability — only the ones worth flagging at a glance
// when picking a provider; command allow/denylist and detailed notes are
// covered per-field elsewhere (AgentConfigForm, CommandFilterEditor).
const GAP_SUMMARY_CAPS: { key: Capability; label: string }[] = [
  { key: 'labelTransitions', label: 'workflow label transitions' },
  { key: 'mcpServers', label: 'MCP servers / plugins' },
  { key: 'costTracking', label: 'cost tracking' },
  { key: 'commandAllowlist', label: 'command allowlist' },
  { key: 'commandDenylist', label: 'command denylist' },
  { key: 'subtasks', label: 'subtasks' },
  { key: 'sessionResume', label: 'session resume' },
]

export default function ProviderConfigForm({
  selected,
  form,
  setForm,
  modelList,
  fetchingModels,
  saving,
  deleting,
  onSave,
  onDelete,
  helpButton,
}: {
  selected: ProviderConfig | null
  form: FormState
  setForm: Dispatch<SetStateAction<FormState>>
  modelList: ModelList | null
  fetchingModels: boolean
  saving: boolean
  deleting: boolean
  onSave: () => void
  onDelete: () => void
  helpButton?: ReactNode
}) {
  const caps = PROVIDER_CAPABILITIES[form.provider]
  const gaps = caps
    ? GAP_SUMMARY_CAPS.filter(({ key }) => (caps[key]?.support ?? 'none') !== 'full').map(({ label }) => label)
    : []

  return (
    <div className="flex-1 overflow-y-auto p-6">
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-2">
          <h2 className="text-base font-semibold text-slate-100">
            {selected ? `Edit: ${selected.name}` : 'New Provider Config'}
          </h2>
          {helpButton}
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-5 max-w-2xl">
        <Field label="Name" className="col-span-2">
          <input
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            className="input"
            placeholder="e.g. Claude (main account)"
          />
        </Field>

        <Field label="Provider">
          <select
            value={form.provider}
            onChange={(e) => setForm((f) => ({ ...f, provider: e.target.value as FormState['provider'] }))}
            className="input"
          >
            {PROVIDERS.map((p) => (
              <option key={p} value={p}>{p}</option>
            ))}
          </select>
          {gaps.length > 0 && (
            <p className="mt-1 text-xs text-amber-400">
              ⚠️ Not fully supported: {gaps.join(', ')}. See{' '}
              <a
                href="https://github.com/myinisjap/agent-task-editor/blob/main/docs/agents.md#capability-matrix"
                target="_blank"
                rel="noreferrer"
                className="underline hover:text-amber-300"
              >
                docs/agents.md
              </a>.
            </p>
          )}
        </Field>

        <Field label="Model">
          <ModelSelector
            provider={form.provider}
            model={form.model}
            onChange={(model) => setForm((f) => ({ ...f, model }))}
            modelList={modelList}
            fetchingModels={fetchingModels}
          />
        </Field>

        <Field label="Env vars (JSON object)" className="col-span-2" hint="API keys and other environment variables merged into the provider CLI's environment.">
          <textarea
            value={form.env}
            onChange={(e) => setForm((f) => ({ ...f, env: e.target.value }))}
            rows={4}
            className="input resize-none font-mono text-xs"
            placeholder='{"ANTHROPIC_API_KEY": "..."}'
          />
          {selected && /"\*\*\*"/.test(form.env) && (
            <p className="mt-1 text-xs text-slate-500">Keys showing *** are already set. Clear or replace the value to update; leave *** to keep existing.</p>
          )}
        </Field>
      </div>

      <div className="flex gap-3 mt-6">
        <button
          onClick={onSave}
          disabled={saving || !form.name.trim() || !form.provider.trim()}
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
