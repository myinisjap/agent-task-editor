/**
 * Pure logic behind the Logs-tab run timeline: classifying display rows into
 * step kinds, grouping consecutive rows, and deriving the summary card. No
 * React in here so it is easy to test against fixture logs.
 */

import type { AgentConfig, AgentRun, ProviderConfig, TaskLabelHistoryEntry } from '../api/client'
import { escalationInfo } from './escalation'
import type { LogRow } from './groupAgentLog'

export type StepKind = 'read' | 'edit' | 'command' | 'test' | 'message' | 'human' | 'error' | 'system' | 'tool'

const READ_TOOLS = new Set(['Read', 'Grep', 'Glob', 'LS', 'NotebookRead', 'WebFetch', 'WebSearch'])
const SEARCH_TOOLS = new Set(['Grep', 'Glob', 'WebSearch'])
const EDIT_TOOLS = new Set(['Edit', 'Write', 'MultiEdit', 'NotebookEdit', 'str_replace_based_edit_tool'])

const TEST_RE =
  /(?:^|[\s;&|(])(?:(?:npm|pnpm|yarn|bun)\s+(?:run\s+)?test|npx\s+vitest|vitest|jest|go\s+test|pytest|cargo\s+test|playwright\s+test|make\s+test|mvn\s+test|gradle\s+test)(?=$|[\s;&|)])/

export function isTestCommand(cmd: string): boolean {
  return TEST_RE.test(cmd)
}

function toolName(row: LogRow): string | null {
  return row.parsed.kind === 'tool_call' ? row.parsed.toolName : null
}

function bashCommand(row: LogRow): string {
  if (row.parsed.kind !== 'tool_call') return ''
  const c = row.parsed.input.command
  return typeof c === 'string' ? c : ''
}

export function classifyRow(row: LogRow): StepKind {
  const p = row.parsed
  switch (p.kind) {
    case 'tool_call': {
      const name = p.toolName
      if (name.endsWith('request_human')) return 'human'
      if (READ_TOOLS.has(name)) return 'read'
      if (EDIT_TOOLS.has(name)) return 'edit'
      if (name === 'Bash') return isTestCommand(bashCommand(row)) ? 'test' : 'command'
      return 'tool'
    }
    case 'assistant_text':
    case 'text':
      return 'message'
    case 'system_event':
      if (p.severity === 'error') return 'error'
      if (p.event.startsWith('Human reply:')) return 'human'
      return 'system'
    case 'tool_result':
      return p.isError ? 'error' : 'system'
    default:
      return 'system'
  }
}

export interface CommandOutcome {
  state: 'pending' | 'passed' | 'failed' | 'unknown'
  exitCode?: number
}

export function commandOutcome(row: LogRow): CommandOutcome {
  if (row.pending) return { state: 'pending' }
  const res = row.result?.parsed
  if (!res) return { state: 'unknown' }
  if (res.isError) {
    const m = (res.text ?? '').match(/^Exit code (\d+)/m)
    return m ? { state: 'failed', exitCode: Number(m[1]) } : { state: 'failed' }
  }
  return { state: 'passed', exitCode: 0 }
}

function shortPath(p: string): string {
  return p.replace(/\\/g, '/').split('/').filter(Boolean).slice(-3).join('/')
}

export function editPath(row: LogRow): string | null {
  if (row.parsed.kind !== 'tool_call') return null
  const i = row.parsed.input
  const p = i.file_path ?? i.path ?? i.notebook_path
  return typeof p === 'string' && p ? p : null
}

function firstLine(s: string | undefined | null, max = 120): string {
  const line = (s ?? '').trim().split('\n')[0] ?? ''
  return line.length > max ? line.slice(0, max) + '…' : line
}

export interface StepGroup {
  key: string
  kind: StepKind
  rows: LogRow[]
  summary: string
  hasError: boolean
  pending: boolean
}

const MERGEABLE = new Set<StepKind>(['read', 'edit', 'command', 'system', 'tool'])

function listNames(names: string[]): string {
  const uniq = [...new Set(names)]
  const shown = uniq.slice(0, 3).join(', ')
  return uniq.length > 3 ? `${shown} +${uniq.length - 3} more` : shown
}

function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? '' : 's'}`
}

function rowText(row: LogRow): string {
  const p = row.parsed
  switch (p.kind) {
    case 'assistant_text':
    case 'text':
    case 'raw':
      return p.text
    case 'system_event':
      return p.event
    case 'tool_result':
      return p.text ?? p.summary
    case 'tool_call':
      return p.summary
    default:
      return ''
  }
}

function summarize(kind: StepKind, rows: LogRow[]): string {
  const first = rows[0]
  switch (kind) {
    case 'read': {
      const names = rows.map((r) => {
        const p = editPath(r)
        if (p) return shortPath(p)
        if (r.parsed.kind !== 'tool_call') return ''
        const pat = r.parsed.input.pattern ?? r.parsed.input.query ?? r.parsed.input.url
        return typeof pat === 'string' ? pat : r.parsed.toolName
      }).filter(Boolean)
      const searchOnly = rows.every((r) => SEARCH_TOOLS.has(toolName(r) ?? ''))
      const verb = searchOnly ? 'Searched' : 'Read'
      const noun = searchOnly ? 'pattern' : 'file'
      return `${verb} ${plural(rows.length, noun)}: ${listNames(names)}`
    }
    case 'edit': {
      const paths = rows.map((r) => editPath(r)).filter((p): p is string => !!p).map(shortPath)
      const n = new Set(paths).size || rows.length
      return `Edited ${plural(n, 'file')}: ${listNames(paths)}`
    }
    case 'command': {
      if (rows.length === 1) {
        const o = commandOutcome(first)
        const cmd = firstLine(bashCommand(first), 80)
        const tail = o.state === 'failed' ? ` — ${o.exitCode != null ? `exit ${o.exitCode}` : 'failed'}`
          : o.state === 'passed' ? ' — exit 0' : o.state === 'pending' ? ' — running…' : ''
        return `Ran \`${cmd}\`${tail}`
      }
      const failed = rows.filter((r) => commandOutcome(r).state === 'failed').length
      return `Ran ${plural(rows.length, 'command')}${failed ? ` (${failed} failed)` : ''}`
    }
    case 'test': {
      const o = commandOutcome(first)
      const cmd = firstLine(bashCommand(first), 80)
      const tail = o.state === 'passed' ? 'passed'
        : o.state === 'failed' ? `failed${o.exitCode != null ? ` (exit ${o.exitCode})` : ''}`
          : o.state === 'pending' ? 'running…' : 'no result'
      return `Tests: \`${cmd}\` — ${tail}`
    }
    case 'message':
      return firstLine(rowText(first))
    case 'human': {
      if (first.parsed.kind === 'tool_call') {
        const i = first.parsed.input
        const q = i.message ?? i.question ?? i.text
        return `Asked you: ${firstLine(typeof q === 'string' ? q : first.parsed.summary)}`
      }
      const t = rowText(first).replace(/^Human reply:\s*/, '')
      return `You replied: ${firstLine(t)}`
    }
    case 'error':
      return firstLine(rowText(first), 200)
    case 'system':
      return rows.length === 1 ? firstLine(rowText(first), 200) : `${rows.length} system events`
    case 'tool': {
      const names = rows.map((r) => toolName(r) ?? '').filter(Boolean)
      return `Used ${listNames(names.map((n) => stripMcpPrefix(n)))}`
    }
  }
}

function groupHasError(rows: LogRow[]): boolean {
  return rows.some((r) => r.result?.parsed.isError === true || classifyRow(r) === 'error')
}

function finish(kind: StepKind, rows: LogRow[]): StepGroup {
  return {
    key: rows[0].key,
    kind,
    rows,
    summary: summarize(kind, rows),
    hasError: kind === 'error' || groupHasError(rows),
    pending: rows.some((r) => r.pending),
  }
}

export function groupSteps(rows: LogRow[]): StepGroup[] {
  const out: StepGroup[] = []
  let kind: StepKind | null = null
  let cur: LogRow[] = []
  const flush = () => {
    if (kind && cur.length) out.push(finish(kind, cur))
    cur = []
    kind = null
  }
  for (const row of rows) {
    const k = classifyRow(row)
    if (kind === k && MERGEABLE.has(k)) {
      cur.push(row)
      continue
    }
    flush()
    kind = k
    cur = [row]
  }
  flush()
  return out
}

/** Mirrors backend reviewbrief.CountRework; `upTo` limits to entries up to that time. */
export function countReworkLoops(history: TaskLabelHistoryEntry[], upTo?: string): number {
  const limit = upTo ? Date.parse(upTo) : NaN
  const visited = new Set<string>()
  let n = 0
  const sorted = [...history].sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at))
  for (const e of sorted) {
    if (!Number.isNaN(limit) && Date.parse(e.created_at) > limit) continue
    if (visited.has(e.to_label)) n++
    visited.add(e.to_label)
  }
  return n
}

function truncateStr(s: string, max: number): string {
  return s.length > max ? s.slice(0, max) + '…' : s
}

export function currentStep(rows: LogRow[], isRunning: boolean): string | null {
  if (!isRunning) return null
  const last = rows[rows.length - 1]
  if (!last) return 'Starting…'
  const p = last.parsed
  if (p.kind === 'tool_call') {
    const i = p.input
    const path = editPath(last)
    if (EDIT_TOOLS.has(p.toolName)) return `Editing ${path ?? ''}`.trim()
    if (p.toolName === 'Bash') return `Running ${truncateStr(firstLine(bashCommand(last), 200), 60)}`
    if (p.toolName === 'Read') return `Reading ${path ?? ''}`.trim()
    if (p.toolName === 'Grep' || p.toolName === 'Glob') {
      return `Searching ${typeof i.pattern === 'string' ? i.pattern : ''}`.trim()
    }
    return `Using ${p.toolName}`
  }
  if (p.kind === 'assistant_text') {
    const words = p.text.trim().split(/\s+/).slice(0, 8).join(' ')
    return words ? truncateStr(words, 60) : 'Thinking…'
  }
  return 'Thinking…'
}

export interface NextStep {
  text: string
  action: 'reply' | 'retry' | 'fix' | 'diff' | null
  fix?: { to: string; text: string }
}

export interface RunSummary {
  outcome: {
    tone: 'running' | 'success' | 'failed' | 'waiting' | 'cancelled' | 'interrupted' | 'pending'
    text: string
    why?: string
  }
  agentName?: string
  providerModel?: string
  durationSecs: number | null
  costUsd: number | null
  costUnknown: boolean
  filesChanged: string[]
  commands: { total: number; failed: number }
  tests: { total: number; passed: number; failed: number; running: number }
  reworkLoop: number | null
  currentStep: string | null
  nextStep: NextStep | null
}

export interface DeriveInput {
  run: AgentRun
  rows: LogRow[]
  agentConfigs?: AgentConfig[]
  providerConfigs?: ProviderConfig[]
  labelHistory?: TaskLabelHistoryEntry[]
  now?: number
}

function lastErrorText(rows: LogRow[]): string | undefined {
  for (let i = rows.length - 1; i >= 0; i--) {
    const p = rows[i].parsed
    if (p.kind === 'system_event' && p.severity === 'error') return p.event
  }
  return undefined
}

export function deriveRunSummary(input: DeriveInput): RunSummary {
  const { run, rows } = input
  const now = input.now ?? Date.now()
  const agentConfigs = input.agentConfigs ?? []
  const providerConfigs = input.providerConfigs ?? []

  const agent = agentConfigs.find((a) => a.id === run.agent_config_id)
  const pc =
    providerConfigs.find((p) => p.id === run.provider_config_id) ??
    (run.provider_config_id === agent?.provider_config_id ? agent?.provider_config : undefined)

  const files: string[] = []
  const seenFiles = new Set<string>()
  const commands = { total: 0, failed: 0 }
  const tests = { total: 0, passed: 0, failed: 0, running: 0 }
  for (const r of rows) {
    const k = classifyRow(r)
    if (k === 'edit') {
      const p = editPath(r)
      if (p && !seenFiles.has(p)) {
        seenFiles.add(p)
        files.push(p)
      }
    } else if (k === 'command' || k === 'test') {
      const o = commandOutcome(r)
      if (k === 'command') {
        commands.total++
        if (o.state === 'failed') commands.failed++
      } else {
        tests.total++
        if (o.state === 'failed') tests.failed++
        else if (o.state === 'passed') tests.passed++
        else if (o.state === 'pending') tests.running++
      }
    }
  }

  let outcome: RunSummary['outcome']
  let nextStep: NextStep | null = null
  const errText = () => lastErrorText(rows) ?? (firstLine(run.notes) || undefined)
  switch (run.status) {
    case 'running':
      outcome = { tone: 'running', text: 'Running' }
      break
    case 'completed':
      outcome = { tone: 'success', text: 'Finished successfully' }
      nextStep = { text: 'Review the changes in the Diff tab.', action: 'diff' }
      break
    case 'failed':
      outcome = { tone: 'failed', text: 'Failed', why: errText() }
      nextStep = { text: 'Check the error above, then retry.', action: 'retry' }
      break
    case 'waiting_human': {
      const info = escalationInfo(run.escalation_reason)
      let why = info.explanation
      if (run.escalation_reason === 'agent_question') {
        const fb = firstLine(run.feedback)
        if (fb) why = `${why} ${fb}`
      }
      outcome = { tone: 'waiting', text: info.label, why }
      nextStep = { text: info.nextStep, action: info.action, fix: info.fix }
      break
    }
    case 'cancelled':
      outcome = { tone: 'cancelled', text: 'Stopped' }
      nextStep = { text: 'Run was stopped. Retry to run it again.', action: 'retry' }
      break
    case 'interrupted':
      outcome = { tone: 'interrupted', text: 'Interrupted (server restarted)', why: errText() }
      nextStep = { text: 'Check the error above, then retry.', action: 'retry' }
      break
    default:
      outcome = { tone: 'pending', text: 'Queued' }
  }

  let durationSecs: number | null = null
  if (run.started_at) {
    const start = Date.parse(run.started_at)
    const end = run.status === 'running' ? now : run.completed_at ? Date.parse(run.completed_at) : NaN
    if (!Number.isNaN(start) && !Number.isNaN(end)) durationSecs = Math.max(0, Math.round((end - start) / 1000))
  }

  const loops = countReworkLoops(input.labelHistory ?? [], run.created_at)

  return {
    outcome,
    agentName: agent?.name,
    providerModel: pc ? pc.model || pc.name : undefined,
    durationSecs,
    costUsd: run.cost_usd ?? null,
    costUnknown: !!run.cost_unknown,
    filesChanged: files,
    commands,
    tests,
    reworkLoop: loops > 0 ? loops : null,
    currentStep: currentStep(rows, run.status === 'running'),
    nextStep,
  }
}

export function truncateOutput(
  text: string,
  maxLines = 20,
  maxChars = 2000,
): { head: string; truncated: boolean; hiddenLines: number } {
  const lines = text.split('\n')
  if (lines.length <= maxLines && text.length <= maxChars) return { head: text, truncated: false, hiddenLines: 0 }
  let head = lines.slice(0, maxLines).join('\n')
  if (head.length > maxChars) head = head.slice(0, maxChars)
  const shown = head.split('\n').length
  return { head, truncated: true, hiddenLines: Math.max(0, lines.length - shown) }
}

/** "mcp__server__tool" -> "tool" (string ops, no regex backtracking). */
function stripMcpPrefix(name: string): string {
  if (!name.startsWith('mcp__')) return name
  const i = name.indexOf('__', 5)
  return i < 0 ? name : name.slice(i + 2)
}
