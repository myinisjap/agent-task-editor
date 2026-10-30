import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type AskRule } from '../../api/client'
import { isRuleUnused } from '../../lib/autonomyDial'
import { useReposStore } from '../../stores/repos'
import { Badge, Button } from '../ui'
import ConfirmDialog from '../shared/ConfirmDialog'

function conditions(r: AskRule): string {
  if (r.summary) return r.summary
  const parts: string[] = []
  if (r.path_globs.length) parts.push(r.path_globs.join(', '))
  if (r.task_type) parts.push(r.task_type)
  if (r.min_changed_lines) parts.push(`≥ ${r.min_changed_lines} lines`)
  return parts.join(' · ')
}

/** Ask rules that force this gate to ask, with hit counts and delete. */
export default function GateAskRules({ workflowId, label, refreshKey = 0 }: { workflowId: string; label: string; refreshKey?: number }) {
  const [rules, setRules] = useState<AskRule[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState<AskRule | null>(null)
  const repos = useReposStore((s) => s.repos)

  const load = useCallback(async () => {
    try {
      setRules(await api.autonomy.askRules.list({ workflow_id: workflowId, label }))
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not load ask rules')
    }
  }, [workflowId, label])
  // eslint-disable-next-line react-hooks/set-state-in-effect -- fetch on mount
  useEffect(() => { void load() }, [load, refreshKey])

  return (
    <section aria-label="Ask rules" className="space-y-2">
      <h3 className="text-sm font-semibold text-fg">Ask rules</h3>
      {error && <p role="alert" className="text-xs text-danger">{error}</p>}
      {rules && rules.length === 0 && (
        <p className="text-xs text-fg-muted">
          No ask rules for this gate. Add one from a task with &lsquo;Should have asked&rsquo;.
        </p>
      )}
      {rules && rules.length > 0 && (
        <ul className="space-y-2">
          {rules.map((r) => {
            const repo = r.repo_id ? repos.find((x) => x.id === r.repo_id) : undefined
            const text = conditions(r)
            return (
              <li key={r.id} className="rounded border border-border p-2 text-sm flex flex-wrap items-start gap-2">
                <div className="flex-1 min-w-0 space-y-1">
                  <div className="text-fg break-words">
                    {r.path_globs.length > 0 && (
                      <span className="inline-flex flex-wrap gap-1 mr-1">
                        {r.path_globs.map((g) => (
                          <code key={g} className="rounded bg-surface-sunken px-1 text-xs">{g}</code>
                        ))}
                      </span>
                    )}
                    <span>{text}</span>
                  </div>
                  <div className="flex flex-wrap items-center gap-1.5 text-xs text-fg-muted">
                    {r.label === null && <Badge size="xs" tone="accent">All gates</Badge>}
                    {r.repo_id && <Badge size="xs">{`One repo${repo ? `: ${repo.name}` : ''}`}</Badge>}
                    <span>Added by {r.created_by}</span>
                    {r.source_task_id && (
                      <Link to={`/tasks/${r.source_task_id}`} className="text-accent-fg hover:underline">from task</Link>
                    )}
                    <span>{r.hit_count} {r.hit_count === 1 ? 'hit' : 'hits'}</span>
                    {isRuleUnused(r) && <Badge size="xs">Unused</Badge>}
                  </div>
                </div>
                <Button size="sm" variant="ghost" aria-label={`Delete rule ${text}`} onClick={() => setPending(r)}>
                  Delete
                </Button>
              </li>
            )
          })}
        </ul>
      )}
      {pending && (
        <ConfirmDialog
          destructive
          title="Delete ask rule?"
          confirmLabel="Delete rule"
          onCancel={() => setPending(null)}
          onConfirm={async () => {
            await api.autonomy.askRules.delete(pending.id)
            setPending(null)
            await load()
          }}
        >
          <p>This gate will no longer be forced to ask for: {conditions(pending)}.</p>
        </ConfirmDialog>
      )}
    </section>
  )
}
