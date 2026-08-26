import type { RuntimeLanguage } from '../../api/client'

// The full allowlist the backend accepts for RuntimeLanguage.id — kept in
// sync with the generated enum (components["schemas"]["RuntimeLanguage"]) so
// a typo here is a compile error, not a silent 400 at save time.
const LANGUAGE_IDS: RuntimeLanguage['id'][] = ['go', 'node', 'python', 'rust', 'java', 'ruby']

const LANGUAGE_LABELS: Record<RuntimeLanguage['id'], string> = {
  go: 'Go',
  node: 'Node',
  python: 'Python',
  rust: 'Rust',
  java: 'Java',
  ruby: 'Ruby',
}

const inputCls =
  'bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500'

/**
 * Editor for a repo's `runtime_languages` list: a fixed six-id allowlist
 * dropdown paired with a free-text version per row. There is deliberately no
 * raw JSON escape hatch here — see docs/runtime-containers.md and AGENTS.md
 * for why (an earlier devcontainer.json editor let a user string reach
 * `runArgs`/`mounts`, which was a privilege-escalation path). Keep it that
 * way: state stays `[{id, version}]`, nothing here should grow a blob to
 * round-trip.
 */
export default function RuntimeLanguagesEditor({ value, onChange }: {
  value: RuntimeLanguage[]
  onChange: (next: RuntimeLanguage[]) => void
}) {
  function update(index: number, patch: Partial<RuntimeLanguage>) {
    onChange(value.map((lang, i) => (i === index ? { ...lang, ...patch } : lang)))
  }

  function remove(index: number) {
    onChange(value.filter((_, i) => i !== index))
  }

  function add() {
    // ponytail: skip a dedup toggle in the UI; just default new rows to the
    // first id not already selected, so a duplicate row never appears from
    // clicking "+ add language" alone. The backend still accepts (and a
    // user can still hand-pick) duplicates via the dropdown — it takes the
    // list as-is — this is purely a nicer default.
    const used = new Set(value.map((l) => l.id))
    const nextId = LANGUAGE_IDS.find((id) => !used.has(id)) ?? LANGUAGE_IDS[0]
    onChange([...value, { id: nextId, version: '' }])
  }

  return (
    <div className="flex flex-col gap-2">
      {value.map((lang, i) => (
        <div key={i} className="flex items-center gap-2">
          <select
            value={lang.id}
            onChange={(e) => update(i, { id: e.target.value as RuntimeLanguage['id'] })}
            className={inputCls}
          >
            {LANGUAGE_IDS.map((id) => (
              <option key={id} value={id}>{LANGUAGE_LABELS[id]}</option>
            ))}
          </select>
          <input
            value={lang.version}
            onChange={(e) => update(i, { version: e.target.value })}
            placeholder="version"
            className={`${inputCls} w-28`}
          />
          <button
            type="button"
            onClick={() => remove(i)}
            aria-label={`Remove ${LANGUAGE_LABELS[lang.id]}`}
            className="text-slate-500 hover:text-red-400 transition-colors px-1"
          >
            ✕
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={add}
        className="self-start text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
      >
        + add language
      </button>
    </div>
  )
}
