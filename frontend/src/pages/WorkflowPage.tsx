import { useEffect, useState } from 'react'
import { api, type Workflow } from '../api/client'
import WorkflowFlowchart from '../components/shared/WorkflowFlowchart'

export default function WorkflowPage() {
  const [workflowId, setWorkflowId] = useState<string | null>(null)
  const [yaml, setYaml] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [workflow, setWorkflow] = useState<Workflow | null>(null)

  useEffect(() => {
    api.workflows.list().then((wfs) => {
      if (!wfs?.length) return
      const wf = wfs[0]
      setWorkflowId(wf.id)

      // Fetch full workflow for the flowchart
      api.workflows.get(wf.id).then(setWorkflow)

      // Fetch YAML for the editor
      fetch(api.workflows.exportYaml(wf.id), {
        headers: import.meta.env.VITE_API_TOKEN
          ? { Authorization: `Bearer ${import.meta.env.VITE_API_TOKEN}` }
          : {},
      })
        .then((r) => r.text())
        .then(setYaml)
    })
  }, [])

  const handleSave = async () => {
    if (!workflowId) return
    setSaving(true)
    setError(null)
    setSaved(false)
    try {
      await api.workflows.updateYaml(workflowId, yaml)
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
      // Re-fetch the workflow to update the flowchart
      api.workflows.get(workflowId).then(setWorkflow)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-6 py-4 border-b border-slate-800 flex-shrink-0">
        <h1 className="text-xl font-semibold text-slate-100">Workflow</h1>
        <div className="flex items-center gap-3">
          {error && (
            <span className="text-sm text-red-400">{error}</span>
          )}
          {saved && (
            <span className="text-sm text-emerald-400">Saved ✓</span>
          )}
          <button
            onClick={handleSave}
            disabled={saving || !workflowId}
            className="px-4 py-1.5 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50 transition-colors"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>

      {/* Split pane: YAML editor left, flowchart right */}
      <div className="flex-1 p-4 min-h-0 flex gap-4 overflow-hidden">
        {/* Left: YAML Editor */}
        <div className="flex-1 min-w-0 flex flex-col gap-1">
          <span className="text-xs text-slate-500 font-medium uppercase tracking-wide">YAML Editor</span>
          <textarea
            value={yaml}
            onChange={(e) => { setYaml(e.target.value); setError(null) }}
            spellCheck={false}
            className="flex-1 bg-slate-900 border border-slate-700 rounded p-4 text-sm text-slate-100 font-mono focus:outline-none focus:ring-1 focus:ring-indigo-500 resize-none"
            placeholder="Loading workflow…"
          />
        </div>

        {/* Right: Flowchart */}
        <div className="flex-1 min-w-0 flex flex-col gap-1">
          <span className="text-xs text-slate-500 font-medium uppercase tracking-wide">
            Visual Preview
            <span className="ml-1 normal-case font-normal">(reflects last saved state)</span>
          </span>
          <div className="flex-1 rounded border border-slate-700 overflow-hidden bg-slate-950">
            <WorkflowFlowchart workflow={workflow} />
          </div>
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
    </div>
  )
}
