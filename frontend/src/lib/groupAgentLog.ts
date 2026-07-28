/**
 * Groups a flat agent log stream into display rows.
 *
 * The agent emits a tool call and its result as two separate log entries. Shown
 * as two rows, the result row had to carry a truncated preview of the output —
 * which the expanded detail block then repeated in full. Folding the result into
 * its call gives one row per tool call, whose disclosure holds the output once.
 */

import type { AgentLog } from '../api/client'
import { parseLogContent, type ParsedLog } from './parseAgentLog'

type ToolResult = Extract<ParsedLog, { kind: 'tool_result' }>

export interface LogRow {
  /** Stable React key — the id of the log entry that opens the row. */
  key: string
  log: AgentLog
  parsed: ParsedLog
  /** The result folded into this row. Only ever set on a tool_call row. */
  result?: { log: AgentLog; parsed: ToolResult }
  /** Tool call still in flight: no result arrived and the run is live. */
  pending?: boolean
}

/** A result short enough to show inline — displaying it hides nothing. */
const INLINE_MAX = 80

export interface ResultView {
  /** Outcome shown on the collapsed row. Never a truncated slice of the body. */
  chip: string
  isError: boolean
  /** Full output, revealed by the disclosure. Absent when the chip says it all. */
  body?: string
}

/**
 * Splits a result into the bit that goes on the collapsed row and the bit behind
 * the disclosure. The two never overlap — that overlap was the whole problem.
 */
export function resultView(parsed: ToolResult): ResultView {
  const raw = parsed.text ?? parsed.detail ?? parsed.summary ?? ''
  const text = raw.replace(/\s+$/, '')
  const trimmed = text.trim()

  if (!trimmed) {
    return { chip: parsed.isError ? 'error' : 'no output', isError: parsed.isError }
  }
  if (parsed.isError) {
    return { chip: 'error', isError: true, body: text }
  }

  const lines = trimmed.split('\n')
  if (parsed.lineCount != null) {
    return { chip: `${parsed.lineCount} lines`, isError: false, body: text }
  }
  if (lines.length > 1) {
    return { chip: `${lines.length} lines`, isError: false, body: text }
  }
  // Single short line: it *is* the summary, so show it and hide nothing.
  if (trimmed.length <= INLINE_MAX) {
    return { chip: trimmed, isError: false }
  }
  return { chip: 'ok', isError: false, body: text }
}

/**
 * Builds the display rows for a run. `isRunning` marks a trailing unanswered
 * tool call as in-flight rather than as one whose result never arrived.
 */
export function groupLogRows(logs: AgentLog[], debug: boolean, isRunning: boolean): LogRow[] {
  const rows: LogRow[] = []
  // tool_use_id -> row awaiting its result. Results don't always land directly
  // after their call (interleaved system events, parallel tool calls), so the
  // id is the reliable link; the adjacency fallback below covers providers that
  // don't emit one.
  const awaiting = new Map<string, LogRow>()

  for (const log of logs) {
    const parsed = parseLogContent(log.type, log.content, debug)
    if (parsed.kind === 'hidden') continue

    if (parsed.kind === 'tool_result') {
      const target = parsed.toolUseId ? awaiting.get(parsed.toolUseId) : undefined
      const prev = rows[rows.length - 1]
      const fallback =
        !target && !parsed.toolUseId && prev?.parsed.kind === 'tool_call' && !prev.result
          ? prev
          : undefined
      const row = target ?? fallback
      if (row) {
        row.result = { log, parsed }
        if (parsed.toolUseId) awaiting.delete(parsed.toolUseId)
        continue
      }
      // Orphan result (its call scrolled off the top, or was never logged) —
      // keep it as its own row rather than dropping output on the floor.
      rows.push({ key: log.id, log, parsed })
      continue
    }

    const row: LogRow = { key: log.id, log, parsed }
    rows.push(row)
    if (parsed.kind === 'tool_call' && parsed.toolUseId) {
      awaiting.set(parsed.toolUseId, row)
    }
  }

  const last = rows[rows.length - 1]
  if (isRunning && last?.parsed.kind === 'tool_call' && !last.result) {
    last.pending = true
  }
  return rows
}
