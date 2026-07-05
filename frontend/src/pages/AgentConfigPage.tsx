import { useEffect, useState } from 'react'
import { api, type AgentConfig, type ModelList, type ClaudeOptions } from '../api/client'
import { useAgentsStore } from '../stores/agents'
import { useWorkflowStore } from '../stores/workflow'

const CLAUDE_MODELS = [
  { label: 'Sonnet', value: 'sonnet' },
  { label: 'Opus',   value: 'opus' },
  { label: 'Haiku',  value: 'haiku' },
  { label: 'Model from env',  value: '' },
]

const EMPTY: Omit<AgentConfig, 'id' | 'created_at' | 'updated_at' | 'enabled'> = {
  name: '',
  provider: 'claude',
  model: 'sonnet',
  system_prompt: '',
  labels: '[]',
  env: '{}',
  max_tokens: 8192,
  timeout_secs: 600,
  max_turns: 50,
  max_retries: 3,
  retry_backoff_secs: 30,
  resume_sessions: true,
  subtasks_enabled: false,
  max_subtasks: 10,
  enabled_plugins: '[]',
  enabled_mcp_servers: '[]',
  command_allowlist: '[]',
  command_denylist: '[]',
}

const PLAN_PROMPT = `You are a planning agent. Your ONLY job is to write an implementation plan.

You MUST NOT write any code or make any file changes.
You MUST NOT use Edit, Write, or Bash to modify files.

Steps:
1. Read the task description and any relevant files to understand the work needed. If the task description states scope constraints (e.g. specific files, directories, or changes that are off-limits), the plan must respect them.
2. Write the plan using mcp__task-editor__update_task_notes. A good plan names the files to change and the change to make in each, lists any new files, and calls out anything ambiguous or risky that the implementer should watch for. Keep it concrete — an implementer should be able to follow it without re-investigating.
3. Call mcp__task-editor__signal_complete with outcome='success' if you produced a plan, 'failure' if the task is too ambiguous or unactionable to plan.
4. If the task is ambiguous and you need clarification from a human, call mcp__task-editor__request_human instead.

Do not implement anything. Stop after calling signal_complete or request_human.`

const TEST_PROMPT = `You are a testing agent. Your job is to verify the implementation is correct.

Steps:
1. Read the "NOTES FROM PRIOR AGENT" section to understand what was implemented. If it's missing or empty, work from the task description and the actual code changes.
2. Find the project's test/check commands (README, package.json scripts, Makefile, CI config, or language conventions) and run the test suite plus any relevant checks (lint, type-check, build).
3. Call mcp__task-editor__update_task_notes with your findings — what you ran and the results (use append:true).
4. Call mcp__task-editor__signal_complete with outcome='success' if tests pass, 'failure' if they fail. Report outcome='failure' for ANY failing test or check, even in areas the implementation did not touch — do not dismiss a failure as pre-existing or unrelated. Note in your findings which failures appear related to the change and which do not, but a failing suite is still a failure.
5. If you cannot determine pass/fail without human input (e.g. ambiguous expected behavior), call mcp__task-editor__request_human instead.`

const REVIEW_PROMPT = `You are a code review agent. Your job is to review the implementation for correctness and completeness.

Steps:
1. Read the "NOTES FROM PRIOR AGENT" section to understand context. If it's missing or empty, work from the task description and the actual code changes.
2. Review the relevant code changes for correctness, completeness against the task, and obvious issues. Rate each issue you find by severity: low (minor style/nits), medium, high, or critical.
3. Call mcp__task-editor__update_task_notes with your review findings, each tagged with its severity (use append:true).
4. Call mcp__task-editor__signal_complete with outcome='success' only if the work is correct, does what the task asked, and has no issues rated medium or above. Any medium, high, or critical issue is a 'failure'. Low-severity style nits alone do not fail the review — note them but pass.
5. If the review raises a question only a human can settle (e.g. a product/design tradeoff), call mcp__task-editor__request_human instead.`

const WORK_PROMPT = `You are an implementation agent. Your job is to implement the plan written by the planning agent.

Steps:
1. Read the "NOTES FROM PRIOR AGENT" section carefully — it contains your implementation plan. If that section is missing or empty, work directly from the task description instead. If the task description states scope constraints (e.g. specific files, directories, or changes that are off-limits), stay within them even if the plan doesn't mention them.
2. Implement the plan. If a step in the plan turns out to be wrong, incomplete, or infeasible, use your judgment to do the right thing and note the deviation in your summary.
3. Before finishing, call mcp__task-editor__update_task_notes with a summary of what you changed (use append:true).
4. Call mcp__task-editor__signal_complete with outcome='success' if done, 'failure' if you hit a blocker.
5. If you hit a blocker only a human can resolve (e.g. missing credentials, a decision outside your scope), call mcp__task-editor__request_human instead.`

const TEMPLATES: Array<Omit<AgentConfig, 'id' | 'created_at' | 'updated_at' | 'enabled'>> = [
  {
    name: 'Planner',
    provider: 'claude',
    model: 'sonnet',
    system_prompt: PLAN_PROMPT,
    labels: '["plan"]',
    env: '{}',
    max_tokens: 8192,
    timeout_secs: 600,
    max_turns: 50,
    max_retries: 3,
    retry_backoff_secs: 30,
    resume_sessions: true,
    subtasks_enabled: true,
    max_subtasks: 10,
    enabled_plugins: '[]',
    enabled_mcp_servers: '[]',
    command_allowlist: '[]',
    command_denylist: '[]',
  },
  {
    name: 'Tester',
    provider: 'claude',
    model: 'sonnet',
    system_prompt: TEST_PROMPT,
    labels: '["testing"]',
    env: '{}',
    max_tokens: 8192,
    timeout_secs: 600,
    max_turns: 50,
    max_retries: 3,
    retry_backoff_secs: 30,
    resume_sessions: true,
    enabled_plugins: '[]',
    enabled_mcp_servers: '[]',
    command_allowlist: '[]',
    command_denylist: '[]',
  },
  {
    name: 'Reviewer',
    provider: 'claude',
    model: 'sonnet',
    system_prompt: REVIEW_PROMPT,
    labels: '["agent-review"]',
    env: '{}',
    max_tokens: 8192,
    timeout_secs: 600,
    max_turns: 50,
    max_retries: 3,
    retry_backoff_secs: 30,
    resume_sessions: true,
    enabled_plugins: '[]',
    enabled_mcp_servers: '[]',
    command_allowlist: '[]',
    command_denylist: '[]',
  },
  {
    name: 'Worker',
    provider: 'claude',
    model: 'sonnet',
    system_prompt: WORK_PROMPT,
    labels: '["work"]',
    env: '{}',
    max_tokens: 8192,
    timeout_secs: 600,
    max_turns: 50,
    max_retries: 3,
    retry_backoff_secs: 30,
    resume_sessions: true,
    enabled_plugins: '[]',
    enabled_mcp_servers: '[]',
    command_allowlist: '[]',
    command_denylist: '[]',
  },
]

const PROVIDERS = ['claude', 'opencode', 'openai', 'llm', 'anthropic', 'qwen_code']

export default function AgentConfigPage() {
  const { configs: agents, fetch: fetchAgents } = useAgentsStore()
  const { workflows, fetch: fetchWorkflows } = useWorkflowStore()
  const [selected, setSelected] = useState<AgentConfig | null>(null)
  const [form, setForm] = useState<typeof EMPTY>(EMPTY)
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [modelList, setModelList] = useState<ModelList | null>(null)
  const [fetchingModels, setFetchingModels] = useState(false)
  const [claudeOptions, setClaudeOptions] = useState<ClaudeOptions | null>(null)
  const [showTemplates, setShowTemplates] = useState(false)
  const [creatingTemplate, setCreatingTemplate] = useState(false)
  const [multiMode, setMultiMode] = useState(false)
  const [multiSelected, setMultiSelected] = useState<Set<string>>(new Set())
  const [bulkSaving, setBulkSaving] = useState(false)

  const availableLabels = workflows[0]?.labels.map((l) => l.name) ?? []

  useEffect(() => {
    fetchAgents()
    fetchWorkflows()
  }, [fetchAgents, fetchWorkflows])

  useEffect(() => {
    if (form.provider === 'claude') {
      setModelList(null)
      setFetchingModels(false)
      return
    }
    if (PROVIDERS.includes(form.provider)) {
      setFetchingModels(true)
      api.agents.models(form.provider)
        .then((data) => {
          setModelList(data)
          if (form.model === '' || form.model === EMPTY.model) {
            setForm((f) => ({ ...f, model: data.default_model }))
          }
        })
        .catch(() => setModelList(null))
        .finally(() => setFetchingModels(false))
    } else {
      setModelList(null)
    }
  }, [form.provider]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (form.provider !== 'claude') {
      setClaudeOptions(null)
      return
    }
    api.agents.claudeOptions()
      .then(setClaudeOptions)
      .catch(() => setClaudeOptions(null))
  }, [form.provider])

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
      max_retries: a.max_retries,
      retry_backoff_secs: a.retry_backoff_secs,
      resume_sessions: a.resume_sessions ?? true,
      subtasks_enabled: a.subtasks_enabled ?? false,
      max_subtasks: a.max_subtasks ?? 10,
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
      const { config, labelConflict } = await api.agents.create(t)
      await fetchAgents()
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
        await api.agents.update(selected.id, { ...payload, enabled: !!selected.enabled })
      } else {
        const { labelConflict } = await api.agents.create(payload)
        if (labelConflict) {
          alert(`Agent created but started disabled — label conflict with active config "${labelConflict}".`)
        }
      }
      fetchAgents()
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
      const updated = await api.agents.update(selected.id, { ...form, enabled: !selected.enabled })
      await fetchAgents()
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
      const selected = agents.filter(a => multiSelected.has(a.id))
      await Promise.all(
        selected.map(a =>
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
            max_retries: a.max_retries,
            retry_backoff_secs: a.retry_backoff_secs,
            resume_sessions: a.resume_sessions ?? true,
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
      await api.agents.delete(selected.id)
      fetchAgents()
      newAgent()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setDeleting(false)
    }
  }

  const isEnabled = selected ? (selected.enabled !== 0 && selected.enabled !== false) : true

  return (
    <div className="flex h-full overflow-hidden">
      {/* Sidebar */}
      <div className="w-56 shrink-0 border-r border-slate-800 overflow-y-auto flex flex-col">
        <div className="p-4 flex items-center justify-between border-b border-slate-800">
          <span className="text-sm font-medium text-slate-300">Agent Configs</span>
          <div className="flex items-center gap-1.5">
            <button
              onClick={() => { setMultiMode(v => !v); setMultiSelected(new Set()) }}
              className="text-xs px-2 py-1 rounded bg-slate-700 hover:bg-slate-600 text-slate-300"
            >
              {multiMode ? 'Done' : 'Select'}
            </button>
            {!multiMode && (
              <button
                onClick={newAgent}
                className="text-xs px-2 py-1 rounded bg-indigo-700 hover:bg-indigo-600 text-white"
              >
                + New
              </button>
            )}
          </div>
        </div>

        {/* Templates section */}
        <div className="border-b border-slate-800">
          <button
            onClick={() => setShowTemplates((v) => !v)}
            className="w-full flex items-center justify-between px-4 py-2.5 text-xs font-medium text-slate-400 hover:text-slate-200 hover:bg-slate-800/50"
          >
            <span>Templates</span>
            <span className="text-slate-600">{showTemplates ? '▲' : '▼'}</span>
          </button>
          {showTemplates && (
            <div className="flex flex-col gap-0.5 px-2 pb-2">
              {TEMPLATES.map((t) => (
                <button
                  key={t.name}
                  onClick={() => applyTemplate(t)}
                  disabled={creatingTemplate}
                  className="text-left text-xs px-3 py-1.5 rounded text-indigo-400 hover:bg-slate-800 hover:text-indigo-300 disabled:opacity-50"
                >
                  + {t.name}
                  <span className="ml-1 text-slate-600">{JSON.parse(t.labels)[0] ?? ''}</span>
                </button>
              ))}
            </div>
          )}
        </div>

        {/* Agent list */}
        <div className="flex flex-col gap-0.5 p-2">
          {agents.map((a) => {
            const isChecked = multiSelected.has(a.id)
            const isDisabled = a.enabled === 0 || a.enabled === false
            return (
              <button
                key={a.id}
                onClick={() => {
                  if (multiMode) {
                    setMultiSelected(prev => {
                      const next = new Set(prev)
                      if (next.has(a.id)) next.delete(a.id); else next.add(a.id)
                      return next
                    })
                  } else {
                    selectAgent(a)
                  }
                }}
                className={`w-full text-left text-sm px-3 py-2 rounded flex items-start gap-2 ${
                  !multiMode && selected?.id === a.id
                    ? 'bg-slate-700 text-slate-100'
                    : 'text-slate-400 hover:bg-slate-800 hover:text-slate-200'
                } ${!multiMode && isDisabled ? 'opacity-50' : ''} ${
                  multiMode && isChecked ? 'ring-1 ring-indigo-500 bg-slate-800' : ''
                }`}
              >
                {multiMode && (
                  <span className={`mt-0.5 w-3.5 h-3.5 rounded border shrink-0 flex items-center justify-center ${
                    isChecked ? 'bg-indigo-600 border-indigo-500' : 'border-slate-600'
                  }`}>
                    {isChecked && <span className="text-white text-[10px] leading-none">✓</span>}
                  </span>
                )}
                <span className="flex-1 min-w-0">
                  <div className="truncate flex items-center gap-1.5">
                    {!multiMode && isDisabled && (
                      <span className="w-1.5 h-1.5 rounded-full bg-slate-600 shrink-0" />
                    )}
                    {multiMode && isDisabled && (
                      <span className="text-slate-600 text-[10px]">[off]</span>
                    )}
                    {a.name}
                  </div>
                  <div className="text-xs text-slate-500 mt-0.5">{a.provider}/{a.model.split('-').slice(0,2).join('-')}</div>
                </span>
              </button>
            )
          })}
          {agents.length === 0 && (
            <p className="text-xs text-slate-600 px-3 py-4">No agents configured</p>
          )}
        </div>

        {/* Bulk action bar — shown only in multi mode */}
        {multiMode && (
          <div className="mt-auto p-2 border-t border-slate-800 flex flex-col gap-1.5">
            <p className="text-xs text-slate-400 px-1">
              {multiSelected.size > 0 ? `${multiSelected.size} selected` : 'Tap agents to select'}
            </p>
            {multiSelected.size > 0 && (
              <>
                <button
                  onClick={() => handleBulkToggle(true)}
                  disabled={bulkSaving}
                  className="text-xs px-2 py-1.5 rounded bg-green-700 hover:bg-green-600 text-white disabled:opacity-50"
                >
                  {bulkSaving ? 'Saving…' : 'Enable All'}
                </button>
                <button
                  onClick={() => handleBulkToggle(false)}
                  disabled={bulkSaving}
                  className="text-xs px-2 py-1.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-300 disabled:opacity-50"
                >
                  {bulkSaving ? 'Saving…' : 'Disable All'}
                </button>
                <button
                  onClick={() => setMultiSelected(new Set(agents.map(a => a.id)))}
                  className="text-xs px-2 py-1.5 rounded text-slate-500 hover:text-slate-300"
                >
                  Select All
                </button>
              </>
            )}
          </div>
        )}
      </div>

      {/* Editor */}
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
                onClick={handleToggleEnabled}
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
            {form.provider === 'claude' ? (
              <select
                value={form.model}
                onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))}
                className="input"
              >
                {CLAUDE_MODELS.map((m) => (
                  <option key={m.value} value={m.value}>{m.label}</option>
                ))}
              </select>
            ) : modelList && modelList.models.length > 0 ? (
              <select
                value={form.model}
                onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))}
                className="input"
              >
                <option value="">Use $MODEL env var</option>
                {modelList.models.map((m) => (
                  <option key={m} value={m}>{m}</option>
                ))}
              </select>
            ) : (
              <input
                value={form.model}
                onChange={(e) => setForm((f) => ({ ...f, model: e.target.value }))}
                className="input"
                placeholder={fetchingModels ? 'Loading models...' : 'e.g. sonnet (empty = use env var)'}
              />
            )}
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

          <Field label="Subtasks" hint="claude/qwen_code only: expose the create_subtask tool so this agent (typically the planner) can decompose its task into child tasks. Children branch off the parent's branch and merge back automatically. Off by default.">
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

          {form.provider === 'claude' && (
            <>
              <Field label="Plugins" className="col-span-2">
                <ChipPicker
                  selected={(() => { try { return JSON.parse(form.enabled_plugins ?? '[]') } catch { return [] } })()}
                  available={(claudeOptions?.plugins ?? []).map((p) => ({ value: p.id, label: p.marketplace ? `${p.name} (${p.marketplace})` : p.name }))}
                  onChange={(ids) => setForm((f) => ({ ...f, enabled_plugins: JSON.stringify(ids) }))}
                  emptyMessage="No plugins found in ~/.claude/plugins/installed_plugins.json."
                />
                <p className="mt-1 text-xs text-slate-500">Discovered from your Claude home dir. Off by default — toggle to enable per agent.</p>
              </Field>

              <Field label="MCP servers" className="col-span-2">
                <ChipPicker
                  selected={(() => { try { return JSON.parse(form.enabled_mcp_servers ?? '[]') } catch { return [] } })()}
                  available={(claudeOptions?.mcp_servers ?? []).map((name) => ({ value: name, label: name }))}
                  onChange={(names) => setForm((f) => ({ ...f, enabled_mcp_servers: JSON.stringify(names) }))}
                  emptyMessage="No user-level MCP servers found in ~/.claude.json."
                />
                <p className="mt-1 text-xs text-slate-500">Only global (user-level) MCP servers are listed; project-scoped servers aren't included.</p>
              </Field>
            </>
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

          <Field label="Env vars (JSON object)" className="col-span-2">
            <textarea
              value={form.env}
              onChange={(e) => setForm((f) => ({ ...f, env: e.target.value }))}
              rows={3}
              className="input resize-none font-mono text-xs"
              placeholder='{"ANTHROPIC_API_KEY": "..."}'
            />
            {selected && /"\*\*\*"/.test(form.env) && (
              <p className="mt-1 text-xs text-slate-500">Keys showing *** are already set. Clear or replace the value to update; leave *** to keep existing.</p>
            )}
          </Field>

          <Field label="Command allowlist (JSON array of glob patterns)" className="col-span-2">
            <textarea
              value={form.command_allowlist}
              onChange={(e) => setForm((f) => ({ ...f, command_allowlist: e.target.value }))}
              rows={2}
              className="input resize-none font-mono text-xs"
              placeholder='["git *", "npm test", "go *"]'
            />
            <p className="mt-1 text-xs text-slate-500">
              If non-empty, only run_bash/Bash commands matching a pattern here are allowed. "*" is a wildcard.
              Best-effort string matching, not a sandbox.{' '}
              {form.provider === 'opencode' && 'Not enforced for the opencode provider.'}
              {form.provider === 'claude' &&
                'Not an effective restriction for the claude provider: the CLI only auto-approves matches, it does not block non-matching commands. Use the denylist below instead.'}
            </p>
          </Field>

          <Field label="Command denylist (JSON array of glob patterns)" className="col-span-2">
            <textarea
              value={form.command_denylist}
              onChange={(e) => setForm((f) => ({ ...f, command_denylist: e.target.value }))}
              rows={2}
              className="input resize-none font-mono text-xs"
              placeholder='["rm -rf *", "curl *", "sudo *"]'
            />
            <p className="mt-1 text-xs text-slate-500">
              Commands matching any pattern here are always denied, checked before the allowlist.{' '}
              {form.provider === 'opencode' && 'Not enforced for the opencode provider.'}
              {form.provider === 'qwen_code' && 'Not enforced for the qwen_code provider (no confirmed CLI denylist flag).'}
            </p>
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

function Field({ label, children, className = '', hint }: { label: string; children: React.ReactNode; className?: string; hint?: string }) {
  return (
    <div className={className}>
      <label className="block text-xs font-medium text-slate-400 mb-1.5">{label}</label>
      {children}
      {hint && <p className="mt-1 text-[11px] text-slate-500">{hint}</p>}
    </div>
  )
}

function LabelPicker({ selected, available, onChange }: {
  selected: string[]
  available: string[]
  onChange: (labels: string[]) => void
}) {
  return (
    <ChipPicker
      selected={selected}
      available={available.map((name) => ({ value: name, label: name }))}
      onChange={onChange}
      emptyMessage="No workflow labels found. Configure a workflow first."
    />
  )
}

// ChipPicker is a generic toggle-chip multi-select, used for workflow labels,
// Claude plugins, and Claude MCP servers.
function ChipPicker({ selected, available, onChange, emptyMessage }: {
  selected: string[]
  available: { value: string; label: string }[]
  onChange: (values: string[]) => void
  emptyMessage: string
}) {
  const toggle = (value: string) => {
    if (selected.includes(value)) {
      onChange(selected.filter((v) => v !== value))
    } else {
      onChange([...selected, value])
    }
  }

  if (available.length === 0) {
    return <p className="text-xs text-slate-500">{emptyMessage}</p>
  }

  return (
    <div className="flex flex-wrap gap-2">
      {available.map(({ value, label }) => {
        const active = selected.includes(value)
        return (
          <button
            key={value}
            type="button"
            onClick={() => toggle(value)}
            className={`px-3 py-1 rounded-full text-xs font-medium border transition-colors ${
              active
                ? 'bg-indigo-600 border-indigo-500 text-white'
                : 'bg-slate-800 border-slate-700 text-slate-400 hover:border-slate-500 hover:text-slate-200'
            }`}
          >
            {label}
          </button>
        )
      })}
    </div>
  )
}
