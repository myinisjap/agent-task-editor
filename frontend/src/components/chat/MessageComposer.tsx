import { useState } from 'react'
import { buildSendPayload } from '../../lib/chatInput'

type Props = {
  /** Write raw bytes to the PTY. */
  onSend: (data: string) => void
  /** True when keystrokes go straight to xterm instead of this box. */
  directMode: boolean
  onToggleDirect: () => void
}

// Native textarea composer (phones only) so autocorrect, swipe typing, dictation
// and paste work normally. Sits above the key bar.
export default function MessageComposer({ onSend, directMode, onToggleDirect }: Props) {
  const [text, setText] = useState('')

  function send() {
    onSend(buildSendPayload(text))
    setText('')
  }

  return (
    <div
      data-testid="message-composer"
      className="md:hidden shrink-0 flex items-end gap-2 border-t border-slate-800 bg-slate-900 px-2 py-1.5"
    >
      <button
        type="button"
        onClick={onToggleDirect}
        aria-pressed={directMode}
        aria-label="Type directly into terminal"
        title="Type directly into terminal"
        className={`shrink-0 px-2.5 py-2 rounded border text-xs ${
          directMode ? 'bg-indigo-600 border-indigo-500 text-white' : 'border-slate-700 text-slate-300'
        }`}
      >
        ⌨
      </button>
      {!directMode && (
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={2}
          placeholder="Message…"
          aria-label="Message"
          className="flex-1 min-w-0 max-h-32 resize-none rounded bg-slate-800 border border-slate-700 text-slate-100 text-base px-2 py-1.5"
        />
      )}
      {directMode && <div className="flex-1 text-xs text-slate-500 py-2">Typing into terminal</div>}
      <button
        type="button"
        onClick={send}
        className="shrink-0 px-3 py-2 rounded bg-indigo-600 hover:bg-indigo-500 text-white text-sm"
      >
        Send
      </button>
    </div>
  )
}
