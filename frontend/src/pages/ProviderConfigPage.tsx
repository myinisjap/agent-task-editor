import { useEffect, useState } from 'react'
import { useProviderConfigsStore } from '../stores/providerConfigs'
import { useAgentsStore } from '../stores/agents'
import type { ProviderConfig, ModelList } from '../api/client'
import ProviderConfigForm, { type FormState } from '../components/providers/ProviderConfigForm'

const EMPTY: FormState = { name: '', provider: 'claude', model: 'sonnet', env: '{}' }

export default function ProviderConfigPage() {
  const { configs, fetch: fetchConfigs, create, update, delete: deleteConfig } = useProviderConfigsStore()
  const { fetchModels } = useAgentsStore()
  const [selected, setSelected] = useState<ProviderConfig | null>(null)
  const [form, setForm] = useState<FormState>(EMPTY)
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [modelList, setModelList] = useState<ModelList | null>(null)
  const [fetchingModels, setFetchingModels] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false)

  useEffect(() => {
    fetchConfigs()
  }, [fetchConfigs])

  useEffect(() => {
    if (form.provider === 'claude') {
      setModelList(null)
      return
    }
    setFetchingModels(true)
    fetchModels(form.provider).then((data) => {
      setModelList(data)
      setFetchingModels(false)
      if (data && form.model === '') {
        setForm((f) => ({ ...f, model: data.default_model }))
      }
    })
  }, [form.provider]) // eslint-disable-line react-hooks/exhaustive-deps

  function selectConfig(pc: ProviderConfig) {
    setSelected(pc)
    setForm({ name: pc.name, provider: pc.provider, model: pc.model, env: pc.env })
  }

  function newConfig() {
    setSelected(null)
    setForm(EMPTY)
  }

  function sanitizeEnv(envJson: string): string {
    try {
      const parsed = JSON.parse(envJson) as Record<string, string>
      const clean: Record<string, string> = {}
      for (const [k, v] of Object.entries(parsed)) {
        if (v !== '***' && v !== '') clean[k] = v
      }
      return JSON.stringify(clean)
    } catch {
      return envJson
    }
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = { ...form, env: sanitizeEnv(form.env) }
      if (selected) {
        await update(selected.id, payload)
      } else {
        await create(payload)
      }
      newConfig()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    if (!selected) return
    if (!confirm(`Delete provider config "${selected.name}"?`)) return
    setDeleting(true)
    try {
      await deleteConfig(selected.id)
      newConfig()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      <div className="md:hidden flex items-center justify-between px-4 py-2 border-b border-slate-800 bg-slate-950">
        <span className="text-sm text-slate-300 truncate">
          {selected ? selected.name : 'New provider config'}
        </span>
        <button
          onClick={() => setSidebarOpen(true)}
          className="text-xs px-2 py-1 rounded bg-slate-700 hover:bg-slate-600 text-slate-300"
        >
          Configs
        </button>
      </div>

      <div className="flex-1 flex overflow-hidden">
        {sidebarOpen && (
          <div className="fixed inset-0 bg-black/50 z-30 md:hidden" onClick={() => setSidebarOpen(false)} />
        )}
        <div
          className={`fixed inset-y-0 left-0 z-40 w-64 max-w-[80vw] bg-slate-950 border-r border-slate-800 overflow-y-auto flex flex-col transition-transform duration-200 ease-in-out
            md:static md:z-auto md:w-56 md:max-w-none md:translate-x-0
            ${sidebarOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'}`}
        >
          <div className="p-4 flex items-center justify-between border-b border-slate-800">
            <span className="text-sm font-medium text-slate-300">Provider Configs</span>
            <div className="flex items-center gap-1.5">
              <button
                onClick={() => { newConfig(); setSidebarOpen(false) }}
                className="text-xs px-2 py-1 rounded bg-indigo-700 hover:bg-indigo-600 text-white"
              >
                + New
              </button>
              <button
                onClick={() => setSidebarOpen(false)}
                aria-label="Close configs"
                className="md:hidden text-slate-400 hover:text-slate-100 p-1 rounded"
              >
                ✕
              </button>
            </div>
          </div>

          <div className="flex flex-col gap-0.5 p-2">
            {configs.map((pc) => (
              <button
                key={pc.id}
                onClick={() => { selectConfig(pc); setSidebarOpen(false) }}
                className={`w-full text-left text-sm px-3 py-2 rounded ${
                  selected?.id === pc.id
                    ? 'bg-slate-700 text-slate-100'
                    : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
                }`}
              >
                <div className="truncate">{pc.name}</div>
                <div className="text-xs text-slate-500 mt-0.5">{pc.provider}{pc.model ? `/${pc.model}` : ''}</div>
              </button>
            ))}
            {configs.length === 0 && (
              <p className="text-xs text-slate-600 px-3 py-4">No provider configs yet</p>
            )}
          </div>
        </div>

        <ProviderConfigForm
          selected={selected}
          form={form}
          setForm={setForm}
          modelList={modelList}
          fetchingModels={fetchingModels}
          saving={saving}
          deleting={deleting}
          onSave={handleSave}
          onDelete={handleDelete}
        />
      </div>
    </div>
  )
}
