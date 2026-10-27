import { useEffect, useState } from 'react'
import { useProviderConfigsStore } from '../stores/providerConfigs'
import { useAgentsStore } from '../stores/agents'
import type { ProviderConfig, ModelList } from '../api/client'
import ProviderConfigForm, { type FormState } from '../components/providers/ProviderConfigForm'
import HelpModal from '../components/shared/HelpModal'
import { X } from 'lucide-react'
import { Button, Icon, PageFrame } from '../components/ui'
import { ProvidersHelp } from '../components/shared/pageHelp'

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
  const [showHelp, setShowHelp] = useState(false)

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

  async function handleSave() {
    setSaving(true)
    try {
      // form.env is sent as-is. The server owns env's write-only merge
      // semantics (see backend/internal/api/handlers/provider_env.go): a
      // value of "***" means "keep the existing stored value for this
      // key", any other value (including "") overwrites, and a key present
      // on the server but omitted here is deleted. Stripping "***" entries
      // client-side (as this used to do) would delete every key the user
      // didn't touch — do not reintroduce that. A malformed env string is
      // passed straight through too, so the server's 400 (rather than a
      // silently-swallowed client-side parse failure) is what the user sees.
      const payload = { ...form }
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
    <PageFrame
      title="Providers"
      testId="providers-page"
      help={{ onClick: () => setShowHelp(true), title: 'About providers' }}
      actions={
        <Button size="sm" className="md:hidden" onClick={() => setSidebarOpen(true)}>
          Configs
        </Button>
      }
    >
      <div className="flex items-start gap-6">
        {sidebarOpen && (
          <div className="fixed inset-0 bg-black/50 z-30 md:hidden" onClick={() => setSidebarOpen(false)} />
        )}
        <div
          className={`fixed inset-y-0 left-0 z-40 w-64 max-w-[80vw] bg-surface border-r border-border overflow-y-auto flex flex-col transition-transform duration-200 ease-in-out
            md:sticky md:top-0 md:self-start md:max-h-[calc(100vh-8rem)] md:z-auto md:w-56 md:shrink-0 md:max-w-none md:translate-x-0 md:rounded-lg
            ${sidebarOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0'}`}
        >
          <div className="p-4 flex items-center justify-between border-b border-border">
            <span className="text-sm font-medium text-fg-muted">Provider Configs</span>
            <div className="flex items-center gap-1.5">
              <button
                onClick={() => { newConfig(); setSidebarOpen(false) }}
                className="text-xs px-2 py-1 rounded bg-accent hover:bg-accent/90 text-on-accent"
              >
                + New
              </button>
              <button
                onClick={() => setSidebarOpen(false)}
                aria-label="Close configs"
                className="md:hidden text-fg-muted hover:text-fg p-1 rounded"
              >
                <Icon icon={X} size="md" />
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
                    ? 'bg-border text-fg'
                    : 'text-fg-muted hover:bg-surface-sunken hover:text-fg'
                }`}
              >
                <div className="truncate">{pc.name}</div>
                <div className="text-xs text-fg-subtle mt-0.5">{pc.provider}{pc.model ? `/${pc.model}` : ''}</div>
              </button>
            ))}
            {configs.length === 0 && (
              <p className="text-xs text-fg-subtle px-3 py-4">No provider configs yet</p>
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

      {showHelp && (
        <HelpModal title="About Providers" onClose={() => setShowHelp(false)}>
          <ProvidersHelp />
        </HelpModal>
      )}
    </PageFrame>
  )
}
