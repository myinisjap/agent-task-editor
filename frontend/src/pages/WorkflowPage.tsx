import { useEffect, useRef, useState } from 'react'
import { api, authedRawFetch, type Workflow } from '../api/client'
import { useWorkflowStore } from '../stores/workflow'
import WorkflowFlowchart from '../components/shared/WorkflowFlowchart'
import { parseWorkflowYaml } from '../lib/parseWorkflowYaml'
import { validateWorkflow, type WorkflowValidationError } from '../lib/validateWorkflow'

// ── New-workflow modal ──────────────────────────────────────────────────────

function NewWorkflowModal({
  onClose,
  onCreate,
}: {
  onClose: () => void
  onCreate: (wf: Workflow) => void
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!name.trim()) return
    setCreating(true)
    setError(null)
    try {
      const wf = await api.workflows.create({ name: name.trim(), description: description.trim() || undefined })
      onCreate(wf)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Creation failed')
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
      <form
        onSubmit={handleSubmit}
        className="bg-slate-900 border border-slate-700 rounded-lg shadow-xl p-6 w-full max-w-sm flex flex-col gap-4"
      >
        <h2 className="text-lg font-semibold text-slate-100">New Workflow</h2>

        <div className="flex flex-col gap-1">
          <label className="text-xs text-slate-400 font-medium">Name *</label>
          <input
            ref={inputRef}
            value={name}
            onChange={(e) => { setName(e.target.value); setError(null) }}
            className="bg-slate-800 border border-slate-700 rounded px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-1 focus:ring-indigo-500"
            placeholder="My Workflow"
          />
        </div>

        <div className="flex flex-col gap-1">
          <label className="text-xs text-slate-400 font-medium">Description (optional)</label>
          <input
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className="bg-slate-800 border border-slate-700 rounded px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-1 focus:ring-indigo-500"
            placeholder="A short description…"
          />
        </div>

        {error && <p className="text-sm text-red-400">{error}</p>}

        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-1.5 text-sm rounded bg-slate-700 hover:bg-slate-600 text-slate-200 transition-colors"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={creating || !name.trim()}
            className="px-4 py-1.5 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50 transition-colors"
          >
            {creating ? 'Creating…' : 'Create'}
          </button>
        </div>
      </form>
    </div>
  )
}

// ── Help modal ───────────────────────────────────────────────────────────────

const EXAMPLE_YAML = `name: My Workflow
description: Optional description
labels:
  - name: backlog
    color: "#6B7280"
    sort_order: 0
    agent_ignore: true
  - name: in-progress
    color: "#F59E0B"
    sort_order: 1
  - name: done
    color: "#10B981"
    sort_order: 2
    is_terminal: true
transitions:
  - from: backlog
    to: in-progress
    trigger: human
  - from: in-progress
    to: done
    trigger: agent
    path: success
  - from: done
    to: in-progress
    trigger: human`

function WorkflowHelpModal({ onClose }: { onClose: () => void }) {
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="bg-slate-900 border border-slate-700 rounded-lg shadow-xl w-full max-w-2xl max-h-[80vh] flex flex-col"
      >
        <div className="flex items-center justify-between px-6 py-4 border-b border-slate-800 flex-shrink-0">
          <h2 className="text-lg font-semibold text-slate-100">How Workflows Work</h2>
          <button
            onClick={onClose}
            aria-label="Close"
            className="text-slate-500 hover:text-slate-200 transition-colors text-sm leading-none"
          >
            ✕
          </button>
        </div>

        <div className="px-6 py-4 overflow-y-auto flex flex-col gap-5 text-sm text-slate-300">
          <section className="flex flex-col gap-1.5">
            <h3 className="text-slate-100 font-semibold">Overview</h3>
            <p>
              A workflow is a state machine made up of <strong>labels</strong> (the columns on the board)
              and <strong>transitions</strong> (the allowed moves between labels). Tasks start on a label
              and can only move to another label if a matching transition is defined — any other move is
              rejected.
            </p>
          </section>

          <section className="flex flex-col gap-1.5">
            <h3 className="text-slate-100 font-semibold">Labels</h3>
            <ul className="flex flex-col gap-1 list-disc list-inside">
              <li><code className="bg-slate-800 rounded px-1 font-mono">name</code> — unique identifier within the workflow</li>
              <li><code className="bg-slate-800 rounded px-1 font-mono">color</code> — hex color used on the board</li>
              <li><code className="bg-slate-800 rounded px-1 font-mono">sort_order</code> — column order on the board</li>
              <li><code className="bg-slate-800 rounded px-1 font-mono">agent_ignore</code> — agents cannot move tasks here; the dispatcher skips tasks already on this label</li>
              <li><code className="bg-slate-800 rounded px-1 font-mono">is_terminal</code> — marks the task as complete; no further transitions</li>
            </ul>
            <p>
              Tasks created without an explicit label — GitHub Issue imports, scheduled tasks, and API
              creates that omit one — land on the workflow's <strong>human-gate label</strong>: the
              lowest <code className="bg-slate-800 rounded px-1 font-mono">sort_order</code>{' '}
              <code className="bg-slate-800 rounded px-1 font-mono">agent_ignore</code> label (falling back
              to the first label if none is marked <code className="bg-slate-800 rounded px-1 font-mono">agent_ignore</code>),
              so a human promotes them before an agent picks them up. There is no reserved label name — in
              the default workflow this happens to be <code className="bg-slate-800 rounded px-1 font-mono">not_ready</code>.
            </p>
          </section>

          <section className="flex flex-col gap-1.5">
            <h3 className="text-slate-100 font-semibold">Transitions</h3>
            <ul className="flex flex-col gap-1 list-disc list-inside">
              <li><code className="bg-slate-800 rounded px-1 font-mono">from</code> / <code className="bg-slate-800 rounded px-1 font-mono">to</code> — source and destination label names</li>
              <li><code className="bg-slate-800 rounded px-1 font-mono">trigger</code> — <code className="bg-slate-800 rounded px-1 font-mono">agent</code>, <code className="bg-slate-800 rounded px-1 font-mono">human</code>, or <code className="bg-slate-800 rounded px-1 font-mono">both</code>: who is allowed to make this move</li>
              <li><code className="bg-slate-800 rounded px-1 font-mono">path</code> — <code className="bg-slate-800 rounded px-1 font-mono">success</code> or <code className="bg-slate-800 rounded px-1 font-mono">failure</code>: which outcome this transition represents, used by the Approve/Reject actions</li>
            </ul>
            <p>Only transitions you define are allowed — a task can never skip to a label with no matching transition.</p>
          </section>

          <section className="flex flex-col gap-1.5">
            <h3 className="text-slate-100 font-semibold">YAML Example</h3>
            <pre className="bg-slate-800 rounded p-3 font-mono text-xs text-slate-200 overflow-x-auto whitespace-pre">
{EXAMPLE_YAML}
            </pre>
          </section>

          <p className="text-xs text-slate-500">
            For the full reference — including the default workflow, Approve/Reject semantics, and engine
            rules — see <code className="bg-slate-800 rounded px-1 font-mono">docs/workflows.md</code> in the repo.
          </p>
        </div>
      </div>
    </div>
  )
}

// ── Main page ───────────────────────────────────────────────────────────────

export default function WorkflowPage() {
  const { workflows, fetch: fetchWorkflows, setSelectedId } = useWorkflowStore()
  const [selectedWorkflowId, setSelectedWorkflowIdLocal] = useState<string | null>(null)

  const [yaml, setYaml] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [showNewModal, setShowNewModal] = useState(false)
  const [showHelpModal, setShowHelpModal] = useState(false)
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [validationErrors, setValidationErrors] = useState<WorkflowValidationError[]>([])

  // Load a specific workflow's YAML + full data
  const loadWorkflow = (id: string) => {
    setSelectedWorkflowIdLocal(id)
    setSelectedId(id)
    setYaml('')
    setError(null)
    setSaved(false)
    setValidationErrors([])

    api.workflows.get(id).then(setWorkflow).catch((err: unknown) => {
      setError(err instanceof Error ? err.message : 'Failed to load workflow')
    })

    authedRawFetch(api.workflows.exportYaml(id))
      .then((r) => {
        if (!r.ok) throw new Error(`Failed to load YAML (${r.status})`)
        return r.text()
      })
      .then(setYaml)
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : 'Failed to load workflow YAML')
      })
  }

  // Initial load
  useEffect(() => {
    fetchWorkflows().then(() => {
      // fetchWorkflows sets selectedId in the store; use that
      const { workflows: wfs, selectedId } = useWorkflowStore.getState()
      const id = selectedId ?? wfs[0]?.id ?? null
      if (id) loadWorkflow(id)
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // If selected workflow is removed from the list (e.g. after deletion), fall back
  useEffect(() => {
    if (selectedWorkflowId && workflows.length > 0) {
      const still = workflows.find((w) => w.id === selectedWorkflowId)
      if (!still) {
        const fallback = workflows[0]
        if (fallback) loadWorkflow(fallback.id)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflows])

  const handleSave = async () => {
    if (!selectedWorkflowId) return

    setError(null)
    setValidationErrors([])

    // Validate the YAML client-side before sending it to the backend: every
    // label must be reachable from the start label, and terminal labels must
    // have no outgoing transitions.
    let parsed
    try {
      parsed = parseWorkflowYaml(yaml)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Could not parse YAML')
      return
    }
    const errs = validateWorkflow(parsed)
    if (errs.length > 0) {
      setValidationErrors(errs)
      setError(`Workflow has ${errs.length} validation error${errs.length > 1 ? 's' : ''} — fix before saving.`)
      return
    }

    setSaving(true)
    setSaved(false)
    try {
      await api.workflows.updateYaml(selectedWorkflowId, yaml)
      setValidationErrors([])
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
      // Re-fetch the workflow to update the flowchart + store
      api.workflows.get(selectedWorkflowId).then(setWorkflow)
      fetchWorkflows()
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  const handleCreate = (wf: Workflow) => {
    setShowNewModal(false)
    fetchWorkflows().then(() => {
      loadWorkflow(wf.id)
    })
  }

  const handleDelete = async (id: string) => {
    setDeleting(true)
    try {
      await api.workflows.delete(id)
      setDeleteConfirmId(null)
      await fetchWorkflows()
      // Navigate away from the deleted workflow
      const remaining = useWorkflowStore.getState().workflows
      const next = remaining[0]
      if (next) {
        loadWorkflow(next.id)
      } else {
        setSelectedWorkflowIdLocal(null)
        setWorkflow(null)
        setYaml('')
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Delete failed')
      setDeleteConfirmId(null)
    } finally {
      setDeleting(false)
    }
  }

  const noWorkflows = workflows.length === 0

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-4 border-b border-slate-800 flex-shrink-0">
        {/* Workflow Tab Bar */}
        <div className="flex items-center gap-2 overflow-x-auto min-w-0">
          {noWorkflows ? (
            <span className="text-sm text-slate-500">No workflows yet</span>
          ) : (
            workflows.map((wf) => (
              <div key={wf.id} className="relative flex items-center group">
                <button
                  onClick={() => loadWorkflow(wf.id)}
                  className={`px-3 py-1.5 text-sm rounded-md transition-colors whitespace-nowrap ${
                    wf.id === selectedWorkflowId
                      ? 'bg-indigo-700 text-white font-medium'
                      : 'bg-slate-800 text-slate-400 hover:bg-slate-700 hover:text-slate-200'
                  }`}
                >
                  {wf.name}
                </button>
                {/* Delete button — visible on hover or when this tab is active */}
                {deleteConfirmId === wf.id ? (
                  <span className="flex items-center gap-1 ml-1">
                    <button
                      onClick={() => handleDelete(wf.id)}
                      disabled={deleting}
                      className="text-xs px-1.5 py-0.5 rounded bg-red-700 hover:bg-red-600 text-white disabled:opacity-50"
                    >
                      {deleting ? '…' : 'Confirm'}
                    </button>
                    <button
                      onClick={() => setDeleteConfirmId(null)}
                      className="text-xs px-1.5 py-0.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-300"
                    >
                      Cancel
                    </button>
                  </span>
                ) : (
                  <button
                    onClick={() => setDeleteConfirmId(wf.id)}
                    title={`Delete "${wf.name}"`}
                    className="ml-1 opacity-0 group-hover:opacity-100 focus:opacity-100 text-slate-500 hover:text-red-400 transition-opacity text-xs leading-none"
                  >
                    ✕
                  </button>
                )}
              </div>
            ))
          )}

          {/* New Workflow button */}
          <button
            onClick={() => setShowNewModal(true)}
            className="flex items-center gap-1 px-3 py-1.5 text-sm rounded-md bg-slate-800 border border-dashed border-slate-600 text-slate-400 hover:border-slate-400 hover:text-slate-200 transition-colors whitespace-nowrap"
          >
            <span>+</span>
            <span>New Workflow</span>
          </button>
        </div>

        {/* Right actions */}
        <div className="flex items-center gap-3 ml-4 flex-shrink-0">
          {error && <span className="text-sm text-red-400">{error}</span>}
          {saved && <span className="text-sm text-emerald-400">Saved ✓</span>}
          <button
            onClick={() => setShowHelpModal(true)}
            title="How workflows work"
            aria-label="How workflows work"
            className="w-7 h-7 flex items-center justify-center text-sm rounded-full bg-slate-800 hover:bg-slate-700 text-slate-400 hover:text-slate-200 transition-colors"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <line x1="12" y1="16" x2="12" y2="12" />
              <line x1="12" y1="8" x2="12.01" y2="8" />
            </svg>
          </button>
          <button
            onClick={handleSave}
            disabled={saving || !selectedWorkflowId}
            className="px-4 py-1.5 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50 transition-colors"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>

      {/* Empty state */}
      {noWorkflows ? (
        <div className="flex-1 flex items-center justify-center text-slate-500 text-sm">
          No workflows. Create one to get started.
        </div>
      ) : (
        <>
          {/* Split pane: flowchart left, YAML editor right */}
          <div className="flex-1 p-4 min-h-0 flex gap-4 overflow-hidden">
            {/* Left: Flowchart */}
            <div className="flex-1 min-w-0 flex flex-col gap-1">
              <span className="text-xs text-slate-500 font-medium uppercase tracking-wide">
                Visual Preview
                <span className="ml-1 normal-case font-normal">(reflects last saved state)</span>
              </span>
              <div className="flex-1 rounded border border-slate-700 overflow-hidden bg-slate-950">
                <WorkflowFlowchart workflow={workflow} />
              </div>
            </div>

            {/* Right: YAML Editor */}
            <div className="flex-1 min-w-0 flex flex-col gap-1">
              <span className="text-xs text-slate-500 font-medium uppercase tracking-wide">YAML Editor</span>
              <textarea
                value={yaml}
                onChange={(e) => { setYaml(e.target.value); setError(null); setValidationErrors([]) }}
                spellCheck={false}
                className="flex-1 bg-slate-900 border border-slate-700 rounded p-4 text-sm text-slate-100 font-mono focus:outline-none focus:ring-1 focus:ring-indigo-500 resize-none"
                placeholder={selectedWorkflowId ? 'Enter YAML…' : 'Select a workflow to edit'}
              />
              {validationErrors.length > 0 && (
                <div className="mt-1 flex flex-col gap-0.5 rounded border border-red-800 bg-red-950/40 px-3 py-2 max-h-32 overflow-y-auto">
                  {validationErrors.map((e, i) => (
                    <p key={i} className="text-xs text-red-400">
                      {e.label ? `[${e.label}] ` : ''}{e.message}
                    </p>
                  ))}
                </div>
              )}
            </div>
          </div>

          {/* Legend */}
          <div className="flex items-center gap-6 px-6 py-2 border-t border-slate-800 flex-shrink-0">
            <span className="text-xs text-slate-500">Legend:</span>
            <div className="flex items-center gap-1.5">
              <svg width="24" height="8"><line x1="0" y1="4" x2="24" y2="4" stroke="#22c55e" strokeWidth="2" markerEnd="url(#arrow)" /></svg>
              <span className="text-xs text-slate-400">Success (bottom→top)</span>
            </div>
            <div className="flex items-center gap-1.5">
              <svg width="24" height="8"><line x1="0" y1="4" x2="24" y2="4" stroke="#ef4444" strokeWidth="2" /></svg>
              <span className="text-xs text-slate-400">Failure (right=agent / left=human)</span>
            </div>
            <div className="flex items-center gap-1.5">
              <svg width="24" height="8"><line x1="0" y1="4" x2="24" y2="4" stroke="#94a3b8" strokeWidth="2" /></svg>
              <span className="text-xs text-slate-400">Solid = human</span>
            </div>
            <div className="flex items-center gap-1.5">
              <svg width="24" height="8"><line x1="0" y1="4" x2="24" y2="4" stroke="#94a3b8" strokeWidth="2" strokeDasharray="6 3" /></svg>
              <span className="text-xs text-slate-400">Dashed = agent</span>
            </div>
          </div>
        </>
      )}

      {/* New Workflow Modal */}
      {showNewModal && (
        <NewWorkflowModal
          onClose={() => setShowNewModal(false)}
          onCreate={handleCreate}
        />
      )}

      {/* Help Modal */}
      {showHelpModal && <WorkflowHelpModal onClose={() => setShowHelpModal(false)} />}
    </div>
  )
}
