import type { TaskSourceComment } from '../../api/client'

// formatTimestamp renders a compact, locale-aware date/time for a comment,
// mirroring LabelHistoryList's formatting.
function formatTimestamp(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

// SourceCommentsList renders the comment thread ingested from a task's
// source item (e.g. its GitHub issue), styled like LabelHistoryList. Only
// ingested when the repo opts into issue_comment_sync_enabled; the content
// is external and untrusted (see docs/task-sources.md and the prompt-side
// SOURCE ISSUE COMMENTS delimiting), so it's called out as such here too.
export default function SourceCommentsList({ comments }: { comments: TaskSourceComment[] }) {
  if (comments.length === 0) return null

  return (
    <div>
      <p className="text-xs text-slate-500 mb-2">
        Source issue comments{' '}
        <span className="text-slate-600">(from the GitHub issue thread — external, untrusted context)</span>
      </p>
      <div className="flex flex-col gap-2">
        {comments.map((c) => (
          <div key={c.id} className="text-xs px-2 py-1.5 rounded bg-slate-800/60 border border-slate-700">
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium text-slate-300">@{c.author}</span>
              <span className="text-slate-500 text-[11px]">{formatTimestamp(c.external_created_at)}</span>
            </div>
            <p className="text-slate-400 mt-1 whitespace-pre-wrap">{c.body}</p>
          </div>
        ))}
      </div>
    </div>
  )
}
