import { describe, it, expect } from 'vitest'
import { isReviewSource, prReviewBadge } from './prReview'
import { buildTaskCommands } from './taskCommands'
import type { Task } from '../api/client'

const pr = (over = {}) => ({
  pr_url: 'https://github.com/o/r/pull/1', pr_number: 1, pr_state: 'open' as const, head_sha: 'b', reviewed_sha: 'a',
  review_posted: true, last_posted_at: null, stale: true, ...over,
})
const task = (over: Partial<Task> = {}): Task =>
  ({ id: 't', title: 'x', label: 'pr-review', branch: 'pr-review-1-t', source: 'github_pr', pr_review: pr(), ...over }) as Task

describe('prReview', () => {
  it('detects review sources', () => {
    expect(isReviewSource('github_pr')).toBe(true)
    expect(isReviewSource('gitea_pr')).toBe(true)
    expect(isReviewSource('github')).toBe(false)
  })

  it('maps state to a badge', () => {
    expect(prReviewBadge(task())?.label).toBe('New commits since review')
    expect(prReviewBadge(task({ pr_review: pr({ stale: false }) }))?.label).toBe('Review posted')
    expect(prReviewBadge(task({ pr_review: pr({ review_posted: false, reviewed_sha: '' }) }))?.label).toBe('Review pending')
    expect(prReviewBadge(task({ pr_review: pr({ pr_state: 'merged' }) }))?.label).toBe('PR merged')
    expect(prReviewBadge(task({ source: 'github' }))).toBeNull()
  })

  it('never offers Create PR for a review task', () => {
    const noop = () => {}
    const handlers = { moveLabel: noop, approve: noop, requestChanges: noop, rerun: noop, createPR: noop, openPR: noop, togglePause: noop, toggleArchive: noop }
    const ids = buildTaskCommands({ task: task(), workflow: null, canRerun: false, handlers }).map((c) => c.id)
    expect(ids).not.toContain('create-pr')
    expect(ids).toContain('open-pr')
    const normal = buildTaskCommands({ task: task({ source: '', pr_review: null }), workflow: null, canRerun: false, handlers }).map((c) => c.id)
    expect(normal).toContain('create-pr')
  })
})
