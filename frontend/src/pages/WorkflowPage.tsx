import { useEffect, useState } from 'react'
import { api } from '../api/client'

export default function WorkflowPage() {
  const [workflowId, setWorkflowId] = useState<string | null>(null)
  const [yaml, setYaml] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    api.workflows.list().then((wfs) => {
      if (!wfs?.length) return
      const wf = wfs[0]
      setWorkflowId(wf.id)
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
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-6 py-4 border-b border-slate-800">
        <h1 className="text-xl font-semibold text-slate-100">Workflow</h1>
        <div className="flex items-center gap-3">
          {error && (
            <span className="text-sm text-red-400">{error}</span>
          )}
          {saved && (
            <span className="text-sm text-emerald-400">Saved</span>
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
      <div className="flex-1 p-4 min-h-0">
        <textarea
          value={yaml}
          onChange={(e) => { setYaml(e.target.value); setError(null) }}
          spellCheck={false}
          className="w-full h-full bg-slate-900 border border-slate-700 rounded p-4 text-sm text-slate-100 font-mono focus:outline-none focus:ring-1 focus:ring-indigo-500 resize-none"
          placeholder="Loading workflow…"
        />
      </div>
    </div>
  )
}
