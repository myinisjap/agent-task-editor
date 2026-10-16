export type ColumnChip = {
  key: string
  label: string
  count: number
  needsHuman: boolean
}

/** Horizontally scrollable row of column chips (phone board). Tap to jump. */
export default function ColumnChips({
  chips,
  currentIndex,
  onSelect,
}: {
  chips: ColumnChip[]
  currentIndex: number
  onSelect: (i: number) => void
}) {
  return (
    <div
      data-testid="column-chips"
      className="flex gap-2 overflow-x-auto mb-2 pb-1"
      style={{ touchAction: 'pan-x' }}
    >
      {chips.map((c, i) => (
        <button
          key={c.key}
          type="button"
          data-needs-human={c.needsHuman ? 'true' : undefined}
          aria-current={i === currentIndex ? 'true' : undefined}
          onClick={() => onSelect(i)}
          className={`shrink-0 flex items-center gap-1.5 px-3 py-1 rounded-full text-xs border whitespace-nowrap ${
            i === currentIndex
              ? 'bg-indigo-600 border-indigo-400 text-white'
              : c.needsHuman
                ? 'bg-amber-950 border-amber-500 text-amber-200'
                : 'bg-slate-800 border-slate-700 text-slate-300'
          }`}
        >
          {c.needsHuman && <span aria-label="needs you" className="w-1.5 h-1.5 rounded-full bg-amber-400" />}
          <span>{c.label}</span>
          <span className="opacity-70">{c.count}</span>
        </button>
      ))}
    </div>
  )
}
