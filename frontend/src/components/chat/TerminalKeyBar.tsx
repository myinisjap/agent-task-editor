import { TERMINAL_KEYS } from '../../lib/termKeys'

type Props = {
  /** Write a raw byte sequence to the PTY. */
  onKey: (seq: string) => void
  /** Arm/disarm the sticky Ctrl modifier. */
  onToggleCtrl: () => void
  ctrlArmed: boolean
  /** Last terminal line looks like a question; highlight the quick answers. */
  promptLike?: boolean
}

// One-tap replies to a CLI prompt: the text followed by Enter.
export const QUICK_ANSWERS = [
  { label: 'Enter', seq: '\r' },
  { label: 'y', seq: 'y\r' },
  { label: 'n', seq: 'n\r' },
  { label: '1', seq: '1\r' },
  { label: '2', seq: '2\r' },
  { label: '3', seq: '3\r' },
]

// A scrollable row of the keys a phone keyboard doesn't have, sitting directly
// above the on-screen keyboard. Mobile only — desktop has the real keys.
export default function TerminalKeyBar({ onKey, onToggleCtrl, ctrlArmed, promptLike = false }: Props) {
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
      <div
        data-testid="quick-answers"
        data-prompt={promptLike}
        className={`shrink-0 flex items-center gap-1 pr-2 mr-1 border-r border-slate-700 rounded ${promptLike ? 'bg-indigo-950 ring-1 ring-indigo-500' : ''}`}
      >
        {QUICK_ANSWERS.map((a) => (
          <button
            key={a.label}
            type="button"
            onPointerDown={keepFocus}
            onClick={() => onKey(a.seq)}
            aria-label={`Answer ${a.label}`}
            className={`${key} ${promptLike ? 'bg-indigo-600 border-indigo-500 text-white' : 'bg-slate-800 text-slate-200'} active:bg-slate-700`}
          >
            {a.label}
          </button>
        ))}
      </div>
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
