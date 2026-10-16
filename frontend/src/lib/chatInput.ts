export const BRACKETED_PASTE_START = '\x1b[200~'
export const BRACKETED_PASTE_END = '\x1b[201~'

/** Bytes to write to the PTY for a composer message. Empty -> just Enter;
 * multi-line -> one bracketed paste then Enter; single line -> text + Enter. */
export function buildSendPayload(text: string): string {
  const t = text.replace(/\r\n?/g, '\n')
  if (t.trim() === '') return '\r'
  if (t.includes('\n')) return `${BRACKETED_PASTE_START}${t}${BRACKETED_PASTE_END}\r`
  return `${t}\r`
}

export const TAP_MAX_DISTANCE = 10 // px
export const TAP_MAX_DURATION = 300 // ms

/** A tap is a short, nearly stationary touch; anything else is a drag/long-press. */
export function isTap(dx: number, dy: number, durationMs: number): boolean {
  return Math.hypot(dx, dy) <= TAP_MAX_DISTANCE && durationMs <= TAP_MAX_DURATION
}

/** Accumulates pixel movement into whole-line scroll amounts, carrying the remainder. */
export function pixelsToLines(pixels: number, remainder: number, lineHeight: number): { lines: number; remainder: number } {
  const total = pixels + remainder
  const lines = Math.trunc(total / lineHeight)
  return { lines, remainder: total - lines * lineHeight }
}
