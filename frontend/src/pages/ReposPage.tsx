import { useEffect, useState } from 'react'
import { api, type Repo, type Workflow } from '../api/client'

type EditForm = { name: string; path: string; remote_url: string; workflow_id: string; issue_sync_enabled: boolean; issue_sync_label: string; issue_writeback_enabled: boolean; pr_review_auto_transition_enabled: boolean }

export default function ReposPage() {
  const [repos, setRepos] = useState<Repo[]>([])
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ name: '', path: '', remote_url: '', workflow_id: '', issue_sync_enabled: false, issue_sync_label: '', issue_writeback_enabled: false, pr_review_auto_transition_enabled: false })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  // Inline edit state
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editForm, setEditForm] = useState<EditForm>({ name: '', path: '', remote_url: '', workflow_id: '', issue_sync_enabled: false, issue_sync_label: '', issue_writeback_enabled: false, pr_review_auto_transition_enabled: false })
  const [editSaving, setEditSaving] = useState(false)
  const [editError, setEditError] = useState('')

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

  /** Parse org/repo from a GitHub URL for auto-filling the Name field. */
  function parseGitHubName(url: string): string {
    const trimmed = url.trim()
    // HTTPS: https://github.com/org/repo[.git]
    const httpsMatch = trimmed.match(/^https:\/\/github\.com\/([^/]+)\/([^/]+?)(?:\.git)?\/?$/)
    if (httpsMatch) return `${httpsMatch[1]}/${httpsMatch[2]}`
    // SSH: git@github.com:org/repo[.git]
    const sshMatch = trimmed.match(/^git@github\.com:([^/]+)\/([^/]+?)(?:\.git)?$/)
    if (sshMatch) return `${sshMatch[1]}/${sshMatch[2]}`
    return ''
  }

  function handleRemoteUrlChange(e: React.ChangeEvent<HTMLInputElement>) {
    const url = e.target.value
    setForm((f) => {
      const parsed = parseGitHubName(url)
      return {
        ...f,
        remote_url: url,
        // Auto-fill name only when it hasn't been manually set
        name: f.name === '' || f.name === parseGitHubName(f.remote_url) ? parsed : f.name,
      }
    })
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setError('')
    try {
      const repo = await api.repos.create({
        name: form.name.trim() || undefined,
        path: form.path.trim() || undefined,
        remote_url: form.remote_url.trim() || undefined,
        workflow_id: form.workflow_id || undefined,
        issue_sync_enabled: form.issue_sync_enabled,
        issue_sync_label: form.issue_sync_label.trim(),
        issue_writeback_enabled: form.issue_writeback_enabled,
        pr_review_auto_transition_enabled: form.pr_review_auto_transition_enabled,
      })
      setRepos((r) => [...r, repo])
      setShowForm(false)
      setForm({ name: '', path: '', remote_url: '', workflow_id: '', issue_sync_enabled: false, issue_sync_label: '', issue_writeback_enabled: false, pr_review_auto_transition_enabled: false })
    } catch (e) {
      setError(String(e))
    } finally {
      setSaving(false)
    }
  }

  function startEdit(repo: Repo) {
    setEditingId(repo.id)
    setEditForm({
      name: repo.name,
      path: repo.path,
      remote_url: repo.remote_url ?? '',
      workflow_id: repo.workflow_id ?? '',
      issue_sync_enabled: !!repo.issue_sync_enabled,
      issue_sync_label: repo.issue_sync_label ?? '',
      issue_writeback_enabled: !!repo.issue_writeback_enabled,
      pr_review_auto_transition_enabled: !!repo.pr_review_auto_transition_enabled,
    })
    setEditError('')
  }

  function cancelEdit() {
    setEditingId(null)
    setEditForm({ name: '', path: '', remote_url: '', workflow_id: '', issue_sync_enabled: false, issue_sync_label: '', issue_writeback_enabled: false, pr_review_auto_transition_enabled: false })
    setEditError('')
  }

  async function handleUpdate(e: React.FormEvent) {
    e.preventDefault()
    if (!editingId) return
    setEditSaving(true)
    setEditError('')
    try {
      const updated = await api.repos.update(editingId, {
        name: editForm.name.trim() || undefined,
        path: editForm.path.trim() || undefined,
        remote_url: editForm.remote_url.trim() || null,
        workflow_id: editForm.workflow_id || null,
        issue_sync_enabled: editForm.issue_sync_enabled,
        issue_sync_label: editForm.issue_sync_label.trim(),
        issue_writeback_enabled: editForm.issue_writeback_enabled,
        pr_review_auto_transition_enabled: editForm.pr_review_auto_transition_enabled,
      })
      setRepos((r) => r.map((x) => (x.id === editingId ? updated : x)))
      cancelEdit()
    } catch (e) {
      setEditError(String(e))
    } finally {
      setEditSaving(false)
    }
  }

  async function handleDelete(repo: Repo) {
    if (!confirm(`Remove repo "${repo.name}"?`)) return
    await api.repos.delete(repo.id)
    setRepos((r) => r.filter((x) => x.id !== repo.id))
  }

  const inputCls =
    'bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500'

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
              <label className="text-xs font-medium text-slate-400">
                Name <span className="text-slate-600">(auto-filled from GitHub URL)</span>
              </label>
              <input
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="org/repo"
                className={inputCls}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">
                Local path <span className="text-slate-600">(optional — leave blank to auto-clone)</span>
              </label>
              <input
                value={form.path}
                onChange={(e) => setForm((f) => ({ ...f, path: e.target.value }))}
                placeholder="Leave blank to auto-clone via Remote URL"
                className={inputCls}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Remote URL</label>
              <input
                value={form.remote_url}
                onChange={handleRemoteUrlChange}
                placeholder="https://github.com/org/repo"
                className={inputCls}
              />
            </div>

            <label className="flex items-center gap-2 text-xs font-medium text-slate-400 cursor-pointer">
              <input
                type="checkbox"
                checked={form.issue_sync_enabled}
                onChange={(e) => setForm((f) => ({ ...f, issue_sync_enabled: e.target.checked }))}
                className="accent-indigo-500"
              />
              Import GitHub Issues as tasks
              <span className="text-slate-600">(requires remote URL + workflow)</span>
            </label>

            {form.issue_sync_enabled && (
              <>
                <div className="flex flex-col gap-1.5">
                  <label className="text-xs font-medium text-slate-400">
                    Workflow <span className="text-slate-600">(imported issues become tasks here)</span>
                  </label>
                  <select
                    value={form.workflow_id}
                    onChange={(e) => setForm((f) => ({ ...f, workflow_id: e.target.value }))}
                    className={inputCls}
                  >
                    <option value="">None</option>
                    {workflows.map((w) => (
                      <option key={w.id} value={w.id}>{w.name}</option>
                    ))}
                  </select>
                </div>

                <div className="flex flex-col gap-1.5">
                  <label className="text-xs font-medium text-slate-400">
                    Issue label filter <span className="text-slate-600">(empty = all open issues)</span>
                  </label>
                  <input
                    value={form.issue_sync_label}
                    onChange={(e) => setForm((f) => ({ ...f, issue_sync_label: e.target.value }))}
                    placeholder="agent-ok"
                    className={inputCls}
                  />
                </div>
              </>
            )}

            <label className="flex items-center gap-2 text-xs font-medium text-slate-400 cursor-pointer">
              <input
                type="checkbox"
                checked={form.issue_writeback_enabled}
                onChange={(e) => setForm((f) => ({ ...f, issue_writeback_enabled: e.target.checked }))}
                className="accent-indigo-500"
              />
              Issue write-back
              <span className="text-slate-600">
                (comment on the source issue when a PR opens, close it when merged; requires remote URL)
              </span>
            </label>

            <label className="flex items-center gap-2 text-xs font-medium text-slate-400 cursor-pointer">
              <input
                type="checkbox"
                checked={form.pr_review_auto_transition_enabled}
                onChange={(e) => setForm((f) => ({ ...f, pr_review_auto_transition_enabled: e.target.checked }))}
                className="accent-indigo-500"
              />
              Auto-transition to work on PR changes-requested
              <span className="text-slate-600">
                (move a task back to its failure-path label when GitHub reports a changes-requested review, new review comment, or failed check; requires remote URL)
              </span>
            </label>
          </div>

          {error && <p className="text-xs text-red-400">{error}</p>}

          <div className="flex justify-end">
            <button
              type="submit"
              disabled={saving || (!form.name.trim() && !form.remote_url.trim()) || (!form.path.trim() && !form.remote_url.trim())}
              className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
            >
              {saving ? 'Adding…' : 'Add Repo'}
            </button>
          </div>
        </form>
      )}

      {loading ? (
        <div className="text-slate-400 text-sm">Loading…</div>
      ) : error && repos.length === 0 ? (
        <div className="text-red-400 text-sm">{error}</div>
      ) : repos.length === 0 ? (
        <div className="text-slate-500 text-sm">No repos added yet.</div>
      ) : (
        <div className="flex flex-col gap-2">
          {repos.map((repo) => (
            <div key={repo.id} className="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden">
              {/* Repo row header — stacks on mobile, single row from sm up */}
              <div className="px-5 py-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:gap-4">
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium text-slate-100 break-words">{repo.name}</div>
                  <div className="text-xs text-slate-500 font-mono mt-0.5 truncate">{repo.path}</div>
                  {repo.remote_url && (
                    <div className="text-xs text-slate-600 mt-0.5 truncate">{repo.remote_url}</div>
                  )}
                </div>
                <div className="flex flex-wrap items-center gap-2 sm:justify-end">
                  {!!repo.issue_sync_enabled && (
                    <span
                      className="text-[10px] px-2 py-0.5 rounded-full bg-indigo-500/10 text-indigo-400 border border-indigo-500/30 shrink-0"
                      title={`Importing open GitHub issues${repo.issue_sync_label ? ` labeled "${repo.issue_sync_label}"` : ''} as tasks into the "${workflowName(repo.workflow_id)}" workflow`}
                    >
                      Issue sync{repo.issue_sync_label ? `: ${repo.issue_sync_label}` : ''}
                    </span>
                  )}
                  {!!repo.issue_writeback_enabled && (
                    <span
                      className="text-[10px] px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/30 shrink-0"
                      title="Commenting on the source issue when a task's PR opens, and closing it when the PR merges"
                    >
                      Write-back
                    </span>
                  )}
                  {!!repo.pr_review_auto_transition_enabled && (
                    <span
                      className="text-[10px] px-2 py-0.5 rounded-full bg-amber-500/10 text-amber-400 border border-amber-500/30 shrink-0"
                      title="Auto-transitioning tasks back to their failure-path label on GitHub changes-requested reviews, new review comments, or failed checks"
                    >
                      PR auto-transition
                    </span>
                  )}
                  <button
                    onClick={() => editingId === repo.id ? cancelEdit() : startEdit(repo)}
                    className="text-xs text-slate-500 hover:text-indigo-400 transition-colors shrink-0"
                  >
                    {editingId === repo.id ? 'Cancel' : 'Edit'}
                  </button>
                  <button
                    onClick={() => handleDelete(repo)}
                    className="text-xs text-slate-600 hover:text-red-400 transition-colors shrink-0"
                  >
                    Remove
                  </button>
                </div>
              </div>

              {/* Inline edit form */}
              {editingId === repo.id && (
                <form
                  onSubmit={handleUpdate}
                  className="border-t border-slate-700 bg-slate-900 px-5 py-4 flex flex-col gap-4"
                >
                  <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">Edit Repo</h3>

                  <div className="grid grid-cols-2 gap-4">
                    <div className="flex flex-col gap-1.5">
                      <label className="text-xs font-medium text-slate-400">Name</label>
                      <input
                        value={editForm.name}
                        onChange={(e) => setEditForm((f) => ({ ...f, name: e.target.value }))}
                        placeholder="org/repo"
                        className={inputCls}
                      />
                    </div>

                    <div className="flex flex-col gap-1.5">
                      <label className="text-xs font-medium text-slate-400">Local path</label>
                      <input
                        value={editForm.path}
                        onChange={(e) => setEditForm((f) => ({ ...f, path: e.target.value }))}
                        placeholder="/path/to/repo"
                        className={inputCls}
                      />
                    </div>

                    <div className="flex flex-col gap-1.5">
                      <label className="text-xs font-medium text-slate-400">Remote URL</label>
                      <input
                        value={editForm.remote_url}
                        onChange={(e) => setEditForm((f) => ({ ...f, remote_url: e.target.value }))}
                        placeholder="https://github.com/org/repo"
                        className={inputCls}
                      />
                    </div>

                    <label className="flex items-center gap-2 text-xs font-medium text-slate-400 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={editForm.issue_sync_enabled}
                        onChange={(e) => setEditForm((f) => ({ ...f, issue_sync_enabled: e.target.checked }))}
                        className="accent-indigo-500"
                      />
                      Import GitHub Issues as tasks
                      <span className="text-slate-600">(requires remote URL + workflow)</span>
                    </label>

                    {editForm.issue_sync_enabled && (
                      <>
                        <div className="flex flex-col gap-1.5">
                          <label className="text-xs font-medium text-slate-400">
                            Workflow <span className="text-slate-600">(imported issues become tasks here)</span>
                          </label>
                          <select
                            value={editForm.workflow_id}
                            onChange={(e) => setEditForm((f) => ({ ...f, workflow_id: e.target.value }))}
                            className={inputCls}
                          >
                            <option value="">None</option>
                            {workflows.map((w) => (
                              <option key={w.id} value={w.id}>{w.name}</option>
                            ))}
                          </select>
                        </div>

                        <div className="flex flex-col gap-1.5">
                          <label className="text-xs font-medium text-slate-400">
                            Issue label filter <span className="text-slate-600">(empty = all open issues)</span>
                          </label>
                          <input
                            value={editForm.issue_sync_label}
                            onChange={(e) => setEditForm((f) => ({ ...f, issue_sync_label: e.target.value }))}
                            placeholder="agent-ok"
                            className={inputCls}
                          />
                        </div>
                      </>
                    )}

                    <label className="flex items-center gap-2 text-xs font-medium text-slate-400 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={editForm.issue_writeback_enabled}
                        onChange={(e) => setEditForm((f) => ({ ...f, issue_writeback_enabled: e.target.checked }))}
                        className="accent-indigo-500"
                      />
                      Issue write-back
                      <span className="text-slate-600">
                        (comment on the source issue when a PR opens, close it when merged; requires remote URL)
                      </span>
                    </label>

                    <label className="flex items-center gap-2 text-xs font-medium text-slate-400 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={editForm.pr_review_auto_transition_enabled}
                        onChange={(e) => setEditForm((f) => ({ ...f, pr_review_auto_transition_enabled: e.target.checked }))}
                        className="accent-indigo-500"
                      />
                      Auto-transition to work on PR changes-requested
                      <span className="text-slate-600">
                        (move a task back to its failure-path label when GitHub reports a changes-requested review, new review comment, or failed check; requires remote URL)
                      </span>
                    </label>
                  </div>

                  {editError && <p className="text-xs text-red-400">{editError}</p>}

                  <div className="flex items-center justify-end gap-2">
                    <button
                      type="button"
                      onClick={cancelEdit}
                      className="px-3 py-1.5 text-sm text-slate-400 hover:text-slate-200 transition-colors"
                    >
                      Cancel
                    </button>
                    <button
                      type="submit"
                      disabled={editSaving || !editForm.name.trim() || !editForm.path.trim()}
                      className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
                    >
                      {editSaving ? 'Saving…' : 'Save'}
                    </button>
                  </div>
                </form>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
