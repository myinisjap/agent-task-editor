import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import type { AgentConfig } from '../api/client'
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
            }}
            className="bg-slate-900 border border-slate-600 rounded px-2 py-1 text-sm text-slate-200"
          >
            {workflows.map((w) => (
              <option key={w.id} value={w.id}>{w.name}</option>
            ))}
          </select>
        )}
        <span className="text-xs text-slate-500">
          Who works each stage. Click a stage to assign agents. Edit stages and transitions in the{' '}
          <Link to="/pipeline/workflow" className="text-indigo-400 hover:text-indigo-300">Workflow</Link> tab.
        </span>
      </div>

      {!workflow ? (
        <div className="flex-1 flex items-center justify-center text-slate-500 text-sm">
          No workflows. Create one in the Workflow tab to get started.
        </div>
      ) : (
        <div className="flex-1 min-h-0 flex flex-col md:flex-row gap-3">
          <div className="flex-1 min-w-0 min-h-[50vh] md:min-h-0 rounded border border-slate-700 overflow-hidden bg-slate-950">
            <WorkflowFlowchart
              workflow={workflow}
              staffing={staffing}
              onSelectLabel={setSelectedLabel}
              selectedLabel={selectedLabel}
            />
          </div>
          {label && (
            <aside
              aria-label={`Assign agents to ${label.name}`}
              className="md:w-72 shrink-0 rounded border border-slate-700 bg-slate-900 p-3 space-y-2 overflow-auto"
            >
              <div className="flex items-start justify-between gap-2">
                <h2 className="text-sm font-semibold text-slate-100">{label.name}</h2>
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
