import { useCallback, useEffect, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import { api, type ProviderConfig, type RepoMap, type RepoMapRun } from '../../api/client'

const btnCls = 'px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-200 disabled:opacity-50'
const mdCls =
  'text-sm text-fg [&_h1]:text-base [&_h1]:font-semibold [&_h2]:font-semibold [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-2 [&_code]:font-mono [&_pre]:overflow-x-auto break-words'

// Staleness line: "generated at <sha>, N commits behind".
export function stalenessText(m: RepoMap): string {
  if (!m.commit_sha) return 'No map generated yet.'
  const sha = m.commit_sha.slice(0, 8)
  if (m.commits_behind == null) return `generated at ${sha}, commits behind unknown`
  if (m.commits_behind === 0) return `generated at ${sha}, up to date`
  const n = m.commits_behind
  return `generated at ${sha}, ${n} commit${n === 1 ? '' : 's'} behind${m.layout_changed ? ' (top-level layout changed)' : ''}`
}

function runLine(r: RepoMapRun): string {
  const cost = `$${r.cost_usd.toFixed(2)}`
  const when = new Date(r.started_at).toLocaleString()
  if (r.status === 'failed') return `${when}: failed (${r.error || 'unknown error'}), ${cost}`
  if (r.status === 'running') return `${when}: running`
  return `${when}: ${r.status}, ${r.page_count} pages, ${r.char_count} chars, ${cost} (${r.trigger})`
}

export default function RepoMapPanel({ repoId }: { repoId: string }) {
  const [map, setMap] = useState<RepoMap | null>(null)
  const [providers, setProviders] = useState<ProviderConfig[]>([])
  const [runs, setRuns] = useState<RepoMapRun[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [showRuns, setShowRuns] = useState(false)
  const reqRef = useRef(0)

  const load = useCallback(async () => {
    const req = ++reqRef.current
    try {
      const [m, r] = await Promise.all([api.repos.map(repoId), api.repos.mapRuns(repoId, 5)])
      if (req !== reqRef.current) return
      setMap(m)
      setRuns(r ?? [])
      setError('')
    } catch (e: any) {
      if (req === reqRef.current) setError(e?.message ?? String(e))
    }
  }, [repoId])

  useEffect(() => {
    void load()
    api.providerConfigs.list().then(setProviders).catch(() => {})
  }, [load])

  // Poll while a run is in progress.
  const running = map?.running ?? false
  useEffect(() => {
    if (!running) return
    const t = setInterval(() => void load(), 4000)
    return () => clearInterval(t)
  }, [running, load])

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await fn()
      await load()
    } catch (e: any) {
      setError(e?.message ?? String(e))
    } finally {
      setBusy(false)
    }
  }

  if (!map) return <p className="text-sm text-fg-subtle">{error || 'Loading map…'}</p>

  const cfg = map.config
  const last = map.last_run

  return (
    <div className="flex flex-col gap-3" aria-label="Repo map">
      <div className="flex flex-wrap items-center gap-3">
        <h3 className="text-xs font-semibold text-fg-muted uppercase tracking-wide">Repo map</h3>
        <label className="flex items-center gap-1 text-xs text-fg-muted">
          <input
            type="checkbox"
            checked={cfg.enabled}
            disabled={busy || (!cfg.enabled && !cfg.provider_config_id)}
            onChange={(e) => act(() => api.repos.setMapConfig(repoId, { enabled: e.target.checked }))}
          />
          Enabled
        </label>
        <select
          aria-label="Map provider"
          className="text-xs bg-surface-raised border border-border rounded px-1 py-1"
          value={cfg.provider_config_id ?? ''}
          disabled={busy}
          onChange={(e) => act(() => api.repos.setMapConfig(repoId, { provider_config_id: e.target.value }))}
        >
          <option value="">Choose a provider…</option>
          {providers.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <button
          className={btnCls}
          disabled={busy || running || !cfg.enabled}
          onClick={() => act(() => api.repos.generateMap(repoId))}
        >
          {running ? 'Generating…' : map.commit_sha ? 'Refresh' : 'Generate'}
        </button>
        <button className={btnCls} onClick={() => setShowRuns((s) => !s)} aria-expanded={showRuns}>
          {showRuns ? 'Hide runs' : 'Runs'}
        </button>
      </div>

      {!cfg.enabled && (
        <p className="text-xs text-fg-subtle">
          Off. Pick a provider config and enable it to generate a short wiki of this repo that planning agents receive as orientation.
        </p>
      )}

      <p className="text-xs text-fg-muted">
        {stalenessText(map)}
        {map.stale && map.commit_sha ? ' (due for refresh)' : ''}
        {last && last.status !== 'running' ? ` · last run ${last.status}, $${last.cost_usd.toFixed(2)}` : ''}
      </p>
      {last?.status === 'failed' && last.error && <p className="text-xs text-danger">Last run failed: {last.error}</p>}
      {error && <p className="text-xs text-danger">{error}</p>}

      {showRuns && (
        <ul className="text-xs text-fg-muted list-disc pl-5">
          {runs.length === 0 && <li>No runs yet.</li>}
          {runs.map((r) => (
            <li key={r.id}>{runLine(r)}</li>
          ))}
        </ul>
      )}

      {map.pages.map((p) => (
        <section key={p.slug} className="border border-border rounded p-3">
          <h4 className="text-sm font-semibold mb-1">{p.title}</h4>
          <div className={mdCls}>
            <ReactMarkdown>{p.body}</ReactMarkdown>
          </div>
        </section>
      ))}
    </div>
  )
}
