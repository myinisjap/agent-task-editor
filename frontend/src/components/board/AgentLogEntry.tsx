import { useState } from 'react'
import ReactMarkdown from 'react-markdown'
import type { AgentLog } from '../../api/client'
import { parseLogContent } from '../../lib/parseAgentLog'

interface Props {
  log: AgentLog
}

const TOOL_ICONS: Record<string, string> = {
  Read: '📄',
  Write: '✏️',
  Edit: '✏️',
  str_replace_based_edit_tool: '✏️',
  Bash: '⚡',
  Grep: '🔍',
  Glob: '🗂',
  WebFetch: '🌐',
  WebSearch: '🌐',
  Task: '📋',
  TaskCreate: '📋',
  TodoWrite: '✅',
  Agent: '🤖',
}

function toolIcon(name: string): string {
  return TOOL_ICONS[name] ?? '🔧'
}

function Timestamp({ ts }: { ts: string | null }) {
  if (!ts) return null
  return <span className="text-slate-600 text-xs shrink-0">{ts}</span>
}

export default function AgentLogEntry({ log }: Props) {
  const parsed = parseLogContent(log.type, log.content)
  // Auto-expand errors so failures are visible without a click
  const [expanded, setExpanded] = useState(() =>
    parsed.kind === 'tool_result' && parsed.isError
  )

  const ts = log.timestamp
    ? new Date(log.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    : null

  switch (parsed.kind) {
    case 'assistant_text':
      return (
        <div className="flex gap-2 py-2 px-3 rounded-lg bg-indigo-950/40 border border-indigo-800/30 my-1">
          <span className="text-indigo-400 shrink-0 mt-1">💬</span>
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
                    <code className="text-indigo-300 text-xs">{children}</code>
                  ) : (
                    // inline code
                    <code className="bg-indigo-950/60 text-indigo-300 px-1 py-0.5 rounded text-xs font-mono">{children}</code>
                  ),
                pre: ({ children }) => (
                  <pre className="bg-slate-900 border border-slate-700 rounded p-2 my-1 overflow-x-auto text-xs font-mono text-slate-300 whitespace-pre-wrap break-words">{children}</pre>
                ),
                strong: ({ children }) => <strong className="text-indigo-100 font-semibold">{children}</strong>,
                em: ({ children }) => <em className="text-indigo-300 italic">{children}</em>,
                h1: ({ children }) => <h1 className="text-sm font-semibold text-indigo-100 mt-2 mb-1">{children}</h1>,
                h2: ({ children }) => <h2 className="text-xs font-semibold text-indigo-100 mt-2 mb-1">{children}</h2>,
                h3: ({ children }) => <h3 className="text-xs font-semibold text-indigo-200 mt-1 mb-0.5">{children}</h3>,
                blockquote: ({ children }) => (
                  <blockquote className="border-l-2 border-indigo-700 pl-2 my-1 text-indigo-300 italic">{children}</blockquote>
                ),
                a: ({ href, children }) => (
                  <a href={href} className="text-indigo-400 underline hover:text-indigo-200" target="_blank" rel="noopener noreferrer">{children}</a>
                ),
              }}
            >
              {parsed.text}
            </ReactMarkdown>
          </div>
          <Timestamp ts={ts} />
        </div>
      )

    case 'tool_call': {
      return (
        <div className="flex gap-2 py-1.5 px-3 rounded-lg bg-cyan-950/30 border border-cyan-800/20 my-0.5 items-start">
          <span className="text-cyan-500 shrink-0 text-sm mt-px">{toolIcon(parsed.toolName)}</span>
          <div className="flex-1 min-w-0">
            <span className="text-cyan-300 font-semibold text-xs">{parsed.toolName}</span>
            {parsed.summary && (
              <span className="text-slate-400 text-xs ml-2 font-mono break-all" title={parsed.summary}>
                {parsed.summary}
              </span>
            )}
          </div>
          <Timestamp ts={ts} />
        </div>
      )
    }

    case 'tool_result': {
      const isErr = parsed.isError
      const canExpand = !!parsed.detail
      return (
        <div className="my-0.5 ml-5">
          <button
            className={`w-full flex gap-2 py-1 px-3 rounded-lg text-left transition-colors ${
              isErr
                ? 'bg-red-950/30 border border-red-800/30 hover:bg-red-950/50'
                : expanded
                  ? 'bg-emerald-950/50 border border-emerald-700/30'
                  : 'bg-emerald-950/20 border border-emerald-800/20 hover:bg-emerald-950/40'
            }`}
            onClick={() => canExpand && setExpanded(!expanded)}
          >
            <span className={`shrink-0 text-xs mt-px ${isErr ? 'text-red-400' : 'text-emerald-600'}`}>
              {isErr ? '✗' : '↳'}
            </span>
            <div className="flex-1 min-w-0">
              {parsed.toolName && (
                <span className="text-emerald-700 font-semibold text-xs mr-1.5">{parsed.toolName}</span>
              )}
              <span className={`text-xs break-words ${isErr ? 'text-red-400' : 'text-slate-400'}`}>
                {parsed.summary || '(empty result)'}
              </span>
            </div>
            <div className="flex items-center gap-2 shrink-0">
              <Timestamp ts={ts} />
              {canExpand && (
                <span className="text-slate-600 text-xs">{expanded ? '▲' : '▼'}</span>
              )}
            </div>
          </button>
          {expanded && parsed.detail && (
            <div className="ml-4 mt-1 p-2 rounded bg-slate-900 border border-slate-700 text-xs text-slate-300 font-mono whitespace-pre-wrap break-words max-h-96 overflow-y-auto">
              {parsed.detail}
            </div>
          )}
        </div>
      )
    }

    case 'system_event':
      return (
        <div className="flex gap-2 py-1 px-3 my-0.5">
          <span className="text-yellow-600 shrink-0 text-xs">◆</span>
          <span className="text-yellow-500 text-xs">{parsed.event}</span>
          {parsed.detail && (
            <span className="text-slate-500 text-xs ml-1 truncate">{parsed.detail}</span>
          )}
          <Timestamp ts={ts} />
        </div>
      )

    case 'text': {
      const isError = log.type === 'stderr'
      return (
        <div className={`flex gap-2 py-0.5 px-3 my-0.5 border-l-2 ${isError ? 'border-red-800/60' : 'border-slate-700/40'}`}>
          <span
            className={`text-xs font-mono whitespace-pre-wrap break-words leading-relaxed flex-1 ${
              isError ? 'text-red-400' : 'text-slate-400'
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
            className="w-full flex gap-2 py-0.5 px-3 text-left hover:bg-slate-800/30 rounded transition-colors"
            onClick={() => setExpanded(!expanded)}
          >
            <span className="text-slate-700 shrink-0 text-xs select-none font-mono">[{log.type}]</span>
            <span className="text-slate-600 text-xs truncate flex-1">
              {(parsed.kind === 'raw' ? parsed.text : log.content).slice(0, 80)}
            </span>
            <span className="text-slate-700 text-xs shrink-0">{expanded ? '▲' : '▼'}</span>
            <Timestamp ts={ts} />
          </button>
          {expanded && (
            <div className="ml-3 mt-1 p-2 rounded bg-slate-900 border border-slate-800 text-xs text-slate-500 font-mono whitespace-pre-wrap break-words max-h-64 overflow-y-auto">
              {parsed.kind === 'raw' ? parsed.text : log.content}
            </div>
          )}
        </div>
      )
  }
}
