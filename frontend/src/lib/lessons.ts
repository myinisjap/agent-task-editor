export const LESSON_BODY_MAX = 300
export const LESSON_KINDS = ['convention', 'trap', 'command', 'decision', 'preference'] as const
export const LESSON_STATUSES = ['proposed', 'active', 'stale', 'retired'] as const

/** Trim and cap text to the server's lesson body limit. */
export function lessonPrefill(text: string): string {
  return text.trim().slice(0, LESSON_BODY_MAX).trimEnd()
}

/** Split comma/newline separated input into trimmed, deduped, non-empty items. */
export function parseList(input: string): string[] {
  const out: string[] = []
  for (const part of input.split(/[,\n]/)) {
    const t = part.trim()
    if (t && !out.includes(t)) out.push(t)
  }
  return out
}

export function formatList(items: string[]): string {
  return items.join(', ')
}
