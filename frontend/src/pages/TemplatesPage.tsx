import { useEffect, useState } from 'react'
import { api, type TaskTemplate, type TaskSchedule, type Repo, type Workflow } from '../api/client'
import SchedulePanel from '../components/templates/SchedulePanel'

type TemplateForm = { name: string; title: string; description: string; type: string }

const emptyTemplateForm: TemplateForm = { name: '', title: '', description: '', type: 'feature' }

export const inputCls =
  'bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500'

export default function TemplatesPage() {
  const [templates, setTemplates] = useState<TaskTemplate[]>([])
  const [repos, setRepos] = useState<Repo[]>([])
  const [schedules, setSchedules] = useState<TaskSchedule[]>([])
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState<TemplateForm>(emptyTemplateForm)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState('')

  const [editingId, setEditingId] = useState<string | null>(null)
  const [editForm, setEditForm] = useState<TemplateForm>(emptyTemplateForm)
  const [editSaving, setEditSaving] = useState(false)
  const [editError, setEditError] = useState('')

  // Which template's schedule panel is expanded.
  const [expandedId, setExpandedId] = useState<string | null>(null)

  function reload() {
    setLoading(true)
    Promise.all([api.templates.list(), api.repos.list(), api.schedules.list(), api.workflows.list()])
      .then(([t, r, s, w]) => {
        setTemplates(t ?? [])
        setRepos(r ?? [])
        setSchedules(s ?? [])
        setWorkflows(w ?? [])
      })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    reload()
  }, [])

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setFormError('')
    try {
      const tpl = await api.templates.create({
        name: form.name.trim(),
        title: form.title.trim(),
        description: form.description.trim(),
        type: form.type,
      })
      setTemplates((t) => [...t, tpl])
      setShowForm(false)
      setForm(emptyTemplateForm)
    } catch (e) {
      setFormError(String(e))
    } finally {
      setSaving(false)
    }
  }

  function startEdit(tpl: TaskTemplate) {
    setEditingId(tpl.id)
    setEditForm({ name: tpl.name, title: tpl.title, description: tpl.description, type: tpl.type })
    setEditError('')
  }

  function cancelEdit() {
    setEditingId(null)
    setEditForm(emptyTemplateForm)
    setEditError('')
  }

  async function handleUpdate(e: React.FormEvent) {
    e.preventDefault()
    if (!editingId) return
    setEditSaving(true)
    setEditError('')
    try {
      const updated = await api.templates.update(editingId, {
        name: editForm.name.trim(),
        title: editForm.title.trim(),
        description: editForm.description.trim(),
        type: editForm.type,
      })
      setTemplates((t) => t.map((x) => (x.id === editingId ? updated : x)))
      cancelEdit()
    } catch (e) {
      setEditError(String(e))
    } finally {
      setEditSaving(false)
    }
  }

  async function handleDeleteTemplate(tpl: TaskTemplate) {
    if (!confirm(`Delete template "${tpl.name}"? Its schedules will be deleted too.`)) return
    await api.templates.delete(tpl.id)
    setTemplates((t) => t.filter((x) => x.id !== tpl.id))
    setSchedules((s) => s.filter((x) => x.template_id !== tpl.id))
  }

  return (
    <div className="p-6 max-w-4xl">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-slate-100">Task Templates</h1>
        <button
          onClick={() => setShowForm((v) => !v)}
          className="px-3 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg transition-colors"
        >
          {showForm ? 'Cancel' : '+ Add Template'}
        </button>
      </div>

      {showForm && (
        <form
          onSubmit={handleCreate}
          className="mb-6 bg-slate-900 border border-slate-700 rounded-xl p-5 flex flex-col gap-4"
        >
          <h2 className="text-sm font-semibold text-slate-200">New Template</h2>
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Name</label>
              <input
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="Upgrade dependency"
                className={inputCls}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <label className="text-xs font-medium text-slate-400">Type</label>
              <select
                value={form.type}
                onChange={(e) => setForm((f) => ({ ...f, type: e.target.value }))}
                className={inputCls}
              >
                <option value="feature">feature</option>
                <option value="bug">bug</option>
                <option value="chore">chore</option>
                <option value="spike">spike</option>
              </select>
            </div>
            <div className="flex flex-col gap-1.5 col-span-2">
              <label className="text-xs font-medium text-slate-400">Task title</label>
              <input
                value={form.title}
                onChange={(e) => setForm((f) => ({ ...f, title: e.target.value }))}
                placeholder="Upgrade <package> to latest"
                className={inputCls}
              />
            </div>
            <div className="flex flex-col gap-1.5 col-span-2">
              <label className="text-xs font-medium text-slate-400">Task description</label>
              <textarea
                value={form.description}
                onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                placeholder="Bump the version, run tests, note breaking changes."
                rows={3}
                className={inputCls}
              />
            </div>
          </div>
          {formError && <p className="text-xs text-red-400">{formError}</p>}
          <div className="flex justify-end">
            <button
              type="submit"
              disabled={saving || !form.name.trim()}
              className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
            >
              {saving ? 'Adding…' : 'Add Template'}
            </button>
          </div>
        </form>
      )}

      {loading ? (
        <div className="text-slate-400 text-sm">Loading…</div>
      ) : error && templates.length === 0 ? (
        <div className="text-red-400 text-sm">{error}</div>
      ) : templates.length === 0 ? (
        <div className="text-slate-500 text-sm">No templates yet.</div>
      ) : (
        <div className="flex flex-col gap-2">
          {templates.map((tpl) => (
            <div key={tpl.id} className="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden">
              <div className="px-5 py-4 flex items-center gap-4">
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium text-slate-100">{tpl.name}</div>
                  <div className="text-xs text-slate-500 mt-0.5 truncate">{tpl.title || '—'}</div>
                </div>
                <span className="text-[10px] px-2 py-0.5 rounded-full bg-slate-800 text-slate-400 border border-slate-700 shrink-0">
                  {tpl.type}
                </span>
                <button
                  onClick={() => setExpandedId(expandedId === tpl.id ? null : tpl.id)}
                  className="text-xs text-slate-500 hover:text-indigo-400 transition-colors shrink-0"
                >
                  {expandedId === tpl.id ? 'Hide schedules' : 'Schedules'}
                  {' '}
                  ({schedules.filter((s) => s.template_id === tpl.id).length})
                </button>
                <button
                  onClick={() => (editingId === tpl.id ? cancelEdit() : startEdit(tpl))}
                  className="text-xs text-slate-500 hover:text-indigo-400 transition-colors shrink-0"
                >
                  {editingId === tpl.id ? 'Cancel' : 'Edit'}
                </button>
                <button
                  onClick={() => handleDeleteTemplate(tpl)}
                  className="text-xs text-slate-600 hover:text-red-400 transition-colors shrink-0"
                >
                  Delete
                </button>
              </div>

              {editingId === tpl.id && (
                <form
                  onSubmit={handleUpdate}
                  className="border-t border-slate-700 bg-slate-900 px-5 py-4 flex flex-col gap-4"
                >
                  <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">Edit Template</h3>
                  <div className="grid grid-cols-2 gap-4">
                    <div className="flex flex-col gap-1.5">
                      <label className="text-xs font-medium text-slate-400">Name</label>
                      <input
                        value={editForm.name}
                        onChange={(e) => setEditForm((f) => ({ ...f, name: e.target.value }))}
                        className={inputCls}
                      />
                    </div>
                    <div className="flex flex-col gap-1.5">
                      <label className="text-xs font-medium text-slate-400">Type</label>
                      <select
                        value={editForm.type}
                        onChange={(e) => setEditForm((f) => ({ ...f, type: e.target.value }))}
                        className={inputCls}
                      >
                        <option value="feature">feature</option>
                        <option value="bug">bug</option>
                        <option value="chore">chore</option>
                        <option value="spike">spike</option>
                      </select>
                    </div>
                    <div className="flex flex-col gap-1.5 col-span-2">
                      <label className="text-xs font-medium text-slate-400">Task title</label>
                      <input
                        value={editForm.title}
                        onChange={(e) => setEditForm((f) => ({ ...f, title: e.target.value }))}
                        className={inputCls}
                      />
                    </div>
                    <div className="flex flex-col gap-1.5 col-span-2">
                      <label className="text-xs font-medium text-slate-400">Task description</label>
                      <textarea
                        value={editForm.description}
                        onChange={(e) => setEditForm((f) => ({ ...f, description: e.target.value }))}
                        rows={3}
                        className={inputCls}
                      />
                    </div>
                  </div>
                  {editError && <p className="text-xs text-red-400">{editError}</p>}
                  <div className="flex items-center justify-end gap-2">
                    <button type="button" onClick={cancelEdit} className="px-3 py-1.5 text-sm text-slate-400 hover:text-slate-200 transition-colors">
                      Cancel
                    </button>
                    <button
                      type="submit"
                      disabled={editSaving || !editForm.name.trim()}
                      className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
                    >
                      {editSaving ? 'Saving…' : 'Save'}
                    </button>
                  </div>
                </form>
              )}

              {expandedId === tpl.id && (
                <SchedulePanel
                  templateId={tpl.id}
                  repos={repos}
                  workflows={workflows}
                  schedules={schedules.filter((s) => s.template_id === tpl.id)}
                  onChange={reload}
                />
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
