import type { LucideIcon } from 'lucide-react'
import { Bot, ChevronDown, ChevronUp, CircleX, ClipboardList, CornerDownRight, Diamond, Dot, FileText, FolderSearch, Globe, ListChecks, MessageSquare, Pencil, Search, Terminal, Wrench, X } from 'lucide-react'
import Icon from '../ui/Icon'
import { useState } from 'react'
import ReactMarkdown from 'react-markdown'
import { resultView, type LogRow } from '../../lib/groupAgentLog'
import { truncateOutput } from '../../lib/runTimeline'
import type { LogSeverity } from '../../lib/logEvents'

interface Props {
  row: LogRow
}

const SEVERITY_STYLE: Record<LogSeverity, { glyph: LucideIcon; icon: string; text: string; row: string }> = {
  error: { glyph: X, icon: 'text-danger', text: 'text-danger', row: 'bg-red-950/20' },
  warning: { glyph: Diamond, icon: 'text-yellow-600', text: 'text-yellow-500', row: '' },
  info: { glyph: Dot, icon: 'text-fg-subtle', text: 'text-fg-muted', row: '' },
  debug: { glyph: Dot, icon: 'text-fg-subtle', text: 'text-fg-subtle', row: 'opacity-80' },
}

const TOOL_ICONS: Record<string, LucideIcon> = {
  Read: FileText,
  Write: Pencil,
  Edit: Pencil,
  str_replace_based_edit_tool: Pencil,
  Bash: Terminal,
  Grep: Search,
  Glob: FolderSearch,
  WebFetch: Globe,
  WebSearch: Globe,
  Task: ClipboardList,
  TaskCreate: ClipboardList,
  TodoWrite: ListChecks,
  Agent: Bot,
}

function toolIcon(name: string): LucideIcon {
  return TOOL_ICONS[name] ?? Wrench
}

function formatTs(ts: string | null | undefined): string | null {
  if (!ts) return null
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function Timestamp({ ts }: { ts: string | null }) {
  if (!ts) return null
  return <span className="text-fg-subtle text-xs shrink-0 tabular-nums">{ts}</span>
}

// Output revealed by a row's disclosure. Rendered once — the collapsed row never
// carries a slice of this text.
function OutputBlock({ text }: { text: string }) {
  const [showAll, setShowAll] = useState(false)
  const { head, truncated, hiddenLines } = truncateOutput(text)
  const shown = truncated && !showAll ? head : text
  return (
    <div className="ml-6 mt-1 p-2 rounded bg-surface-raised border border-border text-xs text-fg-muted font-mono whitespace-pre-wrap break-words max-h-96 overflow-y-auto">
      {shown}
      {truncated && (
        <button
          type="button"
          className="block mt-1 font-sans text-accent-fg hover:text-accent-fg underline"
          onClick={() => setShowAll((v) => !v)}
        >
          {showAll ? 'Show less' : hiddenLines > 0 ? `Show more (${hiddenLines} more lines)` : 'Show more'}
        </button>
      )}
    </div>
  )
}

export default function AgentLogEntry({ row }: Props) {
  const { log, parsed } = row
  const result = row.result?.parsed
  // Auto-expand failures so they're visible without a click.
  const [expanded, setExpanded] = useState(() => result?.isError === true)

  const ts = formatTs(log.timestamp)

  if (parsed.kind === 'hidden') return null

  switch (parsed.kind) {
    case 'assistant_text':
      return (
        <div className="flex gap-2 py-2 px-3 rounded-lg bg-indigo-950/40 border border-indigo-800/30 my-1">
          <span className="text-accent-fg shrink-0 mt-1"><Icon icon={MessageSquare} size="md" /></span>
          <div className="flex-1 min-w-0 text-xs text-indigo-200">
            <ReactMarkdown
              components={{
                p: ({ children }) => <p className="my-1 leading-relaxed">{children}</p>,
                ul: ({ children }) => <ul className="my-1 pl-4 list-disc space-y-0.5">{children}</ul>,
                ol: ({ children }) => <ol className="my-1 pl-4 list-decimal space-y-0.5">{children}</ol>,
                li: ({ children }) => <li className="leading-relaxed">{children}</li>,
                code: ({ children, className }) =>
                  className ? (
                    // fenced code block (inside <pre>)
                    <code className="text-accent-fg text-xs">{children}</code>
                  ) : (
                    // inline code
                    <code className="bg-indigo-950/60 text-accent-fg px-1 py-0.5 rounded text-xs font-mono">{children}</code>
                  ),
                pre: ({ children }) => (
                  <pre className="bg-surface-raised border border-border rounded p-2 my-1 overflow-x-auto text-xs font-mono text-fg-muted whitespace-pre-wrap break-words">{children}</pre>
                ),
                strong: ({ children }) => <strong className="text-indigo-100 font-semibold">{children}</strong>,
                em: ({ children }) => <em className="text-accent-fg italic">{children}</em>,
                h1: ({ children }) => <h1 className="text-sm font-semibold text-indigo-100 mt-2 mb-1">{children}</h1>,
                h2: ({ children }) => <h2 className="text-xs font-semibold text-indigo-100 mt-2 mb-1">{children}</h2>,
                h3: ({ children }) => <h3 className="text-xs font-semibold text-indigo-200 mt-1 mb-0.5">{children}</h3>,
                blockquote: ({ children }) => (
                  <blockquote className="border-l-2 border-indigo-700 pl-2 my-1 text-accent-fg italic">{children}</blockquote>
                ),
                a: ({ href, children }) => (
                  <a href={href} className="text-accent-fg underline hover:text-indigo-200" target="_blank" rel="noopener noreferrer">{children}</a>
                ),
              }}
            >
              {parsed.text}
            </ReactMarkdown>
          </div>
          <Timestamp ts={ts} />
        </div>
      )

    // One row per tool call: the call is the row, its output lives behind the
    // disclosure. The chip states the outcome without quoting the output.
    case 'tool_call': {
      const view = result ? resultView(result) : null
      const body = view?.body
      const canExpand = !!body
      const isErr = view?.isError === true

      const rail = isErr
        ? 'border-l-red-500'
        : row.pending
          ? 'border-l-yellow-500'
          : view
            ? 'border-l-teal-500'
            : 'border-l-slate-700'

      return (
        <div className="my-0.5" data-testid="tool-row">
          <button
            type="button"
            aria-expanded={canExpand ? expanded : undefined}
            disabled={!canExpand}
            className={`w-full flex gap-2 py-1.5 pr-3 pl-2.5 rounded-lg text-left items-start border border-l-2 transition-colors ${rail} ${
              isErr
                ? 'bg-red-950/30 border-red-800/30 hover:bg-red-950/50'
                : 'bg-cyan-950/30 border-cyan-800/20 enabled:hover:bg-cyan-950/50'
            }`}
            onClick={() => canExpand && setExpanded(!expanded)}
          >
            <span className="text-cyan-500 shrink-0 text-sm mt-px"><Icon icon={toolIcon(parsed.toolName)} size="md" /></span>
            {/* Clamped on the wrapper, not the args span, so the tool name and a
                short command share a line instead of stacking. */}
            <div className={`flex-1 min-w-0 ${expanded ? '' : 'line-clamp-2'}`}>
              <span className="text-cyan-300 font-semibold text-xs">{parsed.toolName}</span>
              {parsed.summary && (
                <span className="text-fg-muted text-xs ml-2 font-mono break-words" title={parsed.summary}>
                  {parsed.summary}
                </span>
              )}
            </div>
            <div className="flex items-center gap-2 shrink-0">
              {row.pending ? (
                <span className="text-yellow-300/90 bg-yellow-500/10 border border-yellow-500/30 rounded-full px-1.5 text-[10px] animate-pulse">
                  running
                </span>
              ) : view ? (
                <span
                  className={`rounded-full px-1.5 text-[10px] border max-w-[9rem] truncate ${
                    isErr
                      ? 'text-danger bg-red-500/10 border-red-500/30'
                      : 'text-teal-300 bg-teal-500/10 border-teal-500/25'
                  }`}
                  title={view.chip}
                >
                  {view.chip}
                </span>
              ) : null}
              <Timestamp ts={ts} />
              {canExpand && <span className="text-fg-subtle"><Icon icon={expanded ? ChevronUp : ChevronDown} size="sm" /></span>}
            </div>
          </button>
          {expanded && body && <OutputBlock text={body} />}
        </div>
      )
    }

    // A result whose call isn't in view (paged off the top, or never logged).
    case 'tool_result': {
      const view = resultView(parsed)
      const canExpand = !!view.body
      return (
        <div className="my-0.5 ml-5" data-testid="orphan-result">
          <button
            type="button"
            aria-expanded={canExpand ? expanded : undefined}
            disabled={!canExpand}
            className={`w-full flex gap-2 py-1 px-3 rounded-lg text-left transition-colors ${
              view.isError
                ? 'bg-red-950/30 border border-red-800/30 enabled:hover:bg-red-950/50'
                : 'bg-emerald-950/20 border border-emerald-800/20 enabled:hover:bg-emerald-950/40'
            }`}
            onClick={() => canExpand && setExpanded(!expanded)}
          >
            <span className={`shrink-0 text-xs mt-px ${view.isError ? 'text-danger' : 'text-emerald-600'}`}>
              <Icon icon={view.isError ? CircleX : CornerDownRight} size="sm" />
            </span>
            <div className="flex-1 min-w-0">
              {parsed.toolName && (
                <span className="text-emerald-700 font-semibold text-xs mr-1.5">{parsed.toolName}</span>
              )}
              <span className={`text-xs break-words ${view.isError ? 'text-danger' : 'text-fg-muted'}`}>
                {view.chip}
              </span>
            </div>
            <div className="flex items-center gap-2 shrink-0">
              <Timestamp ts={ts} />
              {canExpand && <span className="text-fg-subtle"><Icon icon={expanded ? ChevronUp : ChevronDown} size="sm" /></span>}
            </div>
          </button>
          {expanded && view.body && <OutputBlock text={view.body} />}
        </div>
      )
    }

    case 'system_event': {
      const sev = SEVERITY_STYLE[parsed.severity]
      return (
        <div
          data-testid="system-event"
          data-severity={parsed.severity}
          className={`flex gap-2 py-1 px-3 my-0.5 ${sev.row}`}
        >
          <span className={`${sev.icon} shrink-0`}><Icon icon={sev.glyph} size="sm" /></span>
          <span className={`${sev.text} text-xs`}>{parsed.event}</span>
          {parsed.detail && (
            <span className="text-fg-subtle text-xs ml-1 truncate">{parsed.detail}</span>
          )}
          <Timestamp ts={ts} />
        </div>
      )
    }

    case 'text': {
      const isError = log.type === 'stderr'
      return (
        <div className={`flex gap-2 py-0.5 px-3 my-0.5 border-l-2 ${isError ? 'border-red-800/60' : 'border-border/40'}`}>
          <span
            className={`text-xs font-mono whitespace-pre-wrap break-words leading-relaxed flex-1 ${
              isError ? 'text-danger' : 'text-fg-muted'
            }`}
          >
            {parsed.text}
          </span>
          <span className="ml-auto shrink-0 self-start"><Timestamp ts={ts} /></span>
        </div>
      )
    }

    case 'raw':
    default:
      return (
        <div className="my-0.5">
          <button
            className="w-full flex gap-2 py-0.5 px-3 text-left hover:bg-surface-sunken/30 rounded transition-colors"
            onClick={() => setExpanded(!expanded)}
          >
            <span className="text-slate-700 shrink-0 text-xs select-none font-mono">[{log.type}]</span>
            <span className="text-fg-subtle text-xs truncate flex-1">
              {(parsed.kind === 'raw' ? parsed.text : log.content).slice(0, 80)}
            </span>
            <span className="text-slate-700 shrink-0"><Icon icon={expanded ? ChevronUp : ChevronDown} size="sm" /></span>
            <Timestamp ts={ts} />
          </button>
          {expanded && (
            <div className="ml-3 mt-1 p-2 rounded bg-surface-raised border border-border text-xs text-fg-subtle font-mono whitespace-pre-wrap break-words max-h-64 overflow-y-auto">
              {parsed.kind === 'raw' ? parsed.text : log.content}
            </div>
          )}
        </div>
      )
  }
}
