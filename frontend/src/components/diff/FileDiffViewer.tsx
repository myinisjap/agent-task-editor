import { Check, ChevronDown, ChevronRight, CornerDownRight, MessageSquare, X } from 'lucide-react'
import Icon from '../ui/Icon'
import { useState } from 'react'
import type { FileDiff, DiffLine } from '../../lib/parseDiff'
import type { DiffComment } from '../../lib/diffComments'

const LINE_BG: Record<DiffLine['type'], string> = {
  add:     'bg-emerald-950 text-success',
  remove:  'bg-red-950 text-danger',
  context: 'text-fg-muted',
  header:  'bg-surface-sunken text-fg-muted',
}

const GUTTER_BG: Record<DiffLine['type'], string> = {
  add:     'bg-emerald-900/60 text-emerald-600',
  remove:  'bg-red-900/60 text-red-600',
  context: 'text-fg-subtle',
  header:  'bg-surface-sunken text-fg-subtle',
}

type LineTarget = {
  side: 'old' | 'new'
  line: number
}

function lineTarget(line: DiffLine): LineTarget | null {
  if (line.type === 'header') return null
  if (line.type === 'remove') {
    return line.oldNo != null ? { side: 'old', line: line.oldNo } : null
  }
  // add + context prefer newNo/new, fall back to oldNo/old
  if (line.newNo != null) return { side: 'new', line: line.newNo }
  if (line.oldNo != null) return { side: 'old', line: line.oldNo }
  return null
}

function sameSide(a: LineTarget, b: LineTarget) {
  return a.side === b.side
}

type DiffLineRowProps = {
  line: DiffLine
  isSelected: boolean
  hasComments: boolean
  isCommenting: boolean
  onGutterMouseDown: (target: LineTarget) => void
  onGutterMouseEnter: (target: LineTarget) => void
  onOpenComment: (target: LineTarget) => void
}

function DiffLineRow({
  line,
  isSelected,
  hasComments,
  isCommenting,
  onGutterMouseDown,
  onGutterMouseEnter,
  onOpenComment,
}: DiffLineRowProps) {
  const prefix = line.type === 'add' ? '+' : line.type === 'remove' ? '-' : line.type === 'header' ? '' : ' '
  const target = lineTarget(line)
  const commentable = target != null

  const highlightClasses = isSelected || isCommenting
    ? 'ring-1 ring-inset ring-amber-500/70'
    : hasComments
      ? 'border-l-2 border-amber-400/80'
      : ''

  return (
    <tr
      className={`${LINE_BG[line.type]} font-mono text-xs group ${highlightClasses}`}
      onMouseDown={() => target && onGutterMouseDown(target)}
      onMouseEnter={() => target && onGutterMouseEnter(target)}
    >
      <td className={`select-none px-2 text-right w-10 ${GUTTER_BG[line.type]} ${commentable ? 'cursor-pointer' : ''}`}>
        {line.oldNo ?? ''}
      </td>
      <td className={`select-none px-2 text-right w-10 ${GUTTER_BG[line.type]} ${commentable ? 'cursor-pointer' : ''}`}>
        {line.newNo ?? ''}
      </td>
      <td className={`select-none px-1.5 w-4 ${GUTTER_BG[line.type]}`}>
        {commentable ? (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              onOpenComment(target)
            }}
            title="Add comment"
            className="opacity-0 group-hover:opacity-100 text-warning hover:text-warning leading-none w-3.5"
          >
            +
          </button>
        ) : (
          prefix
        )}
      </td>
      <td className="px-2 whitespace-pre-wrap break-all select-none">{line.content}</td>
    </tr>
  )
}

type CommentChipProps = {
  comment: DiffComment
  onRemove: (id: string) => void
  onReopen?: (id: string) => void
}

function CommentChip({ comment, onRemove, onReopen }: CommentChipProps) {
  const resolved = comment.status === 'resolved'
  if (resolved) {
    return (
      <tr className="bg-emerald-950/30">
        <td colSpan={4} className="px-3 py-2 border-l-2 border-emerald-500/70">
          <div className="flex items-start gap-2">
            <span className="text-success mt-0.5"><Icon icon={Check} size="xs" /></span>
            <div className="flex-1 min-w-0">
              <p className="text-xs text-fg-muted whitespace-pre-wrap break-words font-sans line-through decoration-slate-600">{comment.comment}</p>
              {comment.resolutionNote && (
                <p className="text-xs text-success/90 whitespace-pre-wrap break-words font-sans mt-1">
                  <Icon icon={CornerDownRight} size="sm" className="inline -mt-0.5 mr-1" />{comment.resolutionNote}
                </p>
              )}
            </div>
            {onReopen && (
              <button
                type="button"
                onClick={() => onReopen(comment.id)}
                className="text-xs text-fg-subtle hover:text-warning shrink-0"
                title="Reopen comment"
              >
                Reopen
              </button>
            )}
            <button
              type="button"
              onClick={() => onRemove(comment.id)}
              className="text-xs text-fg-subtle hover:text-danger shrink-0"
              title="Remove comment"
              aria-label="Remove comment"
            >
              <Icon icon={X} size="sm" />
            </button>
          </div>
        </td>
      </tr>
    )
  }
  return (
    <tr className="bg-amber-950/40">
      <td colSpan={4} className="px-3 py-2 border-l-2 border-amber-400/80">
        <div className="flex items-start gap-2">
          <span className="text-warning mt-0.5"><Icon icon={MessageSquare} size="xs" /></span>
          <div className="flex-1 min-w-0">
            <p className="text-xs text-amber-100 whitespace-pre-wrap break-words font-sans">{comment.comment}</p>
          </div>
          <button
            type="button"
            onClick={() => onRemove(comment.id)}
            className="text-xs text-fg-subtle hover:text-danger shrink-0"
            title="Remove comment"
            aria-label="Remove comment"
          >
            <Icon icon={X} size="sm" />
          </button>
        </div>
      </td>
    </tr>
  )
}

type CommentEditorProps = {
  onSave: (text: string) => void
  onCancel: () => void
  lineLabel: string
}

function CommentEditor({ onSave, onCancel, lineLabel }: CommentEditorProps) {
  const [text, setText] = useState('')
  return (
    <tr className="bg-surface-sunken">
      <td colSpan={4} className="px-3 py-2">
        <div className="flex flex-col gap-2">
          <p className="text-xs text-fg-muted font-sans">Comment on {lineLabel}</p>
          <textarea
            autoFocus
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={2}
            placeholder="Leave feedback on this selection…"
            className="w-full text-xs bg-surface-raised border border-border rounded px-2 py-1.5 text-fg placeholder-slate-500 resize-none focus:outline-none focus:border-amber-500 font-sans"
            onKeyDown={(e) => {
              if (e.key === 'Escape') onCancel()
              if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                e.preventDefault()
                if (text.trim()) onSave(text.trim())
              }
            }}
          />
          <div className="flex gap-2 justify-end">
            <button
              type="button"
              onClick={onCancel}
              className="px-2.5 py-1 text-xs rounded bg-border hover:bg-slate-600 text-fg-muted"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={!text.trim()}
              onClick={() => onSave(text.trim())}
              className="px-2.5 py-1 text-xs rounded bg-amber-600 hover:bg-amber-500 text-black disabled:opacity-50"
            >
              Save comment
            </button>
          </div>
        </div>
      </td>
    </tr>
  )
}

// Files with more lines than this render collapsed by default. Rendering
// every line of every file expanded is what makes a large diff slow — most
// files in a PR are small, but a handful of large ones (generated code,
// lockfiles, vendored files) dominate render cost if forced open. This is
// a conservative alternative to full virtualization: it avoids fighting the
// gutter-drag multi-line selection/inline-comment-editor interactions that
// virtualizing would need to preserve, at the cost of an extra click to
// inspect a big file.
const AUTO_COLLAPSE_LINE_THRESHOLD = 300

function countLines(file: FileDiff): number {
  return file.hunks.reduce((sum, h) => sum + h.lines.length, 0)
}

type FileBlockProps = {
  file: FileDiff
  comments: DiffComment[]
  onAddComment?: (comment: DiffComment) => void
  onRemoveComment?: (id: string) => void
  onReopenComment?: (id: string) => void
}

function FileBlock({ file, comments, onAddComment, onRemoveComment, onReopenComment }: FileBlockProps) {
  const lineCount = countLines(file)
  const [collapsed, setCollapsed] = useState(() => lineCount > AUTO_COLLAPSE_LINE_THRESHOLD)
  const [dragAnchor, setDragAnchor] = useState<LineTarget | null>(null)
  const [selection, setSelection] = useState<{ side: 'old' | 'new'; from: number; to: number } | null>(null)
  const [editingKey, setEditingKey] = useState<string | null>(null)

  const label = file.isNew ? 'new' : file.isDeleted ? 'deleted' : null
  const displayPath = file.newPath || file.oldPath

  const fileComments = comments.filter((c) => c.filePath === displayPath)

  const handleGutterMouseDown = (target: LineTarget) => {
    setDragAnchor(target)
    setSelection({ side: target.side, from: target.line, to: target.line })
    setEditingKey(null)
  }

  const handleGutterMouseEnter = (target: LineTarget) => {
    if (!dragAnchor) return
    if (!sameSide(dragAnchor, target)) return
    setSelection({
      side: dragAnchor.side,
      from: Math.min(dragAnchor.line, target.line),
      to: Math.max(dragAnchor.line, target.line),
    })
  }

  const stopSelecting = () => setDragAnchor(null)

  const openCommentEditorFor = (target: LineTarget) => {
    setSelection({ side: target.side, from: target.line, to: target.line })
    setEditingKey(`${target.side}:${target.line}:${target.line}`)
  }

  const openCommentEditorForSelection = () => {
    if (!selection) return
    setEditingKey(`${selection.side}:${selection.from}:${selection.to}`)
  }

  const isLineSelected = (target: LineTarget | null) => {
    if (!target || !selection) return false
    return target.side === selection.side && target.line >= selection.from && target.line <= selection.to
  }

  const isLineCommenting = (target: LineTarget | null) => {
    if (!target || !editingKey || !selection) return false
    if (!(target.side === selection.side && target.line >= selection.from && target.line <= selection.to)) return false
    return editingKey === `${selection.side}:${selection.from}:${selection.to}`
  }

  const commentsForLine = (target: LineTarget | null) => {
    if (!target) return []
    return fileComments.filter(
      (c) => c.side === target.side && target.line >= c.startLine && target.line <= c.endLine
    )
  }

  const isLastLineOfComment = (target: LineTarget | null, comment: DiffComment) => {
    if (!target) return false
    return target.side === comment.side && target.line === comment.endLine
  }

  const isLastLineOfSelection = (target: LineTarget | null) => {
    if (!target || !selection) return false
    return target.side === selection.side && target.line === selection.to
  }

  return (
    <div className="border border-border rounded mb-3 overflow-hidden">
      <button
        onClick={() => setCollapsed((c) => !c)}
        className="w-full flex items-center gap-2 px-3 py-2 bg-surface-sunken hover:bg-slate-750 text-left"
      >
        <span className="text-fg-muted text-xs select-none"><Icon icon={collapsed ? ChevronRight : ChevronDown} size="sm" /></span>
        <span className="text-xs text-fg font-mono flex-1 truncate">{displayPath}</span>
        {label && (
          <span className={`text-xs px-1.5 py-0.5 rounded font-medium ${
            file.isNew ? 'bg-emerald-900 text-success' : 'bg-red-900 text-danger'
          }`}>{label}</span>
        )}
        {fileComments.length > 0 && (
          <span className="text-xs px-1.5 py-0.5 rounded font-medium bg-amber-900/70 text-warning">
            {fileComments.length} comment{fileComments.length !== 1 ? 's' : ''}
          </span>
        )}
        <span className="text-xs text-fg-subtle">{file.hunks.length} hunk{file.hunks.length !== 1 ? 's' : ''}</span>
        {collapsed && lineCount > AUTO_COLLAPSE_LINE_THRESHOLD && (
          <span className="text-xs text-fg-subtle" title="Large file — collapsed by default for performance">
            ({lineCount} lines)
          </span>
        )}
      </button>

      {!collapsed && (
        <div className="overflow-x-auto" onMouseUp={stopSelecting} onMouseLeave={stopSelecting}>
          {selection && !editingKey && (
            <div className="flex items-center justify-between gap-2 px-3 py-1.5 bg-amber-950/40 border-b border-amber-900/60">
              <p className="text-xs text-amber-200 font-sans">
                {selection.from === selection.to
                  ? `Line ${selection.from} selected`
                  : `Lines ${selection.from}–${selection.to} selected`}
              </p>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setSelection(null)}
                  className="text-xs text-fg-muted hover:text-fg"
                >
                  Clear
                </button>
                <button
                  type="button"
                  onClick={openCommentEditorForSelection}
                  className="text-xs px-2 py-0.5 rounded bg-amber-600 hover:bg-amber-500 text-black"
                >
                  Comment on selection
                </button>
              </div>
            </div>
          )}
          <table className="w-full border-collapse select-none">
            <tbody>
              {file.hunks.flatMap((hunk) =>
                hunk.lines.flatMap((line, j) => {
                  const target = lineTarget(line)
                  const key = `${hunk.header}-${j}`
                  const rows = [
                    <DiffLineRow
                      key={key}
                      line={line}
                      isSelected={isLineSelected(target)}
                      hasComments={commentsForLine(target).some((c) => c.status !== 'resolved')}
                      isCommenting={isLineCommenting(target)}
                      onGutterMouseDown={handleGutterMouseDown}
                      onGutterMouseEnter={handleGutterMouseEnter}
                      onOpenComment={openCommentEditorFor}
                    />,
                  ]

                  // Render inline comment editor directly after the last line of the
                  // active selection/comment target.
                  if (isLastLineOfSelection(target) && editingKey && target) {
                    const lineLabel = selection && selection.from === selection.to
                      ? `line ${selection.from}`
                      : `lines ${selection?.from}-${selection?.to}`
                    rows.push(
                      <CommentEditor
                        key={`${key}-editor`}
                        lineLabel={lineLabel}
                        onCancel={() => setEditingKey(null)}
                        onSave={(text) => {
                          if (!selection) return
                          const quotedText = collectQuotedText(file, selection.side, selection.from, selection.to)
                          onAddComment?.({
                            // Temporary client-side id — the parent replaces it
                            // with the persisted comment returned by the API.
                            id: crypto.randomUUID(),
                            filePath: displayPath,
                            side: selection.side,
                            startLine: selection.from,
                            endLine: selection.to,
                            quotedText,
                            comment: text,
                            status: 'open',
                          })
                          setEditingKey(null)
                          setSelection(null)
                        }}
                      />
                    )
                  }

                  // Render existing comment chips after the last line they cover.
                  for (const c of commentsForLine(target)) {
                    if (isLastLineOfComment(target, c)) {
                      rows.push(
                        <CommentChip
                          key={`${key}-comment-${c.id}`}
                          comment={c}
                          onRemove={(id) => onRemoveComment?.(id)}
                          onReopen={onReopenComment ? (id) => onReopenComment(id) : undefined}
                        />
                      )
                    }
                  }

                  return rows
                })
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function collectQuotedText(file: FileDiff, side: 'old' | 'new', from: number, to: number): string {
  const lines: string[] = []
  for (const hunk of file.hunks) {
    for (const line of hunk.lines) {
      const target = lineTarget(line)
      if (!target) continue
      if (target.side === side && target.line >= from && target.line <= to) {
        lines.push(line.content)
      }
    }
  }
  return lines.join('\n')
}

type Props = {
  files: FileDiff[]
  loading?: boolean
  comments?: DiffComment[]
  onAddComment?: (comment: DiffComment) => void
  onRemoveComment?: (id: string) => void
  onReopenComment?: (id: string) => void
}

export default function FileDiffViewer({ files, loading, comments = [], onAddComment, onRemoveComment, onReopenComment }: Props) {
  if (loading) return <p className="text-xs text-fg-subtle">Loading diff…</p>
  if (files.length === 0) return <p className="text-xs text-fg-subtle">No changes</p>

  const openCount = comments.filter((c) => c.status !== 'resolved').length
  const resolvedCount = comments.length - openCount

  return (
    <div>
      <p className="text-xs text-fg-subtle mb-3">
        {files.length} file{files.length !== 1 ? 's' : ''} changed
        {openCount > 0 && (
          <span className="ml-2 text-warning">
            · {openCount} open comment{openCount !== 1 ? 's' : ''}
          </span>
        )}
        {resolvedCount > 0 && (
          <span className="ml-2 text-success">
            · {resolvedCount} resolved
          </span>
        )}
      </p>
      {files.map((f) => (
        <FileBlock
          key={f.newPath || f.oldPath}
          file={f}
          comments={comments}
          onAddComment={onAddComment}
          onRemoveComment={onRemoveComment}
          onReopenComment={onReopenComment}
        />
      ))}
    </div>
  )
}
