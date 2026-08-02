import { TERMINAL_KEYS } from '../../lib/termKeys'

type Props = {
  /** Write a raw byte sequence to the PTY. */
  onKey: (seq: string) => void
  /** Arm/disarm the sticky Ctrl modifier. */
  onToggleCtrl: () => void
  ctrlArmed: boolean
}

// A scrollable row of the keys a phone keyboard doesn't have, sitting directly
// above the on-screen keyboard. Mobile only — desktop has the real keys.
export default function TerminalKeyBar({ onKey, onToggleCtrl, ctrlArmed }: Props) {
  // Keep the on-screen keyboard up. Tapping a button would otherwise move focus
  // off xterm's hidden textarea, which dismisses the keyboard; suppressing the
  // default on pointerdown stops the button taking focus while click still fires.
  const keepFocus = (e: React.PointerEvent<HTMLButtonElement>) => e.preventDefault()

  const key = 'shrink-0 min-w-9 px-2.5 py-2 rounded border border-slate-700 text-xs font-mono select-none'

  return (
    <div
      data-testid="terminal-key-bar"
      className="md:hidden shrink-0 flex items-center gap-1 overflow-x-auto overscroll-x-contain border-t border-slate-800 bg-slate-900 px-2 py-1.5 touch-manipulation"
    >
      <button
        type="button"
        onPointerDown={keepFocus}
        onClick={onToggleCtrl}
        aria-pressed={ctrlArmed}
        title="Ctrl — applies to the next key you type"
        className={`${key} ${ctrlArmed ? 'bg-indigo-600 border-indigo-500 text-white' : 'bg-slate-800 text-slate-200 active:bg-slate-700'}`}
      >
        Ctrl
      </button>
      {TERMINAL_KEYS.map((k) => (
        <button
          key={k.label}
          type="button"
          onPointerDown={keepFocus}
          onClick={() => onKey(k.seq)}
          title={k.title}
          aria-label={k.title}
          className={`${key} bg-slate-800 text-slate-200 active:bg-slate-700`}
        >
          {k.label}
        </button>
      ))}
    </div>
  )
}
