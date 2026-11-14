import type { Task } from '../api/client'

/** Review-only PR tasks (a PR ATE did not write): they never push or open PRs. */
export function isReviewSource(source: string | undefined | null): boolean {
  return source === 'github_pr' || source === 'gitea_pr'
}

export interface PRReviewBadge {
  label: string
  tone: 'success' | 'warning' | 'neutral'
  title: string
}

/** The review state shown on the board card and in Task Detail. */
export function prReviewBadge(task: Pick<Task, 'source' | 'pr_review'>): PRReviewBadge | null {
  const pr = task.pr_review
  if (!isReviewSource(task.source) || !pr) return null
  if (pr.pr_state === 'merged' || pr.pr_state === 'closed') {
    return { label: `PR ${pr.pr_state}`, tone: 'neutral', title: `The pull request is ${pr.pr_state}` }
  }
  if (pr.review_posted && pr.stale) {
    return { label: 'New commits since review', tone: 'warning', title: 'The PR head moved past the last posted review; it will be re-reviewed' }
  }
  if (pr.review_posted) {
    return { label: 'Review posted', tone: 'success', title: 'The agent review was posted on the pull request' }
  }
  return { label: 'Review pending', tone: 'neutral', title: 'No review has been posted on the pull request yet' }
}
