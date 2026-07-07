import { useCallback, useEffect, useState } from 'react'
import { api } from '../../api/client'
import { fromApiComment, type DiffComment } from '../../lib/diffComments'

// useDiffComments owns the review-comment list for a task's diff, shared
// between the Diff tab (rendering + add/remove/reopen) and the parent's
// approval panel (open-comment count banner + reject validation).
export function useDiffComments(taskId: string | undefined) {
  const [diffComments, setDiffComments] = useState<DiffComment[]>([])

  const refreshComments = useCallback(() => {
    if (!taskId) return
    api.tasks.reviewComments(taskId)
      .then((cs) => setDiffComments((cs ?? []).map(fromApiComment)))
      .catch(() => {})
  }, [taskId])

  // Load persisted review comments (open + resolved) when the task changes.
  useEffect(() => {
    refreshComments()
  }, [refreshComments])

  const handleAddComment = async (draft: DiffComment) => {
    if (!taskId) return
    // Optimistic insert with the draft's temporary id, replaced (or rolled
    // back) once the API responds.
    setDiffComments((prev) => [...prev, draft])
    try {
      const created = await api.tasks.addReviewComment(taskId, {
        file_path: draft.filePath,
        side: draft.side,
        start_line: draft.startLine,
        end_line: draft.endLine,
        quoted_text: draft.quotedText,
        body: draft.comment,
      })
      setDiffComments((prev) => prev.map((c) => (c.id === draft.id ? fromApiComment(created) : c)))
    } catch (e: any) {
      setDiffComments((prev) => prev.filter((c) => c.id !== draft.id))
      alert(`Failed to save comment: ${e.message ?? e}`)
    }
  }

  const handleRemoveComment = async (commentId: string) => {
    if (!taskId) return
    try {
      await api.tasks.deleteReviewComment(taskId, commentId)
      setDiffComments((prev) => prev.filter((c) => c.id !== commentId))
    } catch (e: any) {
      alert(`Failed to delete comment: ${e.message ?? e}`)
    }
  }

  const handleReopenComment = async (commentId: string) => {
    if (!taskId) return
    try {
      const updated = await api.tasks.updateReviewComment(taskId, commentId, { status: 'open' })
      setDiffComments((prev) => prev.map((c) => (c.id === commentId ? fromApiComment(updated) : c)))
    } catch (e: any) {
      alert(`Failed to reopen comment: ${e.message ?? e}`)
    }
  }

  const openComments = diffComments.filter((c) => c.status !== 'resolved')

  return {
    diffComments,
    openComments,
    refreshComments,
    handleAddComment,
    handleRemoveComment,
    handleReopenComment,
  }
}
