import { X } from 'lucide-react'
import ModalShell from '../shared/ModalShell'
import { Button, Icon } from '../ui'

export const TASK_TYPES = ['feature', 'bug', 'chore', 'spike']
export const GIT_STATES = ['pushed', 'pr_open', 'pr_merged', 'pr_closed']

const fieldCls =
  'w-full bg-surface-sunken border border-border rounded px-2 py-2 text-sm text-fg focus:outline-none focus:ring-1 focus:ring-accent'

/** Phone-only bottom sheet holding the board's repo/type/git-state/archived filters. */
export default function BoardFilterSheet({
  repos,
  filterRepo,
  filterType,
  filterGitState,
  showArchived,
  setFilterRepo,
  setFilterType,
  setFilterGitState,
  setShowArchived,
  hasFilters,
  onClear,
  filteredCost,
  onClose,
}: {
  repos: { id: string; name: string }[]
  filterRepo: string
  filterType: string
  filterGitState: string
  showArchived: boolean
  setFilterRepo: (v: string) => void
  setFilterType: (v: string) => void
  setFilterGitState: (v: string) => void
  setShowArchived: (v: boolean) => void
  hasFilters: boolean
  onClear: () => void
  filteredCost: number
  onClose: () => void
}) {
  return (
    <ModalShell
      onClose={onClose}
      ariaLabel="Board filters"
      className="self-end w-full max-h-[85vh] overflow-y-auto rounded-t-xl border-t border-border bg-surface p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
    >
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-base font-semibold text-fg">Filters</h2>
        <Button size="sm" variant="ghost" onClick={onClose} aria-label="Close filters">
          <Icon icon={X} size="sm" />
        </Button>
      </div>
      <div className="flex flex-col gap-3">
        <label className="flex flex-col gap-1 text-xs text-fg-subtle">
          Repo
          <select value={filterRepo} onChange={(e) => setFilterRepo(e.target.value)} className={fieldCls}>
            <option value="">All repos</option>
            {repos.map((r) => (
              <option key={r.id} value={r.id}>{r.name}</option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-fg-subtle">
          Type
          <select value={filterType} onChange={(e) => setFilterType(e.target.value)} className={fieldCls}>
            <option value="">All types</option>
            {TASK_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-fg-subtle">
          Git state
          <select value={filterGitState} onChange={(e) => setFilterGitState(e.target.value)} className={fieldCls}>
            <option value="">Any git state</option>
            {GIT_STATES.map((s) => (
              <option key={s} value={s}>{s}</option>
            ))}
          </select>
        </label>
        <Button
          onClick={() => setShowArchived(!showArchived)}
          aria-pressed={showArchived}
          className={`w-full justify-center py-2 ${showArchived ? '!bg-accent/15 !border-accent !text-accent-fg' : ''}`}
          title="Show archived tasks on the board"
        >
          Archived
        </Button>
        {hasFilters && filteredCost > 0 && (
          <p className="text-xs text-fg-subtle">Filtered cost: ${filteredCost.toFixed(2)}</p>
        )}
      </div>
      <div className="flex gap-2 mt-4">
        {hasFilters && (
          <Button
            className="flex-1 justify-center py-2"
            onClick={() => {
              onClear()
              onClose()
            }}
          >
            Clear filters
          </Button>
        )}
        <Button className="flex-1 justify-center py-2" onClick={onClose}>
          Done
        </Button>
      </div>
    </ModalShell>
  )
}
