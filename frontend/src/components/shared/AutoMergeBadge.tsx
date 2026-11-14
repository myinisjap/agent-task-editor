import type { Task } from '../../api/client'

type AutoMerge = NonNullable<Task['auto_merge']>

/** Short text for the task's auto-merge state, with the blocking reason when not yet eligible. */
export function autoMergeText(am: AutoMerge): string {
  if (am.state === 'merging') return 'Auto-merging…'
  return am.message ? `Auto-merge when green · ${am.message}` : 'Auto-merge when green'
}

/** "Auto-merge when green" chip; renders nothing when the policy does not apply. */
export default function AutoMergeBadge({ autoMerge }: { autoMerge?: Task['auto_merge'] }) {
  if (!autoMerge) return null
  const refused = autoMerge.reason === 'merge_refused'
  return (
    <span
      className={`text-xs px-1.5 py-0.5 rounded ${refused ? 'bg-red-900/40 text-red-300' : 'bg-teal-900/50 text-teal-200'}`}
      title={autoMerge.message || undefined}
    >
      {autoMergeText(autoMerge)}
    </span>
  )
}
