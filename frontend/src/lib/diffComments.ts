// View model for inline diff review comments.
// Comments are persisted on the backend (task_review_comments) and injected
// into every agent run's prompt while open; agents resolve them via the MCP
// resolve_comment tool. This module maps the API wire type to the shape the
// diff viewer renders.

import type { ReviewComment } from '../api/client'

export type DiffComment = {
  id: string
  filePath: string
  side: 'old' | 'new'
  startLine: number
  endLine: number
  quotedText: string
  comment: string
  status: 'open' | 'resolved'
  resolutionNote?: string | null
}

/** Map an API ReviewComment to the diff viewer's view model. */
export function fromApiComment(c: ReviewComment): DiffComment {
  return {
    id: c.id,
    filePath: c.file_path,
    side: c.side,
    startLine: c.start_line,
    endLine: c.end_line,
    quotedText: c.quoted_text,
    comment: c.body,
    status: c.status,
    resolutionNote: c.resolution_note,
  }
}
