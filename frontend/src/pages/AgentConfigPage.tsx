import { useEffect, useState } from 'react'
import { api, type AgentConfig } from '../api/client'
import { useAgentsStore } from '../stores/agents'
import { useWorkflowStore } from '../stores/workflow'
import { EMPTY, TEMPLATES } from '../lib/agentTemplates'
import AgentSidebar from '../components/agents/AgentSidebar'
import AgentConfigForm, { type FormState } from '../components/agents/AgentConfigForm'

export default function AgentConfigPage() {
  const {
    configs: agents,
    fetch: fetchAgents,
    modelList,
    fetchingModels,
    fetchModels,
    claudeOptions,
    fetchClaudeOptions,
    create: createAgent,
    update: updateAgent,
    delete: deleteAgent,
  } = useAgentsStore()
  const { workflows, fetch: fetchWorkflows } = useWorkflowStore()
  const [selected, setSelected] = useState<AgentConfig | null>(null)
  const [form, setForm] = useState<FormState>(EMPTY)
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [showTemplates, setShowTemplates] = useState(false)
  const [creatingTemplate, setCreatingTemplate] = useState(false)
  const [multiMode, setMultiMode] = useState(false)
  const [multiSelected, setMultiSelected] = useState<Set<string>>(new Set())
  const [bulkSaving, setBulkSaving] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false)

  const availableLabels = workflows[0]?.labels.map((l) => l.name) ?? []

  useEffect(() => {
    fetchAgents()
    fetchWorkflows()
  }, [fetchAgents, fetchWorkflows])

  useEffect(() => {
    if (form.provider === 'claude') {
      return
    }
    fetchModels(form.provider).then((data) => {
      if (data && (form.model === '' || form.model === EMPTY.model)) {
        setForm((f) => ({ ...f, model: data.default_model }))
      }
    })
  }, [form.provider]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (form.provider !== 'claude') return
    fetchClaudeOptions()
  }, [form.provider]) // eslint-disable-line react-hooks/exhaustive-deps

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
      max_turns: a.max_turns,
      priority: a.priority ?? 0,
      max_retries: a.max_retries,
      retry_backoff_secs: a.retry_backoff_secs,
      resume_sessions: a.resume_sessions ?? true,
      subtasks_enabled: a.subtasks_enabled ?? false,
      max_subtasks: a.max_subtasks ?? 10,
      max_cost_usd: a.max_cost_usd ?? 0,
      enabled_plugins: a.enabled_plugins ?? '[]',
      enabled_mcp_servers: a.enabled_mcp_servers ?? '[]',
      command_allowlist: a.command_allowlist ?? '[]',
      command_denylist: a.command_denylist ?? '[]',
    })
  }

  function newAgent() {
    setSelected(null)
    setForm(EMPTY)
  }

  async function applyTemplate(t: typeof TEMPLATES[0]) {
    setCreatingTemplate(true)
    setShowTemplates(false)
    try {
      const { config, labelConflict } = await createAgent(t)
      selectAgent(config)
      if (labelConflict) {
        alert(`Agent created but started disabled — label conflict with active config "${labelConflict}".`)
      }
    } catch (e: any) {
      alert(e.message)
    } finally {
      setCreatingTemplate(false)
    }
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
        await updateAgent(selected.id, { ...payload, enabled: !!selected.enabled })
      } else {
        const { labelConflict } = await createAgent(payload)
        if (labelConflict) {
          alert(`Agent created but started disabled — label conflict with active config "${labelConflict}".`)
        }
      }
      newAgent()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleToggleEnabled() {
    if (!selected) return
    setSaving(true)
    try {
      const updated = await updateAgent(selected.id, { ...form, enabled: !selected.enabled })
      selectAgent(updated)
    } catch (e: any) {
      alert(e.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleBulkToggle(enable: boolean) {
    if (multiSelected.size === 0) return
    setBulkSaving(true)
    try {
      const toUpdate = agents.filter((a) => multiSelected.has(a.id))
      await Promise.all(
        toUpdate.map((a) =>
          api.agents.update(a.id, {
            name: a.name,
            provider: a.provider,
            model: a.model,
            system_prompt: a.system_prompt,
            labels: a.labels,
            env: a.env,
            max_tokens: a.max_tokens,
            timeout_secs: a.timeout_secs,
            max_turns: a.max_turns,
            priority: a.priority ?? 0,
            max_retries: a.max_retries,
            retry_backoff_secs: a.retry_backoff_secs,
            resume_sessions: a.resume_sessions ?? true,
            max_cost_usd: a.max_cost_usd ?? 0,
            enabled_plugins: a.enabled_plugins ?? '[]',
            enabled_mcp_servers: a.enabled_mcp_servers ?? '[]',
            command_allowlist: a.command_allowlist ?? '[]',
            command_denylist: a.command_denylist ?? '[]',
            enabled: enable,
          })
        )
      )
      await fetchAgents()
      setMultiSelected(new Set())
    } catch (e: any) {
      alert(e.message)
    } finally {
      setBulkSaving(false)
    }
  }

  async function handleDelete() {
    if (!selected) return
    if (!confirm(`Delete agent "${selected.name}"?`)) return
    setDeleting(true)
    try {
      await deleteAgent(selected.id)
      newAgent()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Mobile-only header bar: shows selected agent + button to open the configs drawer */}
      <div className="md:hidden flex items-center justify-between px-4 py-2 border-b border-slate-800 bg-slate-950">
        <span className="text-sm text-slate-300 truncate">
          {selected ? selected.name : 'New agent'}
        </span>
        <button
          onClick={() => setSidebarOpen(true)}
          className="text-xs px-2 py-1 rounded bg-slate-700 hover:bg-slate-600 text-slate-300"
        >
          Configs
        </button>
      </div>

      <div className="flex-1 flex overflow-hidden">
        <AgentSidebar
          agents={agents}
          selected={selected}
          onSelect={selectAgent}
          onNew={newAgent}
          multiMode={multiMode}
          setMultiMode={setMultiMode}
          multiSelected={multiSelected}
          setMultiSelected={setMultiSelected}
          onBulkToggle={handleBulkToggle}
          bulkSaving={bulkSaving}
          showTemplates={showTemplates}
          setShowTemplates={setShowTemplates}
          creatingTemplate={creatingTemplate}
          onApplyTemplate={applyTemplate}
          isOpen={sidebarOpen}
          onClose={() => setSidebarOpen(false)}
        />

        <AgentConfigForm
          selected={selected}
          form={form}
          setForm={setForm}
          availableLabels={availableLabels}
          modelList={modelList}
          fetchingModels={fetchingModels}
          claudeOptions={claudeOptions}
          saving={saving}
          deleting={deleting}
          onSave={handleSave}
          onDelete={handleDelete}
          onToggleEnabled={handleToggleEnabled}
        />
      </div>
    </div>
  )
}
