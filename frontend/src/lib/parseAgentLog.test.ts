import { describe, it, expect } from 'vitest'
import capturedLogs from './__fixtures__/agent-run-logs.json'
import { parseLogContent } from './parseAgentLog'
import type { ParsedLog } from './parseAgentLog'

// The frontend receives persisted AgentLog rows ({ type, content }) from the API
// and renders each through parseLogContent. The fixture is a real captured run.
const logs = capturedLogs as { type: string; content: string }[]

describe('parseLogContent — captured run fixture', () => {
  it('parses each entry of a real run into the expected sequence of kinds', () => {
    const kinds = logs.map((l) => parseLogContent(l.type, l.content).kind)
    expect(kinds).toEqual([
      'system_event', // system init
      'text', // assistant plain-text stdout
      'tool_call', // Read (direct {name, input} shape)
      'tool_result', // Read result
      'tool_call', // Bash (nested {tool_use} shape)
      'tool_result', // Bash error
      'hidden', // thinking subtype
      'system_event', // result: completed
    ])
  })

  it('never throws on any captured entry', () => {
    for (const l of logs) {
      expect(() => parseLogContent(l.type, l.content)).not.toThrow()
    }
  })
})

describe('parseLogContent — system init', () => {
  it('summarises model, tools, and MCP servers', () => {
    const init = JSON.stringify({
      type: 'system',
      subtype: 'init',
      model: 'claude-opus-4',
      tools: ['Read', 'Bash'],
      mcp_servers: [{ name: 'ate', status: 'connected' }],
      session_id: 'sess-1',
    })
    const parsed = parseLogContent('system', init)
    expect(parsed.kind).toBe('system_event')
    if (parsed.kind !== 'system_event') return
    expect(parsed.event).toContain('model: claude-opus-4')
    expect(parsed.event).toContain('tools: Read, Bash')
    expect(parsed.event).toContain('MCP: ate')
    expect(parsed.detail).toBe('session: sess-1')
  })
})

describe('parseLogContent — hidden noise', () => {
  it('hides thinking subtypes by default', () => {
    const thinking = JSON.stringify({ type: 'system', subtype: 'thinking', thinking: 'noise' })
    expect(parseLogContent('system', thinking).kind).toBe('hidden')
    expect(parseLogContent('tool_call', thinking).kind).toBe('hidden')
  })

  it('reveals thinking subtypes when debug is enabled', () => {
    const thinking = JSON.stringify({ type: 'system', subtype: 'thinking', thinking: 'noise' })
    expect(parseLogContent('system', thinking, true).kind).not.toBe('hidden')
  })
})

describe('parseLogContent — assistant text', () => {
  it('treats non-JSON stdout as plain text', () => {
    const parsed = parseLogContent('stdout', 'just some plain text')
    expect(parsed).toEqual<ParsedLog>({ kind: 'text', text: 'just some plain text' })
  })

  it('extracts assistant text from a raw SDK JSON blob on stdout', () => {
    const blob = JSON.stringify({
      type: 'assistant',
      message: { role: 'assistant', content: [{ type: 'text', text: '  hello world  ' }] },
    })
    const parsed = parseLogContent('stdout', blob)
    expect(parsed).toEqual<ParsedLog>({ kind: 'assistant_text', text: 'hello world' })
  })
})

describe('parseLogContent — tool calls', () => {
  it('summarises a Read tool call to the last path segments', () => {
    const content = JSON.stringify({ type: 'tool_use', name: 'Read', input: { file_path: '/a/b/c/d/e.ts' } })
    const parsed = parseLogContent('tool_call', content)
    expect(parsed.kind).toBe('tool_call')
    if (parsed.kind !== 'tool_call') return
    expect(parsed.toolName).toBe('Read')
    expect(parsed.summary).toBe('c/d/e.ts')
  })

  it('truncates a long Bash command', () => {
    const cmd = 'echo ' + 'x'.repeat(200)
    const content = JSON.stringify({ tool_use: { name: 'Bash', input: { command: cmd } } })
    const parsed = parseLogContent('tool_call', content)
    expect(parsed.kind).toBe('tool_call')
    if (parsed.kind !== 'tool_call') return
    expect(parsed.summary.length).toBe(121) // 120 chars + ellipsis
    expect(parsed.summary.endsWith('…')).toBe(true)
  })

  it('formats a Grep call with pattern and path', () => {
    const content = JSON.stringify({ type: 'tool_use', name: 'Grep', input: { pattern: 'TODO', path: '/x/y/z.ts' } })
    const parsed = parseLogContent('tool_call', content)
    if (parsed.kind !== 'tool_call') throw new Error('expected tool_call')
    expect(parsed.summary).toBe('"TODO" in x/y/z.ts')
  })

  it('falls back to the first string field for unknown tools', () => {
    const content = JSON.stringify({ type: 'tool_use', name: 'MysteryTool', input: { id: 'skip-me', note: 'use-me' } })
    const parsed = parseLogContent('tool_call', content)
    if (parsed.kind !== 'tool_call') throw new Error('expected tool_call')
    expect(parsed.summary).toBe('note: use-me')
  })
})

describe('parseLogContent — tool results', () => {
  it('resolves the tool name from the sibling tool_use block', () => {
    const content = JSON.stringify({
      type: 'user',
      message: {
        role: 'user',
        content: [
          { type: 'tool_use', id: 'tid', name: 'Bash', input: {} },
          { type: 'tool_result', tool_use_id: 'tid', content: 'ok' },
        ],
      },
    })
    const parsed = parseLogContent('tool_result', content)
    expect(parsed.kind).toBe('tool_result')
    if (parsed.kind !== 'tool_result') return
    expect(parsed.toolName).toBe('Bash')
    expect(parsed.isError).toBe(false)
    expect(parsed.summary).toBe('ok')
  })

  it('flags error results and summarises multi-line output with a line count', () => {
    const content = JSON.stringify({
      type: 'user',
      message: {
        role: 'user',
        content: [
          { type: 'tool_result', tool_use_id: 'x', is_error: true, content: 'line1\nline2\nline3\nline4' },
        ],
      },
    })
    const parsed = parseLogContent('tool_result', content)
    if (parsed.kind !== 'tool_result') throw new Error('expected tool_result')
    expect(parsed.isError).toBe(true)
    expect(parsed.summary).toBe('line1  (+3 lines)')
    expect(parsed.detail).toContain('line4')
  })

  it('uses tool_use_result file metadata when present', () => {
    const content = JSON.stringify({
      type: 'user',
      tool_use_result: { type: 'text', file: { filePath: '/a/b/c/file.ts', numLines: 42 } },
      message: {
        role: 'user',
        content: [{ type: 'tool_result', tool_use_id: 'x', content: 'file body' }],
      },
    })
    const parsed = parseLogContent('tool_result', content)
    if (parsed.kind !== 'tool_result') throw new Error('expected tool_result')
    expect(parsed.summary).toBe('b/c/file.ts (42 lines)')
  })
})

describe('parseLogContent — result events', () => {
  it('reports a successful completion', () => {
    const content = JSON.stringify({ type: 'result', subtype: 'success', is_error: false, result: 'all done' })
    const parsed = parseLogContent('system', content)
    if (parsed.kind !== 'system_event') throw new Error('expected system_event')
    expect(parsed.event).toBe('Completed (success)')
  })

  it('reports a failure with the first line of the result as the label', () => {
    const content = JSON.stringify({ type: 'result', subtype: 'error', is_error: true, result: 'boom happened\nmore detail' })
    const parsed = parseLogContent('system', content)
    if (parsed.kind !== 'system_event') throw new Error('expected system_event')
    expect(parsed.event).toBe('Failed: boom happened')
  })
})

describe('parseLogContent — resilient JSON parsing', () => {
  it('parses JSON containing literal tabs/newlines and ANSI escapes inside strings', () => {
    // Raw control chars + an ANSI color sequence embedded in a string value —
    // the kind of thing the claude CLI sometimes emits unescaped.
    const messy = '{"type":"tool_use","name":"Bash","input":{"command":"echo\thi\x1b[31mred\x1b[0m"}}'
    const parsed = parseLogContent('tool_call', messy)
    expect(parsed.kind).toBe('tool_call')
    if (parsed.kind !== 'tool_call') return
    expect(parsed.input.command).toBe('echo\thired')
  })

  it('falls back to raw for JSON objects it does not understand', () => {
    const parsed = parseLogContent('tool_call', '{"unrecognized":"shape"}')
    expect(parsed.kind).toBe('raw')
  })

  it('falls back to text for non-JSON tool_call content', () => {
    const parsed = parseLogContent('tool_call', 'not json at all')
    expect(parsed).toEqual<ParsedLog>({ kind: 'text', text: 'not json at all' })
  })
})
