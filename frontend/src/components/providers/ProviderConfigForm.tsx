import type { Dispatch, ReactNode, SetStateAction } from 'react'
import type { ModelList, ProviderConfig } from '../../api/client'
import { PROVIDERS } from '../../lib/agentTemplates'
import { DEPRECATED_PROVIDERS, PROVIDER_CAPABILITIES, type Capability } from '../../lib/providerCapabilities'
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

  // An existing config may carry a provider that's been removed from the
  // dropdown (anthropic/llm/openai, deprecated) or that's otherwise unknown.
  // If we only ever render <option>s from PROVIDERS, the <select>'s value
  // matches none of them, the browser blanks it or snaps to the first entry,
  // and saving any unrelated field silently rewrites provider to whatever
  // that first entry is. Render an extra, disabled option for the current
  // value so it round-trips instead.
  const isKnownDropdownProvider = PROVIDERS.includes(form.provider)
  const isDeprecated = DEPRECATED_PROVIDERS.has(form.provider)

  // Env values the server returned as the redacted "***" sentinel (see
  // provider_env.go) — the only thing we can safely show for a set-but-
  // hidden secret is its key name, with an explicit way to remove it. Parse
  // failures (a user mid-edit of the raw JSON textarea) just render no
  // chips rather than erroring.
  const maskedEnvKeys: string[] = (() => {
    try {
      const parsed = JSON.parse(form.env) as Record<string, unknown>
      return Object.entries(parsed)
        .filter(([, v]) => v === '***')
        .map(([k]) => k)
    } catch {
      return []
    }
  })()

  function removeEnvKey(key: string) {
    try {
      const parsed = JSON.parse(form.env) as Record<string, unknown>
      delete parsed[key]
      setForm((f) => ({ ...f, env: JSON.stringify(parsed) }))
    } catch {
      // form.env isn't valid JSON right now; nothing safe to do.
    }
  }

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
        <Field label="Name" className="sm:col-span-2">
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
            {!isKnownDropdownProvider && form.provider && (
              <option key={form.provider} value={form.provider} disabled>
                {form.provider} (deprecated)
              </option>
            )}
            {PROVIDERS.map((p) => (
              <option key={p} value={p}>{p}</option>
            ))}
          </select>
          {isDeprecated && (
            <p className="mt-1 text-xs text-amber-400">
              ⚠️ The <code>{form.provider}</code> provider is deprecated, disabled for new configs, and may be removed
              in a future release. This config will keep running, but should be migrated to a supported provider.
            </p>
          )}
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

        <Field label="Env vars (JSON object)" className="sm:col-span-2" hint="API keys and other environment variables merged into the provider CLI's environment.">
          {maskedEnvKeys.length > 0 && (
            <div className="mb-2 flex flex-wrap gap-1.5">
              {maskedEnvKeys.map((key) => (
                <span
                  key={key}
                  className="inline-flex items-center gap-1 text-xs font-mono px-2 py-0.5 rounded bg-slate-800 text-slate-300 border border-slate-700"
                >
                  {key}
                  <button
                    type="button"
                    onClick={() => removeEnvKey(key)}
                    aria-label={`Remove ${key}`}
                    title={`Remove ${key}`}
                    className="text-slate-500 hover:text-red-400"
                  >
                    ✕
                  </button>
                </span>
              ))}
            </div>
          )}
          <textarea
            value={form.env}
            onChange={(e) => setForm((f) => ({ ...f, env: e.target.value }))}
            rows={4}
            className="input resize-none font-mono text-xs"
            placeholder='{"ANTHROPIC_API_KEY": "..."}'
          />
          {selected && maskedEnvKeys.length > 0 && (
            <p className="mt-1 text-xs text-slate-500">
              Values shown as *** are set but hidden. Replace *** with a new value to update a key; delete the whole
              key line (or click ✕ above) to remove it.
            </p>
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
