import { Link } from 'react-router-dom'
import { GitFork } from 'lucide-react'
import Icon from '../ui/Icon'
import Badge from '../ui/Badge'
import { labelDisplayNameByName } from '../../lib/labelDisplay'
import { useReposStore } from '../../stores/repos'
import type { ChangeSet, ChangeSetChild, WorkflowLabel } from '../../api/client'

const VERIFY_TONE: Record<string, 'success' | 'danger' | 'neutral'> = {
  pass: 'success',
  fail: 'danger',
  timeout: 'danger',
  error: 'danger',
  not_run: 'neutral',
}

const PR_STATE: Record<string, string> = { pr_open: 'open', pr_merged: 'merged', pr_closed: 'closed' }

function useRepoName() {
  const byId = useReposStore((s) => s.byId)
  // Subscribing to repos re-renders when the list loads.
  useReposStore((s) => s.repos)
  return (id: string) => byId(id)?.name || id.slice(0, 8)
}

function autoMergeBadge(am: NonNullable<ChangeSetChild['auto_merge']>) {
  const text =
    am.reason === 'change_set_order'
      ? 'waiting for earlier PRs'
      : am.state === 'merging'
        ? 'auto-merging…'
        : 'auto-merge waiting'
  const tone = am.reason === 'change_set_sibling_failed' || am.reason === 'merge_refused' ? 'danger' : 'warning'
  return (
    <Badge size="xs" tone={tone} title={am.message || undefined} data-testid="change-set-auto-merge">
      {text}
    </Badge>
  )
}

function ChildRow({ c, pos, labels }: { c: ChangeSetChild; pos: number | null; labels: WorkflowLabel[] }) {
  return (
    <li data-testid="change-set-row" className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs bg-surface-sunken rounded px-2 py-1">
      <Link to={`/tasks/${c.task_id}`} className="text-fg hover:text-accent-fg min-w-0 truncate" title={c.title}>
        {c.title}
      </Link>
      <span className="text-fg-muted">{labelDisplayNameByName(c.label, labels)}</span>
      {c.branch && <code className="text-[10px] px-1 rounded bg-surface-raised text-fg-muted break-all">{c.branch}</code>}
      {c.pr_url ? (
        <a href={c.pr_url} target="_blank" rel="noreferrer" className="text-accent-fg hover:underline" data-testid="change-set-pr">
          PR ({PR_STATE[c.git_state] ?? c.git_state})
        </a>
      ) : (
        <span className="text-fg-muted">no PR yet</span>
      )}
      <Badge size="xs" tone={VERIFY_TONE[c.verification] ?? 'neutral'} data-testid="change-set-verify">
        verify: {c.verification.replace('_', ' ')}
      </Badge>
      <span className="text-fg-muted" data-testid="change-set-diffstat">
        {c.changed ? `${c.changed.files} files +${c.changed.additions} −${c.changed.deletions}` : '—'}
      </span>
      {c.auto_merge && autoMergeBadge(c.auto_merge)}
      {pos != null && <span className="text-fg-muted" data-testid="change-set-position">#{pos}</span>}
      <Link to={`/tasks/${c.task_id}?tab=diff`} className="text-accent-fg hover:underline ml-auto" data-testid="change-set-diff-link">
        Diff
      </Link>
    </li>
  )
}

/** Read-only "Change set" view of a multi-repo parent (ADR 0041): children grouped by repo. */
export function ChangeSetPanel({ changeSet, repoCount, labels }: { changeSet: ChangeSet; repoCount: number; labels: WorkflowLabel[] }) {
  const repoName = useRepoName()
  const parent = changeSet.parent
  const kids = changeSet.children
  if (!parent) return null
  const order = changeSet.merge_order
  const titleOf = (id: string) => kids.find((k) => k.task_id === id)?.title ?? id.slice(0, 8)

  // Group by repo: primary first, then by earliest merge position, then created order.
  const groups = new Map<string, ChangeSetChild[]>()
  groups.set(parent.repo_id, [])
  for (const k of kids) groups.set(k.repo_id, [...(groups.get(k.repo_id) ?? []), k])
  const rank = (items: ChangeSetChild[]) => Math.min(...items.map((i) => i.merge_position ?? Infinity))
  const repoIds = [...groups.keys()]
  const rest = repoIds.filter((r) => r !== parent.repo_id)
  rest.sort((a, b) => rank(groups.get(a)!) - rank(groups.get(b)!) || repoIds.indexOf(a) - repoIds.indexOf(b))
  const ordered = [parent.repo_id, ...rest].filter((r) => (groups.get(r)?.length ?? 0) > 0)

  return (
    <div data-testid="change-set" className="mb-3">
      <p className="text-xs text-fg-muted mb-1 flex flex-wrap items-center gap-x-2">
        <Icon icon={GitFork} size="sm" />
        <span className="font-medium text-fg">Change set · {repoCount} repos</span>
        <span>rolled-up cost ${parent.rolled_up_cost_usd.toFixed(2)}</span>
      </p>
      {order.length > 0 && (
        <p className="text-xs text-fg-muted mb-2" data-testid="change-set-order">
          Merge order: {order.map(titleOf).join(' → ')}
        </p>
      )}
      <div className="flex flex-col gap-2">
        {ordered.map((rid) => (
          <section key={rid} data-testid="change-set-group" aria-label={repoName(rid)}>
            <p className="text-[11px] uppercase tracking-wide text-fg-muted mb-1">
              {repoName(rid)}
              {rid === parent.repo_id && <span className="ml-1 normal-case">(primary)</span>}
            </p>
            <ul className="flex flex-col gap-1">
              {groups.get(rid)!.map((c) => (
                <ChildRow key={c.task_id} c={c} pos={c.merge_position ?? null} labels={labels} />
              ))}
            </ul>
          </section>
        ))}
      </div>
    </div>
  )
}

/** Compact "Part of change set" line on a child of a multi-repo parent. */
export function ChangeSetLine({ changeSet, taskId }: { changeSet: ChangeSet; taskId: string }) {
  const repoName = useRepoName()
  if (!changeSet.multi_repo || !changeSet.parent) return null
  const siblings = changeSet.children.filter((c) => c.task_id !== taskId)
  return (
    <p data-testid="change-set-line" className="mb-3 text-xs text-fg-muted flex flex-wrap items-center gap-x-1">
      <span>Part of change set:</span>
      <Link to={`/tasks/${changeSet.parent.task_id}`} className="text-accent-fg hover:underline">
        {changeSet.parent.title}
      </Link>
      {siblings.length > 0 && <span>· siblings:</span>}
      {siblings.map((s, i) => (
        <span key={s.task_id}>
          <Link to={`/tasks/${s.task_id}`} className="text-accent-fg hover:underline">
            {s.title}
          </Link>{' '}
          ({repoName(s.repo_id)}){i < siblings.length - 1 ? ',' : ''}
        </span>
      ))}
    </p>
  )
}
