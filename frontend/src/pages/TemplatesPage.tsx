import { useEffect, useState } from 'react'
import { api, type TaskTemplate, type TaskSchedule, type Repo } from '../api/client'

type TemplateForm = { name: string; title: string; description: string; type: string }

const emptyTemplateForm: TemplateForm = { name: '', title: '', description: '', type: 'feature' }

const inputCls =
  'bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500'

// Cron presets shown in the schedule editor. "custom" reveals a raw cron
// text input; the others resolve to a fixed expression.
const CRON_PRESETS: { key: string; label: string; expr: string | null }[] = [
  { key: 'hourly', label: 'Hourly', expr: '0 * * * *' },
  { key: 'daily', label: 'Daily at 06:00', expr: '0 6 * * *' },
  { key: 'weekly-monday', label: 'Weekly on Monday at 06:00', expr: '0 6 * * 1' },
  { key: 'custom', label: 'Custom (raw cron)', expr: null },
]

function presetForExpr(expr: string): string {
  const found = CRON_PRESETS.find((p) => p.expr === expr)
  return found ? found.key : 'custom'
}

export default function TemplatesPage() {
  const [templates, setTemplates] = useState<TaskTemplate[]>([])
  const [repos, setRepos] = useState<Repo[]>([])
  const [schedules, setSchedules] = useState<TaskSchedule[]>([])
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
    Promise.all([api.templates.list(), api.repos.list(), api.schedules.list()])
      .then(([t, r, s]) => {
        setTemplates(t ?? [])
        setRepos(r ?? [])
        setSchedules(s ?? [])
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

function SchedulePanel({
  templateId,
  repos,
  schedules,
  onChange,
}: {
  templateId: string
  repos: Repo[]
  schedules: TaskSchedule[]
  onChange: () => void
}) {
  const [repoId, setRepoId] = useState(repos[0]?.id ?? '')
  const [preset, setPreset] = useState('daily')
  const [cronExpr, setCronExpr] = useState('0 6 * * *')
  const [targetLabel, setTargetLabel] = useState('not_ready')
  const [enabled, setEnabled] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  function handlePresetChange(key: string) {
    setPreset(key)
    const found = CRON_PRESETS.find((p) => p.key === key)
    if (found?.expr) setCronExpr(found.expr)
  }

  async function handleAdd(e: React.FormEvent) {
    e.preventDefault()
    if (!repoId) {
      setError('Select a repo')
      return
    }
    setSaving(true)
    setError('')
    try {
      await api.schedules.create({
        template_id: templateId,
        repo_id: repoId,
        cron_expr: cronExpr.trim(),
        target_label: targetLabel.trim() || 'not_ready',
        enabled,
      })
      onChange()
    } catch (e) {
      setError(String(e))
    } finally {
      setSaving(false)
    }
  }

  async function handleToggle(sched: TaskSchedule) {
    await api.schedules.update(sched.id, {
      cron_expr: sched.cron_expr,
      target_label: sched.target_label,
      enabled: !sched.enabled,
    })
    onChange()
  }

  async function handleDelete(sched: TaskSchedule) {
    if (!confirm('Delete this schedule?')) return
    await api.schedules.delete(sched.id)
    onChange()
  }

  function repoName(id: string) {
    return repos.find((r) => r.id === id)?.name ?? id
  }

  return (
    <div className="border-t border-slate-700 bg-slate-950/50 px-5 py-4 flex flex-col gap-4">
      <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wide">Schedules</h3>

      {schedules.length === 0 ? (
        <p className="text-xs text-slate-500">No schedules for this template yet.</p>
      ) : (
        <div className="flex flex-col gap-2">
          {schedules.map((s) => (
            <div key={s.id} className="flex items-center gap-3 text-xs bg-slate-900 border border-slate-800 rounded-lg px-3 py-2">
              <div className="flex-1 min-w-0">
                <div className="text-slate-200 font-mono">{s.cron_expr}</div>
                <div className="text-slate-500 mt-0.5">
                  {repoName(s.repo_id)} → <span className="text-slate-400">{s.target_label}</span>
                  {s.last_run_at && <span className="ml-2 text-slate-600">last run {new Date(s.last_run_at).toLocaleString()}</span>}
                </div>
              </div>
              <label className="flex items-center gap-1.5 text-slate-400 cursor-pointer shrink-0">
                <input type="checkbox" checked={s.enabled} onChange={() => handleToggle(s)} className="accent-indigo-500" />
                Enabled
              </label>
              <button onClick={() => handleDelete(s)} className="text-slate-600 hover:text-red-400 transition-colors shrink-0">
                Delete
              </button>
            </div>
          ))}
        </div>
      )}

      <form onSubmit={handleAdd} className="flex flex-col gap-3 border-t border-slate-800 pt-4">
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Repo</label>
            <select value={repoId} onChange={(e) => setRepoId(e.target.value)} className={inputCls}>
              <option value="">Select a repo…</option>
              {repos.map((r) => (
                <option key={r.id} value={r.id}>{r.name}</option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Repeat</label>
            <select value={preset} onChange={(e) => handlePresetChange(e.target.value)} className={inputCls}>
              {CRON_PRESETS.map((p) => (
                <option key={p.key} value={p.key}>{p.label}</option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5 col-span-2">
            <label className="text-xs font-medium text-slate-400">
              Cron expression <span className="text-slate-600">(minute hour day-of-month month day-of-week)</span>
            </label>
            <input
              value={cronExpr}
              onChange={(e) => {
                setCronExpr(e.target.value)
                setPreset(presetForExpr(e.target.value))
              }}
              disabled={preset !== 'custom' && CRON_PRESETS.find((p) => p.key === preset)?.expr === cronExpr}
              placeholder="0 6 * * 1"
              className={inputCls + ' font-mono'}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">
              Target label <span className="text-slate-600">(default not_ready)</span>
            </label>
            <input
              value={targetLabel}
              onChange={(e) => setTargetLabel(e.target.value)}
              placeholder="not_ready"
              className={inputCls}
            />
          </div>
          <label className="flex items-center gap-2 text-xs font-medium text-slate-400 cursor-pointer self-end">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} className="accent-indigo-500" />
            Enabled
          </label>
        </div>

        {targetLabel.trim() !== '' && targetLabel.trim() !== 'not_ready' && (
          <p className="text-[11px] text-amber-400/90 bg-amber-500/5 border border-amber-500/20 rounded-lg px-3 py-2">
            ⚠ Starting on label <span className="font-mono">{targetLabel.trim()}</span> instead of{' '}
            <span className="font-mono">not_ready</span> skips human review — the created task goes straight to an
            agent. This enables fully unattended maintenance loops; pair it with a cost budget on the target agent
            config as a safety net.
          </p>
        )}

        {error && <p className="text-xs text-red-400">{error}</p>}

        <div className="flex justify-end">
          <button
            type="submit"
            disabled={saving || !repoId || !cronExpr.trim()}
            className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
          >
            {saving ? 'Adding…' : 'Add Schedule'}
          </button>
        </div>
      </form>
    </div>
  )
}
