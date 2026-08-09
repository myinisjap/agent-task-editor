import { useEffect, useRef, useState } from 'react'
import { api, authedRawFetch, type Workflow } from '../api/client'
import { useWorkflowStore } from '../stores/workflow'
import WorkflowFlowchart from '../components/shared/WorkflowFlowchart'
import { parseWorkflowYaml } from '../lib/parseWorkflowYaml'
import { validateWorkflow, type WorkflowValidationError } from '../lib/validateWorkflow'
import HelpModal from '../components/shared/HelpModal'
import HelpButton from '../components/shared/HelpButton'
import { WorkflowHelp } from '../components/shared/pageHelp'
import ModalShell from '../components/shared/ModalShell'

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
    <ModalShell
      onClose={onClose}
      ariaLabel="New Workflow"
      initialFocusRef={inputRef}
      className="bg-slate-900 border border-slate-700 rounded-lg shadow-xl p-6 w-full max-w-sm flex flex-col gap-4"
    >
      <form
        onSubmit={handleSubmit}
        className="flex flex-col gap-4"
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
    </ModalShell>
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

  // Monotonic id of the most recent loadWorkflow() call. Every async
  // continuation captures the value at its start and bails if a newer load
  // has begun, so a slow response for workflow A can never overwrite the
  // editor / flowchart / error banner after the user has clicked workflow B
  // (#332).
  const loadReqRef = useRef(0)
  // The workflow id the YAML currently in the textarea was loaded for. Used
  // as a belt-and-braces guard in handleSave so we can never PUT one
  // workflow's YAML onto another. null while a load is in flight / after a
  // failed load.
  const loadedYamlIdRef = useRef<string | null>(null)
  // Mirrors loadedYamlIdRef in state so the Save button's `disabled` can
  // react to it (refs don't trigger re-renders). handleSave itself reads the
  // ref, not this, since state can lag inside an async handler.
  const [yamlLoadedId, setYamlLoadedId] = useState<string | null>(null)

  // Load a specific workflow's YAML + full data
  const loadWorkflow = (id: string) => {
    const req = ++loadReqRef.current
    loadedYamlIdRef.current = null
    setYamlLoadedId(null)
    setSelectedWorkflowIdLocal(id)
    setSelectedId(id)
    setYaml('')
    setError(null)
    setSaved(false)
    setValidationErrors([])
    // Loading placeholder — WorkflowFlowchart renders a "Loading chart…"
    // state for `workflow === null`, so this avoids showing workflow A's
    // graph while B's data is still in flight.
    setWorkflow(null)

    api.workflows.get(id)
      .then((wf) => { if (req === loadReqRef.current) setWorkflow(wf) })
      .catch((err: unknown) => {
        if (req !== loadReqRef.current) return
        setError(err instanceof Error ? err.message : 'Failed to load workflow')
      })

    authedRawFetch(api.workflows.exportYaml(id))
      .then((r) => {
        if (!r.ok) throw new Error(`Failed to load YAML (${r.status})`)
        return r.text()
      })
      .then((text) => {
        if (req !== loadReqRef.current) return
        loadedYamlIdRef.current = id
        setYamlLoadedId(id)
        setYaml(text)
      })
      .catch((err: unknown) => {
        if (req !== loadReqRef.current) return
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

    // The YAML in the editor must belong to the workflow we're about to
    // write. Cannot happen now that loadWorkflow sequences its responses,
    // but a save that silently replaces workflow B's labels/transitions
    // with A's is destructive enough to double-check (#332).
    if (loadedYamlIdRef.current !== selectedWorkflowId) {
      setError('Workflow YAML is still loading — try again in a moment.')
      return
    }

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
    const savedId = selectedWorkflowId
    const req = loadReqRef.current
    try {
      await api.workflows.updateYaml(savedId, yaml)
      if (req === loadReqRef.current) {
        setValidationErrors([])
        setSaved(true)
        setTimeout(() => setSaved(false), 2000)
      }
      // Re-fetch the workflow to update the flowchart + store
      api.workflows.get(savedId).then((wf) => {
        if (req === loadReqRef.current) setWorkflow(wf)
      }).catch(() => {})
      fetchWorkflows()
    } catch (err: unknown) {
      if (req === loadReqRef.current) {
        setError(err instanceof Error ? err.message : 'Save failed')
      }
    } finally {
      // Must stay unconditional — otherwise the button can stick on
      // "Saving…" if the user switched workflows during the request.
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
    <div className="flex flex-col h-full" data-testid="workflow-page">
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
          <HelpButton onClick={() => setShowHelpModal(true)} title="How workflows work" />
          <button
            onClick={handleSave}
            disabled={saving || !selectedWorkflowId || yamlLoadedId !== selectedWorkflowId}
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
          <div className="flex-1 p-4 min-h-0 flex flex-col md:flex-row gap-4 overflow-auto md:overflow-hidden">
            {/* Left: Flowchart */}
            <div className="flex-1 min-w-0 min-h-[40vh] md:min-h-0 flex flex-col gap-1">
              <span className="text-xs text-slate-500 font-medium uppercase tracking-wide">
                Visual Preview
                <span className="ml-1 normal-case font-normal">(reflects last saved state)</span>
              </span>
              <div className="flex-1 rounded border border-slate-700 overflow-hidden bg-slate-950">
                <WorkflowFlowchart workflow={workflow} />
              </div>
            </div>

            {/* Right: YAML Editor */}
            <div className="flex-1 min-w-0 min-h-[40vh] md:min-h-0 flex flex-col gap-1">
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
          <div className="flex flex-wrap items-center gap-x-6 gap-y-2 px-6 py-2 border-t border-slate-800 flex-shrink-0">
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
      {showHelpModal && (
        <HelpModal title="How Workflows Work" onClose={() => setShowHelpModal(false)}>
          <WorkflowHelp />
        </HelpModal>
      )}
    </div>
  )
}
