// Key definitions for the mobile terminal key bar.
//
// Phone/tablet on-screen keyboards have no Esc, Tab or arrow keys, which makes
// most terminal UIs (including the Claude CLI, whose Shift+Tab toggles modes)
// unusable on mobile. These are the raw byte sequences a physical keyboard
// would send, written straight to the PTY.

export type TerminalKey = {
  /** Short glyph shown on the button. */
  label: string
  /** Bytes written to the PTY when pressed. */
  seq: string
  /** Accessible name / tooltip. */
  title: string
}

export const TERMINAL_KEYS: TerminalKey[] = [
  { label: 'Esc', seq: '\x1b', title: 'Escape' },
  { label: 'Tab', seq: '\t', title: 'Tab' },
  { label: '⇧Tab', seq: '\x1b[Z', title: 'Shift+Tab' },
  { label: '↑', seq: '\x1b[A', title: 'Up arrow' },
  { label: '↓', seq: '\x1b[B', title: 'Down arrow' },
  { label: '←', seq: '\x1b[D', title: 'Left arrow' },
  { label: '→', seq: '\x1b[C', title: 'Right arrow' },
  { label: '^C', seq: '\x03', title: 'Ctrl+C (interrupt)' },
  { label: '^D', seq: '\x04', title: 'Ctrl+D (end of input)' },
  { label: '^Z', seq: '\x1a', title: 'Ctrl+Z (suspend)' },
  { label: 'Home', seq: '\x1b[H', title: 'Home' },
  { label: 'End', seq: '\x1b[F', title: 'End' },
  { label: 'PgUp', seq: '\x1b[5~', title: 'Page up' },
  { label: 'PgDn', seq: '\x1b[6~', title: 'Page down' },
]

// Fold a single character into the control code a real Ctrl+<key> would send,
// so the sticky Ctrl button composes with letters typed on the device keyboard
// (Ctrl+R for reverse search, Ctrl+A/E for line editing, ...).
// Returns null when the character has no control equivalent — the caller then
// sends the keystroke through unchanged rather than swallowing it.
export function toControlChar(data: string): string | null {
  if (data.length !== 1) return null
  const code = data.toUpperCase().charCodeAt(0)
  // @ A-Z [ \ ] ^ _  ->  0x00-0x1f
  if (code >= 64 && code <= 95) return String.fromCharCode(code - 64)
  if (code === 32) return '\x00' // Ctrl+Space -> NUL
  if (code === 63) return '\x7f' // Ctrl+? -> DEL
  return null
}
