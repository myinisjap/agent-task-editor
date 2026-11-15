import type { Task } from '../../api/client'

/** Titles of the unmerged predecessors, for the chip text and tooltip. */
export function mergeOrderTitles(task: Pick<Task, 'merge_order_pending' | 'git_state'>): string {
  return (task.merge_order_pending ?? []).map((p) => p.title).join(', ')
}

/**
 * Advisory warning (ADR 0041): this change-set PR is open but a PR it depends
 * on has not merged yet. Manual merges are never blocked. Renders nothing otherwise.
 */
export default function MergeOrderBadge({ task }: { task: Pick<Task, 'merge_order_pending' | 'git_state'> }) {
  if (!task.merge_order_pending?.length || task.git_state !== 'pr_open') return null
  const titles = mergeOrderTitles(task)
  return (
    <span
      data-testid="merge-order-warning"
      className="text-xs px-1.5 py-0.5 rounded bg-amber-900/40 text-amber-200 truncate max-w-[220px]"
      title={`Change set order: merge ${titles} first`}
    >
      Merge after: {titles}
    </span>
  )
}
