import { useEffect, useState } from 'react'
import { api, type TaskSchedule, type Repo, type Workflow } from '../../api/client'
import { inputCls } from '../../pages/TemplatesPage'

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

export default function SchedulePanel({
  templateId,
  repos,
  workflows,
  schedules,
  onChange,
}: {
  templateId: string
  repos: Repo[]
  workflows: Workflow[]
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

  // target_label must be one of the selected repo's workflow's labels (the
  // API rejects anything else with a 400) — drive the picker off that
  // repo's actual labels rather than free text.
  const selectedRepo = repos.find((r) => r.id === repoId)
  const workflowLabels = selectedRepo?.workflow_id
    ? (workflows.find((w) => w.id === selectedRepo.workflow_id)?.labels ?? [])
    : []

  useEffect(() => {
    if (workflowLabels.length === 0) return
    if (!workflowLabels.some((l) => l.name === targetLabel)) {
      setTargetLabel(workflowLabels.some((l) => l.name === 'not_ready') ? 'not_ready' : workflowLabels[0].name)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [repoId])

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
            {workflowLabels.length > 0 ? (
              <select value={targetLabel} onChange={(e) => setTargetLabel(e.target.value)} className={inputCls}>
                {workflowLabels
                  .slice()
                  .sort((a, b) => a.sort_order - b.sort_order)
                  .map((l) => (
                    <option key={l.id} value={l.name}>{l.name}</option>
                  ))}
              </select>
            ) : (
              <input
                value={targetLabel}
                disabled
                placeholder="select a repo with a workflow first"
                className={inputCls + ' opacity-50 cursor-not-allowed'}
              />
            )}
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
            disabled={saving || !repoId || !cronExpr.trim() || workflowLabels.length === 0}
            className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
          >
            {saving ? 'Adding…' : 'Add Schedule'}
          </button>
        </div>
      </form>
    </div>
  )
}
