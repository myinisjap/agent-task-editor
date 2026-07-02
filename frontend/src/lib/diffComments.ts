// Ephemeral, frontend-only inline diff review comments.
// These are not persisted to the backend — they are collected during a review
// session and merged into the free-text rejection note when the reviewer
// clicks "Reject", giving the agent file/line-anchored context for the feedback.

export type DiffComment = {
  id: string
  filePath: string
  side: 'old' | 'new'
  startLine: number
  endLine: number
  quotedText: string
  comment: string
}

/**
 * Combine the free-text rejection note with any inline diff comments into a
 * single note string suitable for `api.tasks.reject(id, note)`. The result is
 * formatted so the agent can clearly see which file/line(s) each comment
 * refers to, along with the quoted diff content for context.
 */
export function buildRejectionNote(freeText: string, comments: DiffComment[]): string {
  const trimmedFreeText = freeText.trim()
  if (comments.length === 0) return trimmedFreeText

  const sections = comments.map((c, i) => {
    const lineRef = c.startLine === c.endLine
      ? `line ${c.startLine}`
      : `lines ${c.startLine}-${c.endLine}`
    return [
      `${i + 1}. ${c.filePath} (${lineRef}):`,
      '```',
      c.quotedText,
      '```',
      `→ ${c.comment}`,
    ].join('\n')
  })

  const header = trimmedFreeText ? `${trimmedFreeText}\n\n` : ''
  return `${header}Inline review comments:\n\n${sections.join('\n\n')}`
}
