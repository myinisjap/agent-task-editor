import { useEffect, useState } from 'react'
import { api } from '../../api/client'
import { parseDiff, type FileDiff } from '../../lib/parseDiff'
import type { DiffComment } from '../../lib/diffComments'
import FileDiffViewer from '../diff/FileDiffViewer'

// DiffReviewPane renders the Diff tab: PR link/Create PR/Refresh header plus
// the file diff viewer wired to review comments. Diff files/loading state are
// owned here (fetched per taskId); diffComments are owned by the caller's
// useDiffComments hook since they're also needed by the parent's approval
// panel banner and reject-validation logic.
export default function DiffReviewPane({
  taskId,
  prUrl,
  onCreatePR,
  creatingPR,
  diffComments,
  onAddComment,
  onRemoveComment,
  onReopenComment,
}: {
  taskId: string | undefined
  prUrl?: string
  onCreatePR: () => void
  creatingPR: boolean
  diffComments: DiffComment[]
  onAddComment: (draft: DiffComment) => void
  onRemoveComment: (commentId: string) => void
  onReopenComment: (commentId: string) => void
}) {
  const [diffFiles, setDiffFiles] = useState<FileDiff[]>([])
  const [diffLoading, setDiffLoading] = useState(false)

  const loadDiff = () => {
    if (!taskId) return
    setDiffLoading(true)
    api.tasks.diff(taskId)
      .then((d) => setDiffFiles(parseDiff(d.diff)))
      .catch(() => setDiffFiles([]))
      .finally(() => setDiffLoading(false))
  }

  // Load diff when task is available
  useEffect(() => {
    loadDiff()
  }, [taskId]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="h-full overflow-y-auto p-4" data-testid="diff-review-pane">
      <div className="flex items-center justify-between mb-3">
        <p className="text-xs text-slate-500">Changes on this task's branch</p>
        <div className="flex items-center gap-3">
          {prUrl ? (
            <a
              href={prUrl}
              target="_blank"
              rel="noreferrer"
              className="px-3 py-1.5 text-xs font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white"
            >
              View PR ↗
            </a>
          ) : (
            <button
              onClick={onCreatePR}
              disabled={creatingPR}
              className="px-3 py-1.5 text-xs font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
              title="Push the branch and open a GitHub pull request"
            >
              {creatingPR ? 'Creating PR…' : 'Create PR'}
            </button>
          )}
          <button
            onClick={loadDiff}
            className="px-3 py-1.5 text-xs font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-200"
          >
            ↻ Refresh
          </button>
        </div>
      </div>
      <FileDiffViewer
        files={diffFiles}
        loading={diffLoading}
        comments={diffComments}
        onAddComment={onAddComment}
        onRemoveComment={onRemoveComment}
        onReopenComment={onReopenComment}
      />
    </div>
  )
}
