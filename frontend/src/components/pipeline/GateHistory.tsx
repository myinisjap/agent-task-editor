import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type AutonomyChange, type AutonomyEscape, type AutonomyHistory } from '../../api/client'
import { DIAL_NAME, type DialMode } from '../../lib/autonomyDial'
import { formatRelativeAge } from '../../lib/format'
import { Button } from '../ui'

const SOURCE: Record<AutonomyEscape['source'], string> = {
  undo: 'undone',
  should_have_asked: 'should have asked',
  rejected_later: 'rejected later',
  reopened: 'reopened',
}

type Entry = { at: string; change?: AutonomyChange; escape?: AutonomyEscape }

/** Step-down counter plus recent mode changes and escapes, newest first. */
export default function GateHistory({
  workflowId,
  label,
  currentMode,
  refreshKey,
  busy,
  onChangeBack,
}: {
  workflowId: string
  label: string
  currentMode: DialMode
  refreshKey: string
  busy?: boolean
  onChangeBack: (mode: DialMode) => void
}) {
  const [hist, setHist] = useState<AutonomyHistory | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [limit, setLimit] = useState(10)

  const load = useCallback(async () => {
    try {
      setHist(await api.autonomy.history(workflowId, label))
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not load history')
    }
  }, [workflowId, label])
  // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch on mount/refresh
  useEffect(() => { void load() }, [load, refreshKey])

  const entries = useMemo<Entry[]>(() => {
    if (!hist) return []
    return [
      ...hist.changes.map((c) => ({ at: c.created_at, change: c })),
      ...hist.escapes.map((e) => ({ at: e.created_at, escape: e })),
    ].sort((a, b) => new Date(b.at).getTime() - new Date(a.at).getTime())
  }, [hist])
  const newestChange = entries.find((e) => e.change)?.change

  return (
    <section aria-label="Escapes and history" className="space-y-2">
      <h3 className="text-sm font-semibold text-fg">Escapes and history</h3>
      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
      {hist && (
        <>
          <p className="text-xs text-fg-muted">
            {hist.step_down.threshold === 0
              ? 'Step-down off'
              : `Escapes counted: ${hist.step_down.escapes_counted} of ${hist.step_down.threshold} in last ${hist.step_down.window_days} days`}
          </p>
          {entries.length === 0 && <p className="text-xs text-fg-muted">No changes or escapes yet.</p>}
          <ul className="space-y-1">
            {entries.slice(0, limit).map((e) => {
              if (e.change) {
                const c = e.change
                const policy = c.actor === 'policy'
                const text = policy
                  ? c.reason || `${hist.step_down.escapes_counted} escapes in ${hist.step_down.window_days} days. Moved from ${DIAL_NAME[c.from_mode]} to ${DIAL_NAME[c.to_mode]}`
                  : `${c.actor} changed ${DIAL_NAME[c.from_mode]} → ${DIAL_NAME[c.to_mode]}`
                return (
                  <li key={c.id} className="text-sm text-fg flex flex-wrap items-center gap-2">
                    <span>{text} · <span className="text-fg-muted">{formatRelativeAge(c.created_at)}</span></span>
                    {policy && c === newestChange && currentMode === c.to_mode && (
                      <Button size="sm" disabled={busy} onClick={() => onChangeBack(c.from_mode)}>Change back</Button>
                    )}
                  </li>
                )
              }
              const x = e.escape as AutonomyEscape
              return (
                <li key={x.id} className="text-sm text-fg">
                  Escape: <Link to={`/tasks/${x.task_id}`} className="text-accent-fg hover:underline">{x.task_title}</Link>
                  {' '}({SOURCE[x.source]}) · <span className="text-fg-muted">{formatRelativeAge(x.created_at)}</span>
                </li>
              )
            })}
          </ul>
          {entries.length > limit && (
            <Button size="sm" variant="ghost" onClick={() => setLimit((n) => n + 10)}>Show more</Button>
          )}
        </>
      )}
    </section>
  )
}
