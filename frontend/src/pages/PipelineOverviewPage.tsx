import { labelDisplayName } from '../lib/labelDisplay'
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type AgentConfig, type Workflow } from '../api/client'
import WorkflowGraphEditor from '../components/shared/WorkflowGraphEditor'
import { useTasksStore } from '../stores/tasks'
import {
  draftFromWorkflow,
  draftToWorkflow,
  isDirty,
  serializeDraft,
  validateDraft,
  addTransition,
  type WorkflowDraft,
} from '../lib/workflowEdit'
import { validateWorkflow } from '../lib/validateWorkflow'
import WorkflowFlowchart from '../components/shared/WorkflowFlowchart'
import { useAgentsStore } from '../stores/agents'
import { useWorkflowStore } from '../stores/workflow'
import { parseAgentLabels } from '../lib/setupStatus'
import { toggleAgentLabel, workflowStaffing } from '../lib/staffing'

/**
 * Read-only graph of a workflow with who works each stage, plus a small panel
 * to assign agent configs to the clicked stage. Assignment edits the agent
 * config's `labels` through the normal agent update API.
 */
export default function PipelineOverviewPage() {
  const workflows = useWorkflowStore((s) => s.workflows)
  const selectedId = useWorkflowStore((s) => s.selectedId)
  const fetchWorkflows = useWorkflowStore((s) => s.fetch)
  const setSelectedId = useWorkflowStore((s) => s.setSelectedId)
  const agents = useAgentsStore((s) => s.configs)
  const fetchAgents = useAgentsStore((s) => s.fetch)
  const updateAgent = useAgentsStore((s) => s.update)

  const [selectedLabel, setSelectedLabel] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  // ── Edit mode ───────────────────────────────────────────────────────────
  const tasks = useTasksStore((s) => s.tasks)
  const fetchTasks = useTasksStore((s) => s.fetch)
  const tasksLoading = useTasksStore((s) => s.loading)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<WorkflowDraft | null>(null)
  const [baseline, setBaseline] = useState<WorkflowDraft | null>(null)
  const [baseWf, setBaseWf] = useState<Workflow | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [conflict, setConflict] = useState(false)
  const [savedNote, setSavedNote] = useState(false)
  const [prefill, setPrefill] = useState<{ from: string; to: string; n: number } | null>(null)

  useEffect(() => {
    void fetchWorkflows()
    void fetchAgents()
  }, [fetchWorkflows, fetchAgents])

  const workflow = workflows.find((w) => w.id === selectedId) ?? workflows[0]
  const staffing = useMemo(
    () => (workflow ? workflowStaffing(workflow, agents) : undefined),
    [workflow, agents],
  )
  const label = workflow?.labels.find((l) => l.name === selectedLabel)

  const dirty = editing && draft !== null && baseline !== null && isDirty(draft, baseline)
  const draftWorkflow = useMemo(
    () => (editing && draft && workflow ? draftToWorkflow(draft, workflow) : undefined),
    [editing, draft, workflow],
  )
  const draftStaffing = useMemo(
    () => (draftWorkflow ? workflowStaffing(draftWorkflow, agents) : undefined),
    [draftWorkflow, agents],
  )
  const errors = useMemo(() => (draft ? validateDraft(draft) : []), [draft])
  const warnings = useMemo(() => {
    if (!draft || draft.labels.length === 0) return []
    return validateWorkflow({
      name: draft.name,
      labels: draft.labels.map((l, i) => ({ name: l.name, sortOrder: i, isTerminal: l.is_terminal, agentIgnore: l.agent_ignore })),
      transitions: draft.transitions.map((t) => ({ from: t.from_label, to: t.to_label })),
    }).filter((w) => w.message.includes('not reachable'))
  }, [draft])
  const taskCounts = useMemo(() => {
    const m = new Map<string, number>()
    for (const t of tasks) {
      if (t.workflow_id !== workflow?.id) continue
      m.set(t.label, (m.get(t.label) ?? 0) + 1)
    }
    return m
  }, [tasks, workflow?.id])
  const savedLabelNames = useMemo(() => new Set((baseWf?.labels ?? []).map((l) => l.name)), [baseWf])

  const startEdit = () => {
    if (!workflow) return
    const d = draftFromWorkflow(workflow)
    setDraft(d)
    setBaseline(d)
    setBaseWf(workflow)
    setEditing(true)
    setSaveError(null)
    setConflict(false)
    setSavedNote(false)
    void fetchTasks()
  }
  const discard = () => {
    setEditing(false)
    setDraft(null)
    setBaseline(null)
    setSaveError(null)
    setConflict(false)
    setSelectedLabel(null)
  }
  const save = async (force = false) => {
    if (!draft || !baseWf || errors.length > 0) return
    setSaving(true)
    setSaveError(null)
    try {
      if (!force) {
        // The API has no ETag, so refetch and compare before writing.
        const fresh = await api.workflows.get(baseWf.id)
        const changed =
          fresh.updated_at !== baseWf.updated_at ||
          isDirty(draftFromWorkflow(fresh), baseline as WorkflowDraft)
        if (changed) {
          setConflict(true)
          return
        }
      }
      await api.workflows.updateYaml(baseWf.id, serializeDraft(draft))
      await fetchWorkflows()
      setEditing(false)
      setDraft(null)
      setBaseline(null)
      setConflict(false)
      setSavedNote(true)
    } catch (e) {
      setSaveError(e instanceof Error ? e.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }
  const reloadFromServer = async () => {
    await fetchWorkflows()
    discard()
  }
  const connect = (from: string, to: string) => {
    if (!draft) return
    setPrefill({ from, to, n: (prefill?.n ?? 0) + 1 })
    // A drag is a complete gesture: create the default agent-success edge now;
    // its type can be changed in the Transitions list.
    const r = addTransition(draft, from, to, 'agent_success')
    if (r.error) setSaveError(r.error)
    else { setSaveError(null); setDraft(r.draft) }
  }

  const toggle = async (agent: AgentConfig, assigned: boolean) => {
    if (!label) return
    setBusyId(agent.id)
    setError(null)
    try {
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { id: _id, created_at: _c, updated_at: _u, provider_config: _p, ...rest } = agent
      await updateAgent(agent.id, { ...rest, labels: toggleAgentLabel(agent, label.name, assigned) })
    } catch (e) {
      setError(`Could not update ${agent.name}: ${e instanceof Error ? e.message : String(e)}`)
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div data-testid="pipeline-overview" className="flex flex-col h-full min-h-0 p-4 gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold text-slate-100">Pipeline</h1>
        {workflows.length > 1 && (
          <select
            aria-label="Workflow"
            value={workflow?.id ?? ''}
            onChange={(e) => {
              setSelectedId(e.target.value)
              setSelectedLabel(null)
              setSavedNote(false)
            }}
            disabled={editing}
            className="bg-slate-900 border border-slate-600 rounded px-2 py-1 text-sm text-slate-200"
          >
            {workflows.map((w) => (
              <option key={w.id} value={w.id}>{w.name}</option>
            ))}
          </select>
        )}
        <span className="text-xs text-slate-500">
          {editing
            ? 'Editing: changes are staged until you save.'
            : 'Who works each stage. Click a stage to assign agents. Prefer YAML? Use the '}
          {!editing && (
            <>
              <Link to="/pipeline/workflow" className="text-indigo-400 hover:text-indigo-300">Workflow</Link> tab (Advanced).
            </>
          )}
        </span>
        <div className="ml-auto flex items-center gap-2">
          {savedNote && !editing && <span role="status" className="text-xs text-green-400">Saved</span>}
          {editing && dirty && <span data-testid="dirty-indicator" className="text-xs text-amber-300">Unsaved changes</span>}
          {!editing && workflow && (
            <button type="button" onClick={startEdit}
              className="px-3 py-1 text-sm rounded bg-slate-700 hover:bg-slate-600 text-slate-100">
              Edit workflow
            </button>
          )}
          {editing && (
            <>
              <button type="button" onClick={discard} disabled={saving}
                className="px-3 py-1 text-sm rounded bg-slate-700 hover:bg-slate-600 text-slate-100 disabled:opacity-50">
                {dirty ? 'Discard' : 'Cancel'}
              </button>
              <button type="button" onClick={() => void save()} disabled={!dirty || saving || errors.length > 0}
                className="px-3 py-1 text-sm rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50">
                {saving ? 'Saving…' : 'Save'}
              </button>
            </>
          )}
        </div>
      </div>

      {editing && (
        <div className="space-y-1">
          {conflict && (
            <div role="alert" className="rounded border border-amber-600 bg-amber-900/30 p-2 text-sm text-amber-200 flex flex-wrap items-center gap-2">
              <span>This workflow was changed on the server after you started editing.</span>
              <button type="button" onClick={() => void reloadFromServer()} className="px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-100">
                Reload and discard my edits
              </button>
              <button type="button" onClick={() => void save(true)} className="px-2 py-1 text-xs rounded bg-amber-600 hover:bg-amber-500 text-black">
                Overwrite with my edits
              </button>
            </div>
          )}
          {saveError && <p role="alert" className="text-sm text-red-300">{saveError}</p>}
          {errors.length > 0 && (
            <ul aria-label="Validation errors" className="text-xs text-red-300 list-disc pl-5">
              {errors.map((e, i) => <li key={i}>{e.message}</li>)}
            </ul>
          )}
          {warnings.length > 0 && (
            <ul aria-label="Warnings" className="text-xs text-amber-300 list-disc pl-5">
              {warnings.map((w, i) => <li key={i}>{w.message}</li>)}
            </ul>
          )}
          {draft?.transitions.some((t) => t.agent_config_id) && (
            <p className="text-xs text-amber-300">
              Some transitions name a specific agent config. Saving from the graph editor (like the YAML tab) does not keep that per-transition assignment; assign agents to labels instead.
            </p>
          )}
        </div>
      )}

      {!workflow ? (
        <div className="flex-1 flex items-center justify-center text-slate-500 text-sm">
          No workflows. Create one in the Workflow tab to get started.
        </div>
      ) : (
        <div className="flex-1 min-h-0 flex flex-col md:flex-row gap-3">
          <div className="flex-1 min-w-0 min-h-[50vh] md:min-h-0 rounded border border-slate-700 overflow-hidden bg-slate-950">
            <WorkflowFlowchart
              workflow={draftWorkflow ?? workflow}
              staffing={draftStaffing ?? staffing}
              onSelectLabel={setSelectedLabel}
              onConnectLabels={editing ? connect : undefined}
              selectedLabel={selectedLabel}
            />
          </div>
          {editing && draft && (
            <WorkflowGraphEditor
              draft={draft}
              onChange={setDraft}
              selectedLabel={selectedLabel}
              onSelectLabel={setSelectedLabel}
              taskCounts={taskCounts}
              tasksPending={tasksLoading}
              savedLabelNames={savedLabelNames}
              connectPrefill={prefill}
            />
          )}
          {!editing && label && (
            <aside
              aria-label={`Assign agents to ${labelDisplayName(label)}`}
              className="md:w-72 shrink-0 rounded border border-slate-700 bg-slate-900 p-3 space-y-2 overflow-auto"
            >
              <div className="flex items-start justify-between gap-2">
                <h2 className="text-sm font-semibold text-slate-100">{labelDisplayName(label)}</h2>
                <button
                  type="button"
                  aria-label="Close panel"
                  onClick={() => setSelectedLabel(null)}
                  className="text-slate-400 hover:text-slate-100"
                >
                  ×
                </button>
              </div>
              {staffing?.get(label.name)?.kind === 'human' && (
                <p className="text-xs text-slate-400">
                  Only a human moves tasks out of this stage. Assigning an agent only matters once the stage
                  has an agent transition.
                </p>
              )}
              {agents.length === 0 ? (
                <p className="text-xs text-slate-400">
                  No agent configs yet. <Link to="/pipeline/agents" className="text-indigo-400 hover:text-indigo-300">Create one →</Link>
                </p>
              ) : (
                <ul className="space-y-1">
                  {agents.map((a) => {
                    const assigned = parseAgentLabels(a.labels).includes(label.name)
                    return (
                      <li key={a.id} className="flex items-center gap-2 text-sm">
                        <label className="flex flex-1 min-w-0 items-center gap-2 text-slate-200">
                          <input
                            type="checkbox"
                            checked={assigned}
                            disabled={busyId !== null}
                            onChange={(e) => void toggle(a, e.target.checked)}
                          />
                          <span className="truncate">{a.name}</span>
                          <span className="text-xs text-slate-500 shrink-0">
                            {a.provider_config?.provider ?? ''}{a.enabled ? '' : ' · disabled'}
                          </span>
                        </label>
                        <Link
                          to="/pipeline/agents"
                          title={`Edit ${a.name}`}
                          aria-label={`Edit ${a.name}`}
                          className="text-xs text-indigo-400 hover:text-indigo-300 shrink-0"
                        >
                          Edit
                        </Link>
                      </li>
                    )
                  })}
                </ul>
              )}
              {error && <p role="alert" className="text-xs text-red-300">{error}</p>}
            </aside>
          )}
        </div>
      )}
    </div>
  )
}
