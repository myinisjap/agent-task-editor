import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '../../api/client'
import { fromApiComment, type DiffComment } from '../../lib/diffComments'

// useDiffComments owns the review-comment list for a task's diff, shared
// between the Diff tab (rendering + add/remove/reopen) and the parent's
// approval panel (open-comment count banner + reject validation).
export function useDiffComments(taskId: string | undefined) {
  const [diffComments, setDiffComments] = useState<DiffComment[]>([])
  // Tracks the taskId currently "owned" by this hook instance so an in-flight
  // refreshComments() response can't clobber state after the caller (e.g.
  // TaskDetailPage on navigation) has moved on to a different taskId. Kept in
  // sync synchronously during render (not in an effect) so there's no window
  // where a response could land and read a stale value before an effect runs.
  const currentTaskIdRef = useRef(taskId)
  currentTaskIdRef.current = taskId

  const refreshComments = useCallback(() => {
    if (!taskId) return
    api.tasks.reviewComments(taskId)
      .then((cs) => {
        if (currentTaskIdRef.current !== taskId) return
        setDiffComments((cs ?? []).map(fromApiComment))
      })
      .catch(() => {})
  }, [taskId])

  // Reset comments when the task changes so a navigation never shows the
  // previous task's comments while the new fetch is in flight.
  useEffect(() => {
    setDiffComments([])
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
      // Bail if the user navigated to a different task while this request
      // was in flight — applying it now would inject task A's comment into
      // task B's list.
      if (currentTaskIdRef.current !== taskId) return
      setDiffComments((prev) => prev.map((c) => (c.id === draft.id ? fromApiComment(created) : c)))
    } catch (e: any) {
      if (currentTaskIdRef.current !== taskId) return
      setDiffComments((prev) => prev.filter((c) => c.id !== draft.id))
      alert(`Failed to save comment: ${e.message ?? e}`)
    }
  }

  const handleRemoveComment = async (commentId: string) => {
    if (!taskId) return
    try {
      await api.tasks.deleteReviewComment(taskId, commentId)
      if (currentTaskIdRef.current !== taskId) return
      setDiffComments((prev) => prev.filter((c) => c.id !== commentId))
    } catch (e: any) {
      if (currentTaskIdRef.current !== taskId) return
      alert(`Failed to delete comment: ${e.message ?? e}`)
    }
  }

  const handleReopenComment = async (commentId: string) => {
    if (!taskId) return
    try {
      const updated = await api.tasks.updateReviewComment(taskId, commentId, { status: 'open' })
      if (currentTaskIdRef.current !== taskId) return
      setDiffComments((prev) => prev.map((c) => (c.id === commentId ? fromApiComment(updated) : c)))
    } catch (e: any) {
      if (currentTaskIdRef.current !== taskId) return
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
