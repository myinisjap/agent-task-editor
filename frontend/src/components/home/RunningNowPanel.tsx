import { Link } from 'react-router-dom'
import type { Dashboard, Task, Workflow } from '../../api/client'
import { useNow } from '../../lib/useNow'
import { formatElapsed } from '../../lib/homeInbox'
import { Card } from '../ui'
import type { RunStep } from '../../lib/useRunSteps'

const MAX_FREE_ROWS = 12

type Props = {
  agents: Dashboard['active_agents']
  /** Worker limit when known; free-slot rows are shown only then. */
  maxWorkers?: number
  repoConcurrency: Dashboard['repo_concurrency']
  workflow?: Workflow
  tasks?: Pick<Task, 'id' | 'label'>[]
  /** Latest tool call per run_id. */
  steps?: Record<string, RunStep>
}

export default function RunningNowPanel({ agents, maxWorkers, repoConcurrency, workflow, tasks, steps }: Props) {
  const now = useNow(1000)
  const colorFor = (taskId: string): string | undefined => {
    const label = tasks?.find((t) => t.id === taskId)?.label
    return workflow?.labels.find((l) => l.name === label)?.color
  }
  const free = maxWorkers !== undefined ? Math.max(0, maxWorkers - agents.length) : 0
  const freeShown = Math.min(free, MAX_FREE_ROWS)
  return (
    <Card as="section" padding="md" aria-label="Running now" data-testid="running-now-panel" className="flex flex-col gap-3">
      <h2 className="text-xs font-medium text-fg-subtle uppercase tracking-wide">Running now</h2>
      <ul className="flex flex-col gap-1.5 min-w-0">
        {agents.map((a) => {
          const color = colorFor(a.task_id)
          const step = steps?.[a.run_id]
          return (
            <li key={a.run_id} data-testid="running-row" className="min-w-0 rounded-md border border-border px-2 py-1.5">
              <div className="flex items-center gap-2 min-w-0">
              <span aria-hidden className={`inline-block w-2.5 h-2.5 rounded-full shrink-0 ${color ? '' : 'bg-fg-subtle'}`} style={color ? { backgroundColor: color } : undefined} />
              <Link to={`/tasks/${a.task_id}`} className="flex-1 min-w-0 truncate text-sm text-fg hover:underline">{a.task_title}</Link>
              <span className="shrink-0 max-w-[30%] truncate text-xs text-fg-muted">{a.agent_name}</span>
              <span className="shrink-0 font-mono text-xs text-fg">{formatElapsed(now - new Date(a.started_at).getTime())}</span>
              </div>
              {step && (
                <div data-testid="run-step" title={`${step.tool}  ${step.summary}`} className="mt-1 min-w-0 truncate font-mono text-xs text-fg-muted">
                  <span className="text-fg">{step.tool}</span><span className="whitespace-pre">{"  "}</span>{step.summary}
                </div>
              )}
            </li>
          )
        })}
        {Array.from({ length: freeShown }, (_, i) => (
          <li key={`free-${i}`} data-testid="free-slot" className="rounded-md border border-dashed border-border px-2 py-1.5 text-sm text-fg-subtle">
            Free slot
          </li>
        ))}
        {free > freeShown && <li className="text-xs text-fg-subtle">+{free - freeShown} free</li>}
        {agents.length === 0 && maxWorkers === undefined && <li className="text-sm text-fg-subtle">No agents running.</li>}
      </ul>
      {repoConcurrency.length > 1 && (
        <div className="flex flex-col gap-2 pt-2 border-t border-border" data-testid="repo-concurrency">
          {repoConcurrency.map((rc) => {
            const pct = rc.limit > 0 ? Math.min(100, (rc.in_use / rc.limit) * 100) : 0
            const saturated = rc.in_use >= rc.limit
            return (
              <div key={rc.repo_id} className="min-w-0">
                <div className="flex items-center justify-between gap-2 mb-1">
                  <span className="text-xs text-fg truncate">{rc.repo_name}</span>
                  <span className={`text-xs shrink-0 ${saturated ? 'text-warning' : 'text-fg-muted'}`}>
                    {rc.in_use} / {rc.limit} workers
                  </span>
                </div>
                <div className="h-1.5 rounded-full bg-surface-sunken overflow-hidden">
                  <div className={`h-full rounded-full ${saturated ? 'bg-warning' : 'bg-accent'}`} style={{ width: `${pct}%` }} />
                </div>
              </div>
            )
          })}
        </div>
      )}
    </Card>
  )
}
