import { describe, it, expect } from 'vitest'
import { fromApiComment } from './diffComments'
import type { ReviewComment } from '../api/client'

const apiComment: ReviewComment = {
  id: 'c1',
  task_id: 't1',
  file_path: 'src/lib/parseDiff.ts',
  side: 'new',
  start_line: 10,
  end_line: 12,
  quoted_text: 'const x = 1',
  body: 'please rename this',
  status: 'open',
  resolution_note: null,
  resolved_by_run_id: null,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

describe('fromApiComment', () => {
  it('maps the API wire type to the diff viewer view model', () => {
    expect(fromApiComment(apiComment)).toEqual({
      id: 'c1',
      filePath: 'src/lib/parseDiff.ts',
      side: 'new',
      startLine: 10,
      endLine: 12,
      quotedText: 'const x = 1',
      comment: 'please rename this',
      status: 'open',
      resolutionNote: null,
    })
  })

  it('preserves a resolution note and resolved status', () => {
    const resolved = fromApiComment({
      ...apiComment,
      status: 'resolved',
      resolution_note: 'done in commit abc',
    })
    expect(resolved.status).toBe('resolved')
    expect(resolved.resolutionNote).toBe('done in commit abc')
  })

  it('does not carry over API-only fields', () => {
    const mapped = fromApiComment(apiComment)
    expect(mapped).not.toHaveProperty('task_id')
    expect(mapped).not.toHaveProperty('resolved_by_run_id')
    expect(mapped).not.toHaveProperty('created_at')
  })
})
