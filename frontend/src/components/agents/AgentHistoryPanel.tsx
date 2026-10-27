import { useEffect, useState } from 'react'
import { api, type AgentConfig, type AgentConfigVersion } from '../../api/client'
import { diffLines } from '../../lib/lineDiff'

const LINE_BG = {
  add: 'bg-emerald-950 text-emerald-300',
  remove: 'bg-red-950 text-red-300',
  context: 'text-slate-400',
} as const
const LINE_PREFIX = { add: '+', remove: '-', context: ' ' } as const

function fmt(v: unknown): string {
  if (typeof v === 'string') return v === '' ? '(empty)' : v.length > 40 ? `${v.slice(0, 40)}…` : v
  return JSON.stringify(v)
}

/** One-line summary of non-prompt fields that differ between two snapshots. */
function otherChanges(prev: Record<string, unknown> | undefined, cur: Record<string, unknown>): string[] {
  if (!prev) return []
  const out: string[] = []
  for (const key of Object.keys(cur)) {
    if (key === 'system_prompt') continue
    if (JSON.stringify(prev[key]) !== JSON.stringify(cur[key])) {
      out.push(`${key}: ${fmt(prev[key])} → ${fmt(cur[key])}`)
    }
  }
  return out
}

export default function AgentHistoryPanel({
  agentId,
  onRestored,
  reloadKey = 0,
}: {
  agentId: string
  onRestored: (config: AgentConfig) => void
  reloadKey?: number
}) {
  const [versions, setVersions] = useState<AgentConfigVersion[]>([])
  const [error, setError] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<number | null>(null)
  const [confirming, setConfirming] = useState<number | null>(null)
  const [restoring, setRestoring] = useState(false)
  const [restoreError, setRestoreError] = useState<string | null>(null)
  const [refresh, setRefresh] = useState(0)

  useEffect(() => {
    let cancelled = false
    api.agents
      .versions(agentId)
      .then((v) => {
        if (!cancelled) {
          setVersions(v)
          setError(null)
        }
      })
      .catch((e: Error) => {
        if (!cancelled) setError(e.message)
      })
    return () => {
      cancelled = true
    }
  }, [agentId, reloadKey, refresh])

  async function doRestore(version: number) {
    setRestoring(true)
    setRestoreError(null)
    try {
      const config = await api.agents.restoreVersion(agentId, version)
      setConfirming(null)
      onRestored(config)
      setRefresh((n) => n + 1)
    } catch (e: any) {
      setRestoreError(e.message)
    } finally {
      setRestoring(false)
    }
  }

  const latest = versions[0]?.version

  return (
    <section className="mt-8 border-t border-slate-800 pt-4" data-testid="agent-history">
      <h3 className="text-sm font-semibold text-slate-200 mb-2">History</h3>
      {error && <p className="text-xs text-red-400">{error}</p>}
      {!error && versions.length === 0 && <p className="text-xs text-slate-500">No versions yet.</p>}
      <ul className="space-y-2">
        {versions.map((v, idx) => {
          const prev = versions[idx + 1]
          const snap = v.snapshot as unknown as Record<string, unknown>
          const prevSnap = prev?.snapshot as unknown as Record<string, unknown> | undefined
          const isOpen = expanded === v.version
          return (
            <li key={v.id} className="rounded border border-slate-800 bg-slate-900 p-2 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <button
                  onClick={() => setExpanded(isOpen ? null : v.version)}
                  className="font-mono font-semibold text-indigo-300 hover:underline"
                  aria-expanded={isOpen}
                >
                  v{v.version}
                </button>
                {v.version === latest && (
                  <span className="px-1.5 rounded bg-emerald-900 text-emerald-300">current</span>
                )}
                <span className="text-slate-400">{new Date(v.created_at).toLocaleString()}</span>
                <span className="text-slate-500">{v.actor_id || '—'}</span>
                {v.note && <span className="text-slate-300 italic">{v.note}</span>}
                {v.version !== latest && confirming !== v.version && (
                  <button
                    onClick={() => {
                      setConfirming(v.version)
                      setRestoreError(null)
                    }}
                    className="ml-auto px-2 py-0.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-200"
                  >
                    Restore
                  </button>
                )}
                {confirming === v.version && (
                  <span className="ml-auto flex items-center gap-2">
                    <span className="text-amber-300">Restore v{v.version} as a new version?</span>
                    <button
                      onClick={() => doRestore(v.version)}
                      disabled={restoring}
                      className="px-2 py-0.5 rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
                    >
                      Confirm
                    </button>
                    <button
                      onClick={() => setConfirming(null)}
                      className="px-2 py-0.5 rounded bg-slate-700 hover:bg-slate-600 text-slate-200"
                    >
                      Cancel
                    </button>
                  </span>
                )}
              </div>
              {confirming === v.version && restoreError && (
                <p className="mt-1 text-red-400">{restoreError}</p>
              )}
              {isOpen && (
                <div className="mt-2 space-y-2">
                  {otherChanges(prevSnap, snap).length > 0 && (
                    <p className="text-slate-400">{otherChanges(prevSnap, snap).join(', ')}</p>
                  )}
                  <pre className="font-mono overflow-x-auto" aria-label={`Prompt diff v${v.version}`}>
                    {diffLines(prev ? prev.snapshot.system_prompt ?? '' : '', v.snapshot.system_prompt ?? '').map((l, i) => (
                      <div key={i} className={LINE_BG[l.type]}>
                        {LINE_PREFIX[l.type]} {l.text}
                      </div>
                    ))}
                  </pre>
                </div>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}
