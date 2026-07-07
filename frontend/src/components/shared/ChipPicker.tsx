// ChipPicker is a generic toggle-chip multi-select, used for workflow labels,
// Claude plugins, and Claude MCP servers.
export default function ChipPicker({ selected, available, onChange, emptyMessage }: {
  selected: string[]
  available: { value: string; label: string }[]
  onChange: (values: string[]) => void
  emptyMessage: string
}) {
  const toggle = (value: string) => {
    if (selected.includes(value)) {
      onChange(selected.filter((v) => v !== value))
    } else {
      onChange([...selected, value])
    }
  }

  if (available.length === 0) {
    return <p className="text-xs text-slate-500">{emptyMessage}</p>
  }

  return (
    <div className="flex flex-wrap gap-2">
      {available.map(({ value, label }) => {
        const active = selected.includes(value)
        return (
          <button
            key={value}
            type="button"
            onClick={() => toggle(value)}
            className={`px-3 py-1 rounded-full text-xs font-medium border transition-colors ${
              active
                ? 'bg-indigo-600 border-indigo-500 text-white'
                : 'bg-slate-800 border-slate-700 text-slate-400 hover:border-slate-500 hover:text-slate-200'
            }`}
          >
            {label}
          </button>
        )
      })}
    </div>
  )
}
