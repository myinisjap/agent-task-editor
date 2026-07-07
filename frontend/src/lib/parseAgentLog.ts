/**
 * Parses raw agent log content into a structured, human-readable form.
 *
 * Log content arrives as either plain text or JSON blobs (Claude Code SDK format).
 * This module extracts the meaningful parts and produces a summary + optional detail.
 */

export type ParsedLog =
  | { kind: 'text'; text: string }
  | { kind: 'tool_call'; toolName: string; input: Record<string, unknown>; summary: string }
  | { kind: 'tool_result'; toolName?: string; summary: string; isError: boolean; detail?: string; lineCount?: number }
  | { kind: 'assistant_text'; text: string }
  | { kind: 'system_event'; event: string; detail?: string }
  | { kind: 'hidden' }  // ponytail: debug noise (thinking_tokens etc), show when debug flag added
  | { kind: 'raw'; text: string }

const HIDDEN_SUBTYPES = new Set(['thinking_tokens', 'thinking'])

/** Try to parse JSON, return null on failure */

function tryJson(s: string): unknown {
  const t = s?.trimStart()
  if (!t || t[0] !== '{' && t[0] !== '[') return null
  try { return JSON.parse(t) } catch { /* fall through */ }
  // Retry after escaping literal control chars inside JSON string values.
  // The claude CLI sometimes emits literal \t/\n/ANSI inside string values.
  // ponytail: state-machine walk; handles \" escapes correctly
  try {
    let out = ''
    let inStr = false
    let i = 0
    while (i < t.length) {
      const ch = t[i]
      if (inStr) {
        if (ch === '\\') { out += ch + (t[i + 1] ?? ''); i += 2; continue }
        if (ch === '"') { inStr = false; out += ch; i++; continue }
        // Literal control char inside a string — escape it
        const code = ch.charCodeAt(0)
        if (code < 0x20) {
          if (code === 0x09) out += '\\t'
          else if (code === 0x0a) out += '\\n'
          else if (code === 0x0d) out += '\\r'
          // strip ANSI: \x1b[ ... letter
          else if (code === 0x1b && t[i + 1] === '[') {
            i += 2
            while (i < t.length && !/[a-zA-Z]/.test(t[i])) i++
            i++ // skip the terminating letter
            continue
          }
          // other control chars: drop
          i++; continue
        }
        out += ch
      } else {
        if (ch === '"') inStr = true
        out += ch
      }
      i++
    }
    return JSON.parse(out)
  } catch { return null }
}

/** Shorten a file path to last 3 segments */
function shortPath(p: string): string {
  const parts = p.replace(/\\/g, '/').split('/')
  return parts.slice(-3).join('/')
}

/** Truncate a string to a max length, appending an ellipsis if shortened */
function truncate(s: string, max: number): string {
  return s.length > max ? s.slice(0, max) + '…' : s
}

/** Summarise a tool input object for display */
function summariseInput(toolName: string, input: Record<string, unknown>): string {
  if (!input || typeof input !== 'object') return ''

  switch (toolName) {
    case 'Read':
    case 'str_replace_based_edit_tool':
    case 'Edit':
    case 'Write': {
      const path = (input.file_path ?? input.path ?? '') as string
      return path ? shortPath(path) : ''
    }
    case 'Bash': {
      const cmd = (input.command ?? '') as string
      return cmd.length > 120 ? cmd.slice(0, 120) + '…' : cmd
    }
    case 'Grep': {
      const pattern = (input.pattern ?? '') as string
      const path = (input.path ?? input.glob ?? '') as string
      return path ? `"${pattern}" in ${shortPath(path)}` : `"${pattern}"`
    }
    case 'Glob': {
      const pattern = (input.pattern ?? '') as string
      return pattern
    }
    case 'TodoWrite':
    case 'Task':
    case 'TaskCreate': {
      const title = (input.title ?? input.task ?? '') as string
      return title.length > 80 ? title.slice(0, 80) + '…' : title
    }
    case 'WebFetch':
    case 'WebSearch': {
      const url = (input.url ?? input.query ?? '') as string
      return url.length > 120 ? url.slice(0, 120) + '…' : url
    }
    default: {
      // Generic: show first string-valued key
      for (const [k, v] of Object.entries(input)) {
        if (typeof v === 'string' && v.length > 0 && k !== 'id') {
          const display = v.length > 120 ? v.slice(0, 120) + '…' : v
          return `${k}: ${display}`
        }
      }
      return ''
    }
  }
}

/** Summarise tool result text for display */
function summariseResult(_toolName: string | undefined, text: string): { summary: string; detail?: string } {
  const lines = text.trim().split('\n')
  const lineCount = lines.length

  // For multi-line results, show first meaningful line + count
  if (lineCount > 3) {
    const first = lines[0].trim()
    const preview = first.length > 120 ? first.slice(0, 120) + '…' : first
    return {
      summary: preview ? `${preview}  (+${lineCount - 1} lines)` : `${lineCount} lines`,
      detail: text,
    }
  }

  // Single / short result
  const flat = text.trim()
  if (flat.length > 200) {
    return { summary: flat.slice(0, 200) + '…', detail: text }
  }
  return { summary: flat || '(empty)' }
}

/** Parse a Claude Code SDK message event */
function parseMessage(obj: Record<string, unknown>): ParsedLog | null {
  const msg = obj.message as Record<string, unknown> | undefined
  if (!msg) return null

  const role = msg.role as string | undefined
  const content = msg.content

  // assistant message with text
  if (role === 'assistant' && Array.isArray(content)) {
    const texts: string[] = []
    for (const block of content as unknown[]) {
      if (typeof block === 'object' && block !== null) {
        const b = block as Record<string, unknown>
        if (b.type === 'text' && typeof b.text === 'string' && b.text.trim()) {
          texts.push(b.text.trim())
        }
      }
    }
    if (texts.length > 0) {
      return { kind: 'assistant_text', text: texts.join('\n') }
    }
  }

  // user message (tool results)
  if (role === 'user' && Array.isArray(content)) {
    for (const block of content as unknown[]) {
      if (typeof block !== 'object' || block === null) continue
      const b = block as Record<string, unknown>
      if (b.type !== 'tool_result') continue

      const c = b.content
      const isError = b.is_error === true
      let text = ''
      if (typeof c === 'string') {
        text = c
      } else if (Array.isArray(c)) {
        text = (c as unknown[]).map((x) => {
          if (typeof x === 'object' && x !== null && (x as Record<string, unknown>).type === 'text') {
            return (x as Record<string, unknown>).text as string
          }
          return ''
        }).filter(Boolean).join('\n')
      }

      // Resolve tool name from the tool_use block in the same message
      let toolName: string | undefined
      for (const sibling of content as unknown[]) {
        if (typeof sibling === 'object' && sibling !== null) {
          const s = sibling as Record<string, unknown>
          if (s.type === 'tool_use' && s.id === b.tool_use_id && typeof s.name === 'string') {
            toolName = s.name
          }
        }
      }

      // Check for file content in tool_use_result
      const tur = obj.tool_use_result as Record<string, unknown> | undefined
      if (tur?.type === 'text') {
        const file = tur.file as Record<string, unknown> | undefined
        if (file?.filePath) {
          return {
            kind: 'tool_result',
            toolName,
            summary: `${shortPath(file.filePath as string)} (${file.numLines ?? '?'} lines)`,
            isError: false,
            detail: text || undefined,
          }
        }
      }

      const { summary, detail } = summariseResult(toolName, text)
      return { kind: 'tool_result', toolName, summary, isError, detail }
    }
  }

  return null
}

/** Parse a Claude Code SDK tool_use block */
function parseToolUse(obj: Record<string, unknown>): ParsedLog | null {
  const toolUse = (obj.tool_use ?? obj.toolUse) as Record<string, unknown> | undefined
  if (!toolUse) return null
  const toolName = (toolUse.name ?? toolUse.tool_name ?? '') as string
  const input = (toolUse.input ?? {}) as Record<string, unknown>
  const summary = summariseInput(toolName, input)
  return { kind: 'tool_call', toolName, input, summary }
}

/** Parse a system/init event into a human-readable summary */
function parseSystemInit(obj: Record<string, unknown>): ParsedLog {
  const model = (obj.model ?? '') as string
  const tools = (Array.isArray(obj.tools) ? obj.tools : []) as string[]
  const mcpServers = (Array.isArray(obj.mcp_servers) ? obj.mcp_servers : []) as { name: string; status?: string }[]
  const sessionId = (obj.session_id ?? '') as string

  const parts: string[] = []
  if (model) parts.push(`model: ${model}`)
  if (tools.length > 0) parts.push(`tools: ${tools.join(', ')}`)
  if (mcpServers.length > 0) parts.push(`MCP: ${mcpServers.map((s) => s.name).join(', ')}`)

  const event = `Session started · ${parts.join(' · ')}`
  const detail = sessionId ? `session: ${sessionId}` : undefined
  return { kind: 'system_event', event, detail }
}

/** Parse a background task lifecycle event (task_started / task_notification) */
function parseTaskLifecycle(obj: Record<string, unknown>): ParsedLog | null {
  const subtype = obj.subtype as string | undefined

  if (subtype === 'task_started') {
    const taskType = (obj.task_type ?? '') as string
    const description = truncate(((obj.description ?? '') as string).trim(), 120)
    const parts = ['Background task started']
    if (taskType) parts.push(taskType)
    if (description) parts.push(description)
    return { kind: 'system_event', event: parts.join(' · ') }
  }

  if (subtype === 'task_notification') {
    const status = (obj.status ?? 'unknown') as string
    const summary = truncate(((obj.summary ?? '') as string).trim(), 120)
    const isFailure = status !== 'completed'
    const label = isFailure ? `Failed: Background task (${status})` : `Background task ${status}`
    const event = summary ? `${label}: ${summary}` : label
    return { kind: 'system_event', event }
  }

  return null
}

/** Parse a result/completion event */
function parseResult(obj: Record<string, unknown>): ParsedLog | null {
  if (obj.type !== 'result') return null
  const subtype = obj.subtype as string | undefined
  const isError = obj.is_error === true
  const result = (obj.result ?? '') as string
  const errorLabel = result.trim() ? result.trim().split('\n')[0].slice(0, 120) : (subtype ?? 'error')
  const event = isError ? `Failed: ${errorLabel}` : `Completed (${subtype ?? 'success'})`
  const detail = result.length > 200 ? result.slice(0, 200) + '…' : result || undefined
  return { kind: 'system_event', event, detail }
}

export function parseLogContent(type: string, content: string, debug: boolean = false): ParsedLog {
  // Plain text types — no JSON expected
  if (type === 'stdout' || type === 'stderr') {
    // stdout might be raw SDK JSON blobs (e.g. {"type":"user",...}) — try to parse them
    const obj = tryJson(content) as Record<string, unknown> | null
    if (obj) {
      // ponytail: hide all SDK system events (thinking_tokens, thinking, etc) — noise
      if (obj.type === 'system' && !(debug || !HIDDEN_SUBTYPES.has(obj.subtype as string))) return { kind: 'hidden' }
      if (obj.type === 'system' && obj.subtype === 'init') return parseSystemInit(obj)
      if (obj.type === 'system' && (obj.subtype === 'task_started' || obj.subtype === 'task_notification')) {
        const lifecycle = parseTaskLifecycle(obj)
        if (lifecycle) return lifecycle
      }
      const msg = parseMessage(obj)
      if (msg) return msg
      const toolUse = parseToolUse(obj)
      if (toolUse) return toolUse
      if (obj.type === 'result') {
        const r = parseResult(obj)
        if (r) return r
      }
    }
    return { kind: 'text', text: content }
  }

  if (type === 'system') {
    // Could be plain text or JSON event
    const obj = tryJson(content) as Record<string, unknown> | null
    if (obj) {
      // ponytail: hide all SDK system events — noise
      if (obj.type === 'system' && !(debug || !HIDDEN_SUBTYPES.has(obj.subtype as string))) return { kind: 'hidden' }
      if (obj.type === 'system' && obj.subtype === 'init') return parseSystemInit(obj)
      if (obj.type === 'system' && (obj.subtype === 'task_started' || obj.subtype === 'task_notification')) {
        const lifecycle = parseTaskLifecycle(obj)
        if (lifecycle) return lifecycle
      }
      if (obj.type === 'result') {
        const r = parseResult(obj)
        if (r) return r
      }
    }
    return { kind: 'system_event', event: content }
  }

  // tool_call and tool_result are typically JSON
  const obj = tryJson(content) as Record<string, unknown> | null

  if (!obj) {
    // Not JSON — just show as text
    return { kind: 'text', text: content }
  }

  // Filter internal SDK noise regardless of log type
  if (obj.type === 'system' && HIDDEN_SUBTYPES.has(obj.subtype as string) && !debug) {
    return { kind: 'hidden' }
  }

  // Background task lifecycle events (task_started / task_notification)
  if (obj.type === 'system' && (obj.subtype === 'task_started' || obj.subtype === 'task_notification')) {
    const lifecycle = parseTaskLifecycle(obj)
    if (lifecycle) return lifecycle
  }

  // Try tool_use extraction
  const toolUse = parseToolUse(obj)
  if (toolUse) return toolUse

  // Try message extraction (assistant text / tool results)
  const msg = parseMessage(obj)
  if (msg) return msg

  // Try result/completion
  if (obj.type === 'result') {
    const r = parseResult(obj)
    if (r) return r
  }

  // Direct tool_call shape: { name, input }
  if (typeof obj.name === 'string' && obj.input !== undefined) {
    const toolName = obj.name
    const input = obj.input as Record<string, unknown>
    const summary = summariseInput(toolName, input)
    return { kind: 'tool_call', toolName, input, summary }
  }

  // Fallback: show content but truncated
  const truncated = content.length > 300 ? content.slice(0, 300) + '…' : content
  return { kind: 'raw', text: truncated }
}
