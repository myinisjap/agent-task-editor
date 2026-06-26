import { useEffect, useState } from 'react'
import { api, type AgentConfig } from '../api/client'
import { useAgentsStore } from '../stores/agents'
import { useWorkflowStore } from '../stores/workflow'

const EMPTY: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at'> = {
  name: '',
  provider: 'claude',
  model: 'claude-sonnet-4-6',
  system_prompt: '',
  labels: '[]',
  env: '{}',
  max_tokens: 8192,
  timeout_secs: 600,
}

export default function AgentConfigPage() {
  const { configs: agents, fetch: fetchAgents } = useAgentsStore()
  const { workflows, fetch: fetchWorkflows } = useWorkflowStore()
  const [selected, setSelected] = useState<AgentConfig | null>(null)
  const [form, setForm] = useState<typeof EMPTY>(EMPTY)
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)

  const availableLabels = workflows[0]?.labels.map((l) => l.name) ?? []

  useEffect(() => {
    fetchAgents()
    fetchWorkflows()
  }, [fetchAgents, fetchWorkflows])

  function selectAgent(a: AgentConfig) {
    setSelected(a)
    setForm({
      name: a.name,
      provider: a.provider,
      model: a.model,
      system_prompt: a.system_prompt,
      labels: a.labels,
      env: a.env,
      max_tokens: a.max_tokens,
      timeout_secs: a.timeout_secs,
    })
  }

  function newAgent() {
    setSelected(null)
    setForm(EMPTY)
  }

  async function handleSave() {
    setSaving(true)
    try {
      if (selected) {
        await api.agents.update(selected.id, form)
      } else {
        await api.agents.create(form)
      }
      fetchAgents()
      newAgent()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    if (!selected) return
    if (!confirm(`Delete agent "${selected.name}"?`)) return
    setDeleting(true)
    try {
      await api.agents.delete(selected.id)
      fetchAgents()
      newAgent()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="flex h-full overflow-hidden">
      {/* Sidebar */}
      <div className="w-56 shrink-0 border-r border-slate-800 overflow-y-auto flex flex-col">
        <div className="p-4 flex items-center justify-between border-b border-slate-800">
          <span className="text-sm font-medium text-slate-300">Agent Configs</span>
          <button
            onClick={newAgent}
            className="text-xs px-2 py-1 rounded bg-indigo-700 hover:bg-indigo-600 text-white"
          >
            + New
          </button>
        </div>
        <div className="flex flex-col gap-0.5 p-2">
          {agents.map((a) => (
            <button
              key={a.id}
              onClick={() => selectAgent(a)}
              className={`text-left text-sm px-3 py-2 rounded ${
                selected?.id === a.id
                  ? 'bg-slate-700 text-slate-100'
                  : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
              }`}
            >
              <div className="truncate">{a.name}</div>
              <div className="text-xs text-slate-500 mt-0.5">{a.provider}/{a.model}</div>
            </button>
          ))}
          {agents.length === 0 && (
            <p className="text-xs text-slate-600 px-3 py-4">No agents configured</p>
          )}
        </div>
      </div>

      {/* Editor */}
      <div className="flex-1 overflow-y-auto p-6">
        <h2 className="text-base font-semibold text-slate-100 mb-6">
          {selected ? `Edit: ${selected.name}` : 'New Agent Config'}
        </h2>

        <div className="grid grid-cols-2 gap-5 max-w-2xl">
          <Field label="Name">
            <input
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              className="input"
              placeholder="e.g. Code Agent"
            />
          </Field>

          <Field label="Provider">
            <select
              value={form.provider}
              onChange={(e) => setForm((f) => ({ ...f, provider: e.target.value }))}
              className="input"
            >
              <option value="claude">claude</option>
              <option value="openai">openai</option>
              <option value="llm">llm (generic)</option>
            </select>
          </Field>

          <Field label="Model">
            <input
              value={form.model}
              onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))}
              className="input"
              placeholder="e.g. claude-sonnet-4-6"
            />
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

          <Field label="Labels" className="col-span-2">
            <LabelPicker
              selected={(() => { try { return JSON.parse(form.labels) } catch { return [] } })()}
              available={availableLabels}
              onChange={(lbls) => setForm((f) => ({ ...f, labels: JSON.stringify(lbls) }))}
            />
          </Field>

          <Field label="System prompt" className="col-span-2">
            <textarea
              value={form.system_prompt}
              onChange={(e) => setForm((f) => ({ ...f, system_prompt: e.target.value }))}
              rows={8}
              className="input resize-none font-mono text-xs"
              placeholder="You are an expert software engineer…"
            />
          </Field>

          <Field label="Env vars (JSON object)" className="col-span-2">
            <textarea
              value={form.env}
              onChange={(e) => setForm((f) => ({ ...f, env: e.target.value }))}
              rows={3}
              className="input resize-none font-mono text-xs"
              placeholder='{"ANTHROPIC_API_KEY": "..."}'
            />
          </Field>
        </div>

        <div className="flex gap-3 mt-6">
          <button
            onClick={handleSave}
            disabled={saving || !form.name.trim()}
            className="px-5 py-2 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
          >
            {saving ? 'Saving…' : selected ? 'Update' : 'Create'}
          </button>
          {selected && (
            <button
              onClick={handleDelete}
              disabled={deleting}
              className="px-5 py-2 text-sm font-medium rounded bg-red-800 hover:bg-red-700 text-white disabled:opacity-50"
            >
              {deleting ? 'Deleting…' : 'Delete'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}

function Field({ label, children, className = '' }: { label: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={className}>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      {children}
    </div>
  )
}

function LabelPicker({ selected, available, onChange }: {
  selected: string[]
  available: string[]
  onChange: (labels: string[]) => void
}) {
  const toggle = (name: string) => {
    if (selected.includes(name)) {
      onChange(selected.filter((l) => l !== name))
    } else {
      onChange([...selected, name])
    }
  }

  if (available.length === 0) {
    return <p className="text-xs text-slate-500">No workflow labels found. Configure a workflow first.</p>
  }

  return (
    <div className="flex flex-wrap gap-2">
      {available.map((name) => {
        const active = selected.includes(name)
        return (
          <button
            key={name}
            type="button"
            onClick={() => toggle(name)}
            className={`px-3 py-1 rounded-full text-xs font-medium border transition-colors ${
              active
                ? 'bg-indigo-600 border-indigo-500 text-white'
                : 'bg-slate-800 border-slate-700 text-slate-400 hover:border-slate-500 hover:text-slate-200'
            }`}
          >
            {name}
          </button>
        )
      })}
    </div>
  )
}
