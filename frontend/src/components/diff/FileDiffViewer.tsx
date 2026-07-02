import { useState } from 'react'
import type { FileDiff, DiffLine } from '../../lib/parseDiff'
import type { DiffComment } from '../../lib/diffComments'

const LINE_BG: Record<DiffLine['type'], string> = {
  add:     'bg-emerald-950 text-emerald-300',
  remove:  'bg-red-950 text-red-300',
  context: 'text-slate-400',
  header:  'bg-slate-800 text-slate-400',
}

const GUTTER_BG: Record<DiffLine['type'], string> = {
  add:     'bg-emerald-900/60 text-emerald-600',
  remove:  'bg-red-900/60 text-red-600',
  context: 'text-slate-600',
  header:  'bg-slate-800 text-slate-600',
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
            className="opacity-0 group-hover:opacity-100 text-amber-400 hover:text-amber-300 leading-none w-3.5"
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
}

function CommentChip({ comment, onRemove }: CommentChipProps) {
  return (
    <tr className="bg-amber-950/40">
      <td colSpan={4} className="px-3 py-2 border-l-2 border-amber-400/80">
        <div className="flex items-start gap-2">
          <span className="text-amber-400 text-xs mt-0.5">💬</span>
          <div className="flex-1 min-w-0">
            <p className="text-xs text-amber-100 whitespace-pre-wrap break-words font-sans">{comment.comment}</p>
          </div>
          <button
            type="button"
            onClick={() => onRemove(comment.id)}
            className="text-xs text-slate-500 hover:text-red-400 shrink-0"
            title="Remove comment"
          >
            ✕
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
    <tr className="bg-slate-800">
      <td colSpan={4} className="px-3 py-2">
        <div className="flex flex-col gap-2">
          <p className="text-xs text-slate-400 font-sans">Comment on {lineLabel}</p>
          <textarea
            autoFocus
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={2}
            placeholder="Leave feedback on this selection…"
            className="w-full text-xs bg-slate-900 border border-slate-600 rounded px-2 py-1.5 text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-amber-500 font-sans"
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
              className="px-2.5 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-300"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={!text.trim()}
              onClick={() => onSave(text.trim())}
              className="px-2.5 py-1 text-xs rounded bg-amber-600 hover:bg-amber-500 text-white disabled:opacity-50"
            >
              Save comment
            </button>
          </div>
        </div>
      </td>
    </tr>
  )
}

type FileBlockProps = {
  file: FileDiff
  comments: DiffComment[]
  onAddComment?: (comment: DiffComment) => void
  onRemoveComment?: (id: string) => void
}

function FileBlock({ file, comments, onAddComment, onRemoveComment }: FileBlockProps) {
  const [collapsed, setCollapsed] = useState(false)
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
    <div className="border border-slate-700 rounded mb-3 overflow-hidden">
      <button
        onClick={() => setCollapsed((c) => !c)}
        className="w-full flex items-center gap-2 px-3 py-2 bg-slate-800 hover:bg-slate-750 text-left"
      >
        <span className="text-slate-400 text-xs select-none">{collapsed ? '▶' : '▼'}</span>
        <span className="text-xs text-slate-200 font-mono flex-1 truncate">{displayPath}</span>
        {label && (
          <span className={`text-xs px-1.5 py-0.5 rounded font-medium ${
            file.isNew ? 'bg-emerald-900 text-emerald-300' : 'bg-red-900 text-red-300'
          }`}>{label}</span>
        )}
        {fileComments.length > 0 && (
          <span className="text-xs px-1.5 py-0.5 rounded font-medium bg-amber-900/70 text-amber-300">
            {fileComments.length} comment{fileComments.length !== 1 ? 's' : ''}
          </span>
        )}
        <span className="text-xs text-slate-500">{file.hunks.length} hunk{file.hunks.length !== 1 ? 's' : ''}</span>
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
                  className="text-xs text-slate-400 hover:text-slate-200"
                >
                  Clear
                </button>
                <button
                  type="button"
                  onClick={openCommentEditorForSelection}
                  className="text-xs px-2 py-0.5 rounded bg-amber-600 hover:bg-amber-500 text-white"
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
                      hasComments={commentsForLine(target).length > 0}
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
                            id: crypto.randomUUID(),
                            filePath: displayPath,
                            side: selection.side,
                            startLine: selection.from,
                            endLine: selection.to,
                            quotedText,
                            comment: text,
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
}

export default function FileDiffViewer({ files, loading, comments = [], onAddComment, onRemoveComment }: Props) {
  if (loading) return <p className="text-xs text-slate-500">Loading diff…</p>
  if (files.length === 0) return <p className="text-xs text-slate-600">No changes</p>

  return (
    <div>
      <p className="text-xs text-slate-500 mb-3">
        {files.length} file{files.length !== 1 ? 's' : ''} changed
        {comments.length > 0 && (
          <span className="ml-2 text-amber-400">
            · {comments.length} inline comment{comments.length !== 1 ? 's' : ''}
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
        />
      ))}
    </div>
  )
}
