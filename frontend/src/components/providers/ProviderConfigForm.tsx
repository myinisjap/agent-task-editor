import type { Dispatch, SetStateAction } from 'react'
import type { ModelList, ProviderConfig } from '../../api/client'
import { PROVIDERS } from '../../lib/agentTemplates'
import Field from '../agents/Field'
import ModelSelector from '../agents/ModelSelector'

export type FormState = Omit<ProviderConfig, 'id' | 'created_at' | 'updated_at'>

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
}) {
  return (
    <div className="flex-1 overflow-y-auto p-6">
      <div className="flex items-center justify-between mb-6">
        <h2 className="text-base font-semibold text-slate-100">
          {selected ? `Edit: ${selected.name}` : 'New Provider Config'}
        </h2>
      </div>

      <div className="grid grid-cols-2 gap-5 max-w-2xl">
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
            onChange={(e) => setForm((f) => ({ ...f, provider: e.target.value }))}
            className="input"
          >
            {PROVIDERS.map((p) => (
              <option key={p} value={p}>{p}</option>
            ))}
          </select>
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
