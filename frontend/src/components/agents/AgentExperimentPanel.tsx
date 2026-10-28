import { useCallback, useEffect, useState } from 'react'
import {
  api,
  type AgentConfig,
  type AgentExperiment,
  type AgentExperimentArmStats,
  type ProviderConfig,
} from '../../api/client'
import { diffLines } from '../../lib/lineDiff'
import { otherChanges } from './AgentHistoryPanel'

const LINE_BG = {
  add: 'bg-emerald-950 text-emerald-300',
  remove: 'bg-red-950 text-red-300',
  context: 'text-slate-400',
} as const
const LINE_PREFIX = { add: '+', remove: '-', context: ' ' } as const

type Snap = Record<string, unknown>

const EFFORTS = ['', 'low', 'medium', 'high', 'xhigh', 'max']
const PERMISSIONS = ['', 'default', 'auto', 'acceptEdits', 'bypassPermissions']

const pct = (n: number) => `${n.toFixed(0)}%`
const usd = (n: number) => `$${n.toFixed(2)}`
const secs = (n: number) => `${n.toFixed(0)}s`

const ROWS: { label: string; get: (s: AgentExperimentArmStats) => string }[] = [
  { label: 'Tasks', get: (s) => String(s.tasks) },
  { label: 'Done', get: (s) => String(s.tasks_done) },
  { label: 'First-pass success', get: (s) => pct(s.first_pass_success_rate_percent) },
  { label: 'Rework rate', get: (s) => pct(s.rework_rate_percent) },
  { label: 'Human-touch rate', get: (s) => pct(s.human_touch_rate_percent) },
  { label: 'Cost to done', get: (s) => usd(s.avg_cost_to_done_usd) },
  { label: 'Avg duration', get: (s) => secs(s.avg_duration_secs) },
]

function ResultsTable({ exp }: { exp: AgentExperiment }) {
  const res = exp.results
  if (!res) return <p className="text-xs text-slate-500">No results yet.</p>
  return (
    <table className="text-xs w-full" aria-label="Experiment results">
      <thead>
        <tr className="text-slate-400 text-left">
          <th className="font-normal py-1">Metric</th>
          <th className="font-normal">Control</th>
          <th className="font-normal">Candidate</th>
        </tr>
      </thead>
      <tbody>
        {ROWS.map((r) => (
          <tr key={r.label} className="border-t border-slate-800 text-slate-200">
            <td className="py-1 text-slate-400">{r.label}</td>
            <td>{r.get(res.control)}</td>
            <td>{r.get(res.candidate)}</td>
          </tr>
        ))}
        <tr className="border-t border-slate-800">
          <td />
          {[res.control, res.candidate].map((s, i) => (
            <td key={i} className="py-1">
              {s.not_enough_data && (
                <span className="px-1.5 rounded bg-amber-900 text-amber-300">
                  Not enough data (n &lt; {res.min_tasks_per_arm})
                </span>
              )}
            </td>
          ))}
        </tr>
      </tbody>
    </table>
  )
}

export default function AgentExperimentPanel({
  agentId,
  config,
  providerConfigs = [],
  onPromoted,
  reloadKey = 0,
}: {
  agentId: string
  config: AgentConfig
  providerConfigs?: ProviderConfig[]
  onPromoted: (config: AgentConfig) => void
  reloadKey?: number
}) {
  const [experiments, setExperiments] = useState<AgentExperiment[]>([])
  const [detail, setDetail] = useState<AgentExperiment | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [confirming, setConfirming] = useState<'stop' | 'promote' | null>(null)
  const [promoteNote, setPromoteNote] = useState('')
  const [showPast, setShowPast] = useState(false)

  // Start form state.
  const [starting, setStarting] = useState(false)
  const [base, setBase] = useState<Snap | null>(null)
  const [draft, setDraft] = useState<Snap>({})
  const [share, setShare] = useState(50)
  const [startNote, setStartNote] = useState('')

  const running = experiments.find((e) => e.status === 'running')

  const load = useCallback(async () => {
    try {
      const list = await api.agents.experiments(agentId)
      setExperiments(list)
      setError(null)
      const run = list.find((e) => e.status === 'running')
      setDetail(run ? await api.agents.experiment(agentId, run.id) : null)
    } catch (e: any) {
      setError(e.message)
    }
  }, [agentId])

  useEffect(() => {
    void load()
  }, [load, reloadKey, config.current_version])

  async function openStart() {
    setActionError(null)
    try {
      const versions = await api.agents.versions(agentId)
      const snap = (versions[0]?.snapshot ?? {}) as unknown as Snap
      setBase(snap)
      setDraft(snap)
      setStarting(true)
    } catch (e: any) {
      setActionError(e.message)
    }
  }

  async function submitStart() {
    setBusy(true)
    setActionError(null)
    try {
      await api.agents.startExperiment(agentId, {
        candidate: draft as any,
        share_percent: share,
        note: startNote || undefined,
      })
      setStarting(false)
      setStartNote('')
      await load()
    } catch (e: any) {
      setActionError(e.message)
    } finally {
      setBusy(false)
    }
  }

  async function doStop(exp: AgentExperiment) {
    setBusy(true)
    setActionError(null)
    try {
      await api.agents.stopExperiment(agentId, exp.id)
      setConfirming(null)
      await load()
    } catch (e: any) {
      setActionError(e.message)
    } finally {
      setBusy(false)
    }
  }

  async function doPromote(exp: AgentExperiment) {
    setBusy(true)
    setActionError(null)
    try {
      const res = await api.agents.promoteExperiment(agentId, exp.id, promoteNote.trim() || undefined)
      setConfirming(null)
      setPromoteNote('')
      onPromoted(res.config)
      await load()
    } catch (e: any) {
      setActionError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const setField = (k: string, v: unknown) => setDraft((d) => ({ ...d, [k]: v }))
  const numField = (k: string, label: string, step?: string) => (
    <label className="flex flex-col gap-0.5">
      <span className="text-slate-400">{label}</span>
      <input
        type="number"
        step={step}
        value={String(draft[k] ?? 0)}
        onChange={(e) => setField(k, Number(e.target.value))}
        className="input w-32"
        aria-label={label}
      />
    </label>
  )

  const past = experiments.filter((e) => e.status !== 'running')
  const exp = detail ?? running

  return (
    <section className="mt-8 border-t border-slate-800 pt-4" data-testid="agent-experiments">
      <h3 className="text-sm font-semibold text-slate-200 mb-2">Experiment</h3>
      {error && <p className="text-xs text-red-400">{error}</p>}

      {running && exp && (
        <div className="rounded border border-slate-800 bg-slate-900 p-3 text-xs space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <span className="px-1.5 rounded bg-indigo-900 text-indigo-300">running</span>
            <span className="text-slate-300">{exp.share_percent}% of new tasks use the candidate</span>
            {exp.note && <span className="text-slate-300 italic">{exp.note}</span>}
          </div>
          <p className="text-slate-500">
            Control is this agent's current config at dispatch time; edits made during the experiment change control.
          </p>
          <ResultsTable exp={exp} />
          {confirming === null && (
            <div className="flex gap-2">
              <button onClick={() => setConfirming('stop')} className="px-2 py-0.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-200">
                Stop
              </button>
              <button onClick={() => setConfirming('promote')} className="px-2 py-0.5 rounded bg-indigo-600 hover:bg-indigo-500 text-white">
                Promote
              </button>
            </div>
          )}
          {confirming === 'stop' && (
            <div className="flex items-center gap-2">
              <span className="text-amber-300">Stop the experiment? All later runs use the current config.</span>
              <button onClick={() => doStop(running)} disabled={busy} className="px-2 py-0.5 rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50">
                Confirm stop
              </button>
              <button onClick={() => setConfirming(null)} className="px-2 py-0.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-200">
                Cancel
              </button>
            </div>
          )}
          {confirming === 'promote' && (
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-amber-300">Promote the candidate as the agent's new version?</span>
              <input
                value={promoteNote}
                onChange={(e) => setPromoteNote(e.target.value)}
                placeholder="Note (optional)"
                aria-label="Promote note"
                className="input w-48"
              />
              <button onClick={() => doPromote(running)} disabled={busy} className="px-2 py-0.5 rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50">
                Confirm promote
              </button>
              <button onClick={() => setConfirming(null)} className="px-2 py-0.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-200">
                Cancel
              </button>
            </div>
          )}
        </div>
      )}

      {!running && !starting && (
        <button onClick={openStart} className="px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-200">
          Start experiment
        </button>
      )}

      {!running && starting && base && (
        <div className="rounded border border-slate-800 bg-slate-900 p-3 text-xs space-y-3">
          <p className="text-slate-400">
            Edit a copy of the current config. Only these fields are editable here; everything else (fallbacks,
            ladder, plugins, command lists, …) is copied unchanged.
          </p>
          <label className="flex flex-col gap-0.5">
            <span className="text-slate-400">Candidate system prompt</span>
            <textarea
              value={String(draft.system_prompt ?? '')}
              onChange={(e) => setField('system_prompt', e.target.value)}
              rows={6}
              className="input font-mono"
              aria-label="Candidate system prompt"
            />
          </label>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-0.5">
              <span className="text-slate-400">Provider config</span>
              <select
                value={String(draft.provider_config_id ?? '')}
                onChange={(e) => setField('provider_config_id', e.target.value)}
                className="input"
                aria-label="Candidate provider config"
              >
                {providerConfigs.map((pc) => (
                  <option key={pc.id} value={pc.id}>
                    {pc.name}
                  </option>
                ))}
                {!providerConfigs.some((pc) => pc.id === draft.provider_config_id) && (
                  <option value={String(draft.provider_config_id ?? '')}>{String(draft.provider_config_id ?? '')}</option>
                )}
              </select>
            </label>
            <label className="flex flex-col gap-0.5">
              <span className="text-slate-400">Effort</span>
              <select value={String(draft.effort ?? '')} onChange={(e) => setField('effort', e.target.value)} className="input" aria-label="Candidate effort">
                {EFFORTS.map((x) => (
                  <option key={x} value={x}>
                    {x || 'Default'}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-0.5">
              <span className="text-slate-400">Permission mode</span>
              <select
                value={String(draft.permission_mode ?? '')}
                onChange={(e) => setField('permission_mode', e.target.value)}
                className="input"
                aria-label="Candidate permission mode"
              >
                {PERMISSIONS.map((x) => (
                  <option key={x} value={x}>
                    {x || 'Default'}
                  </option>
                ))}
              </select>
            </label>
            {numField('max_turns', 'Max turns')}
            {numField('timeout_secs', 'Timeout (s)')}
            {numField('max_cost_usd', 'Max cost (USD)', '0.01')}
          </div>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-0.5">
              <span className="text-slate-400">Share of new tasks (%)</span>
              <input
                type="number"
                min={1}
                max={90}
                value={share}
                onChange={(e) => setShare(Number(e.target.value))}
                className="input w-24"
                aria-label="Share percent"
              />
            </label>
            <label className="flex flex-col gap-0.5 flex-1 min-w-[12rem]">
              <span className="text-slate-400">Note</span>
              <input value={startNote} onChange={(e) => setStartNote(e.target.value)} className="input" aria-label="Experiment note" />
            </label>
          </div>
          <div>
            <p className="text-slate-400 mb-1">Changes vs current</p>
            {otherChanges(base, draft).length > 0 && <p className="text-slate-300 mb-1">{otherChanges(base, draft).join(', ')}</p>}
            <pre className="font-mono overflow-x-auto" aria-label="Candidate prompt diff">
              {diffLines(String(base.system_prompt ?? ''), String(draft.system_prompt ?? '')).map((l, i) => (
                <div key={i} className={LINE_BG[l.type]}>
                  {LINE_PREFIX[l.type]} {l.text}
                </div>
              ))}
            </pre>
          </div>
          <div className="flex gap-2">
            <button onClick={submitStart} disabled={busy} className="px-2 py-1 rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50">
              Start
            </button>
            <button onClick={() => setStarting(false)} className="px-2 py-1 rounded bg-slate-700 hover:bg-slate-600 text-slate-200">
              Cancel
            </button>
          </div>
        </div>
      )}

      {actionError && <p className="mt-1 text-xs text-red-400">{actionError}</p>}

      {past.length > 0 && (
        <div className="mt-3 text-xs">
          <button onClick={() => setShowPast((v) => !v)} className="text-indigo-300 hover:underline" aria-expanded={showPast}>
            Past experiments ({past.length})
          </button>
          {showPast && (
            <ul className="mt-1 space-y-1">
              {past.map((e) => (
                <li key={e.id} className="rounded border border-slate-800 bg-slate-900 p-2 flex flex-wrap gap-2 text-slate-300">
                  <span className="font-semibold">{e.status}</span>
                  <span>{e.share_percent}%</span>
                  <span className="text-slate-400">{new Date(e.started_at).toLocaleString()}</span>
                  {e.promoted_version != null && <span>→ v{e.promoted_version}</span>}
                  {e.note && <span className="italic">{e.note}</span>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  )
}
