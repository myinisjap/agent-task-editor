import ModalShell from './ModalShell'
import { INBOX_SHORTCUTS, SHORTCUTS, formatKeys, isMacPlatform, type Shortcut } from '../../lib/shortcuts'

function Section({ title, items }: { title: string; items: Shortcut[] }) {
  const mac = isMacPlatform()
  return (
    <section className="mb-4">
      <h3 className="text-xs font-semibold uppercase tracking-wide text-slate-500 mb-2">{title}</h3>
      <ul className="flex flex-col gap-1.5">
        {items.map((s) => (
          <li key={s.id} className="flex items-center justify-between gap-4 text-sm text-slate-300">
            <span>{s.label}</span>
            <kbd className="rounded border border-slate-600 bg-slate-800 px-1.5 py-0.5 text-xs text-slate-200 whitespace-nowrap">
              {formatKeys(s.keys, mac)}
            </kbd>
          </li>
        ))}
      </ul>
    </section>
  )
}

export default function ShortcutSheet({ onClose }: { onClose: () => void }) {
  return (
    <ModalShell onClose={onClose} ariaLabel="Keyboard shortcuts" className="max-w-md w-full">
      <div className="p-5">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-base font-semibold text-slate-100">Keyboard shortcuts</h2>
          <button onClick={onClose} aria-label="Close" className="text-slate-400 hover:text-slate-100">✕</button>
        </div>
        <Section title="Global" items={SHORTCUTS} />
        <Section title="Inbox (wide screens)" items={INBOX_SHORTCUTS} />
      </div>
    </ModalShell>
  )
}
