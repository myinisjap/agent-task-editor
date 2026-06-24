import { useState } from 'react'
import type { FileDiff, DiffLine } from '../../lib/parseDiff'

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

function DiffLineRow({ line }: { line: DiffLine }) {
  const prefix = line.type === 'add' ? '+' : line.type === 'remove' ? '-' : line.type === 'header' ? '' : ' '
  return (
    <tr className={`${LINE_BG[line.type]} font-mono text-xs`}>
      <td className={`select-none px-2 text-right w-10 ${GUTTER_BG[line.type]}`}>
        {line.oldNo ?? ''}
      </td>
      <td className={`select-none px-2 text-right w-10 ${GUTTER_BG[line.type]}`}>
        {line.newNo ?? ''}
      </td>
      <td className={`select-none px-1.5 w-4 ${GUTTER_BG[line.type]}`}>{prefix}</td>
      <td className="px-2 whitespace-pre-wrap break-all">{line.content}</td>
    </tr>
  )
}

function FileBlock({ file }: { file: FileDiff }) {
  const [collapsed, setCollapsed] = useState(false)

  const label = file.isNew ? 'new' : file.isDeleted ? 'deleted' : null
  const displayPath = file.newPath || file.oldPath

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
        <span className="text-xs text-slate-500">{file.hunks.length} hunk{file.hunks.length !== 1 ? 's' : ''}</span>
      </button>

      {!collapsed && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse">
            <tbody>
              {file.hunks.flatMap((hunk) =>
                hunk.lines.map((line, j) => (
                  <DiffLineRow key={`${hunk.header}-${j}`} line={line} />
                ))
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

type Props = {
  files: FileDiff[]
  loading?: boolean
}

export default function FileDiffViewer({ files, loading }: Props) {
  if (loading) return <p className="text-xs text-slate-500">Loading diff…</p>
  if (files.length === 0) return <p className="text-xs text-slate-600">No changes</p>

  return (
    <div>
      <p className="text-xs text-slate-500 mb-3">
        {files.length} file{files.length !== 1 ? 's' : ''} changed
      </p>
      {files.map((f) => (
        <FileBlock key={f.newPath || f.oldPath} file={f} />
      ))}
    </div>
  )
}
