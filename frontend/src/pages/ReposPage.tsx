import { useEffect, useState } from 'react'
import { api, type Repo, type Workflow } from '../api/client'

export default function ReposPage() {
  const [repos, setRepos] = useState<Repo[]>([])
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ name: '', path: '', remote_url: '', workflow_id: '' })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([api.repos.list(), api.workflows.list()])
      .then(([r, w]) => {
        setRepos(r ?? [])
        setWorkflows(w ?? [])
      })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false))
  }, [])

  function workflowName(id?: string | null) {
    if (!id) return '—'
    return workflows.find((w) => w.id === id)?.name ?? id
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setError('')
    try {
      const repo = await api.repos.create({
        name: form.name.trim(),
        path: form.path.trim(),
        remote_url: form.remote_url.trim() || undefined,
        workflow_id: form.workflow_id || undefined,
      })
      setRepos((r) => [...r, repo])
      setShowForm(false)
      setForm({ name: '', path: '', remote_url: '', workflow_id: '' })
    } catch (e) {
      setError(String(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete(repo: Repo) {
    if (!confirm(`Remove repo "${repo.name}"?`)) return
    await api.repos.delete(repo.id)
    setRepos((r) => r.filter((x) => x.id !== repo.id))
  }

  return (
    <div className="p-6 max-w-3xl">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-slate-100">Repos</h1>
        <button
          onClick={() => setShowForm((v) => !v)}
          className="px-3 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg transition-colors"
        >
          {showForm ? 'Cancel' : '+ Add Repo'}
        </button>
      </div>

      {showForm && (
        <form
          onSubmit={handleCreate}
          className="mb-6 bg-slate-900 border border-slate-700 rounded-xl p-5 flex flex-col gap-4"
        >
          <h2 className="text-sm font-semibold text-slate-200">New Repo</h2>

          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Name</label>
              <input
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                required
                placeholder="my-project"
                className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Local path</label>
              <input
                value={form.path}
                onChange={(e) => setForm((f) => ({ ...f, path: e.target.value }))}
                required
                placeholder="/home/user/projects/my-project"
                className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Remote URL (optional)</label>
              <input
                value={form.remote_url}
                onChange={(e) => setForm((f) => ({ ...f, remote_url: e.target.value }))}
                placeholder="https://github.com/org/repo"
                className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Workflow</label>
              <select
                value={form.workflow_id}
                onChange={(e) => setForm((f) => ({ ...f, workflow_id: e.target.value }))}
                className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              >
                <option value="">None</option>
                {workflows.map((w) => (
                  <option key={w.id} value={w.id}>{w.name}</option>
                ))}
              </select>
            </div>
          </div>

          {error && <p className="text-xs text-red-400">{error}</p>}

          <div className="flex justify-end">
            <button
              type="submit"
              disabled={saving || !form.name.trim() || !form.path.trim()}
              className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
            >
              {saving ? 'Adding…' : 'Add Repo'}
            </button>
          </div>
        </form>
      )}

      {loading ? (
        <div className="text-slate-400 text-sm">Loading…</div>
      ) : error ? (
        <div className="text-red-400 text-sm">{error}</div>
      ) : repos.length === 0 ? (
        <div className="text-slate-500 text-sm">No repos added yet.</div>
      ) : (
        <div className="flex flex-col gap-2">
          {repos.map((repo) => (
            <div
              key={repo.id}
              className="bg-slate-900 border border-slate-800 rounded-xl px-5 py-4 flex items-center gap-4"
            >
              <div className="flex-1 min-w-0">
                <div className="text-sm font-medium text-slate-100">{repo.name}</div>
                <div className="text-xs text-slate-500 font-mono mt-0.5 truncate">{repo.path}</div>
                {repo.remote_url && (
                  <div className="text-xs text-slate-600 mt-0.5 truncate">{repo.remote_url}</div>
                )}
              </div>
              <div className="text-xs text-slate-500 shrink-0">
                {workflowName(repo.workflow_id)}
              </div>
              <button
                onClick={() => handleDelete(repo)}
                className="text-xs text-slate-600 hover:text-red-400 transition-colors shrink-0"
              >
                Remove
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
