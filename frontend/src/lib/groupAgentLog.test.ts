import { describe, it, expect } from 'vitest'
import type { AgentLog } from '../api/client'
import { groupLogRows, resultView } from './groupAgentLog'
import { parseLogContent } from './parseAgentLog'

let seq = 0
function log(type: AgentLog['type'], payload: unknown): AgentLog {
  seq += 1
  return {
    id: `log-${seq}`,
    agent_run_id: 'run-1',
    timestamp: new Date(1700000000000 + seq * 1000).toISOString(),
    type,
    content: typeof payload === 'string' ? payload : JSON.stringify(payload),
  }
}

function toolCall(name: string, input: Record<string, unknown>, id?: string): AgentLog {
  return log('tool_call', {
    type: 'assistant',
    message: { role: 'assistant', content: [{ type: 'tool_use', ...(id ? { id } : {}), name, input }] },
  })
}

function toolResult(content: string, id?: string, isError = false): AgentLog {
  return log('tool_result', {
    type: 'user',
    message: {
      role: 'user',
      content: [{ type: 'tool_result', ...(id ? { tool_use_id: id } : {}), ...(isError ? { is_error: true } : {}), content }],
    },
  })
}

function asResult(l: AgentLog) {
  const parsed = parseLogContent(l.type, l.content)
  if (parsed.kind !== 'tool_result') throw new Error('expected tool_result')
  return parsed
}

describe('groupLogRows', () => {
  it('folds a result into the call that produced it, by tool_use_id', () => {
    const rows = groupLogRows(
      [toolCall('Bash', { command: 'go test ./...' }, 't1'), toolResult('ok\nok\nok', 't1')],
      false,
      false,
    )

    expect(rows).toHaveLength(1)
    expect(rows[0].parsed.kind).toBe('tool_call')
    expect(rows[0].result?.parsed.text).toBe('ok\nok\nok')
  })

  it('pairs across an interleaved event using the tool_use_id', () => {
    const rows = groupLogRows(
      [
        toolCall('Bash', { command: 'go build ./...' }, 't1'),
        log('system', { type: 'system', subtype: 'init', model: 'claude-opus-5' }),
        toolResult('done', 't1'),
      ],
      false,
      false,
    )

    expect(rows).toHaveLength(2)
    expect(rows[0].result?.parsed.text).toBe('done')
    expect(rows[1].parsed.kind).toBe('system_event')
  })

  it('matches the right call when two are open at once', () => {
    const rows = groupLogRows(
      [
        toolCall('Read', { file_path: '/a/first.go' }, 't1'),
        toolCall('Read', { file_path: '/a/second.go' }, 't2'),
        toolResult('second body', 't2'),
        toolResult('first body', 't1'),
      ],
      false,
      false,
    )

    expect(rows).toHaveLength(2)
    expect(rows[0].result?.parsed.text).toBe('first body')
    expect(rows[1].result?.parsed.text).toBe('second body')
  })

  it('falls back to the adjacent call when the provider emits no tool_use_id', () => {
    const rows = groupLogRows(
      [toolCall('Bash', { command: 'ls' }), toolResult('a\nb')],
      false,
      false,
    )

    expect(rows).toHaveLength(1)
    expect(rows[0].result?.parsed.text).toBe('a\nb')
  })

  it('keeps a result whose call is not in view as its own row', () => {
    const rows = groupLogRows([toolResult('orphaned output', 't-missing')], false, false)

    expect(rows).toHaveLength(1)
    expect(rows[0].parsed.kind).toBe('tool_result')
  })

  it('does not attach a second result to an already-answered call', () => {
    const rows = groupLogRows(
      [toolCall('Bash', { command: 'ls' }), toolResult('first'), toolResult('second')],
      false,
      false,
    )

    expect(rows).toHaveLength(2)
    expect(rows[0].result?.parsed.text).toBe('first')
    expect(rows[1].parsed.kind).toBe('tool_result')
  })

  it('marks a trailing unanswered call as pending only while the run is live', () => {
    const logs = [toolCall('Bash', { command: 'go build ./...' }, 't1')]
    expect(groupLogRows(logs, false, true)[0].pending).toBe(true)
    expect(groupLogRows(logs, false, false)[0].pending).toBeUndefined()
  })

  it('does not mark an earlier unanswered call as pending', () => {
    const rows = groupLogRows(
      [toolCall('Bash', { command: 'ls' }, 't1'), log('stdout', 'plain text line')],
      false,
      true,
    )

    expect(rows[0].pending).toBeUndefined()
  })

  it('drops hidden debug entries unless debug is on', () => {
    const thinking = log('tool_call', { type: 'system', subtype: 'thinking_tokens', tokens: 12 })
    expect(groupLogRows([thinking], false, false)).toHaveLength(0)
    expect(groupLogRows([thinking], true, false)).toHaveLength(1)
  })
})

describe('resultView', () => {
  it('never repeats output text in the chip — the duplication this replaces', () => {
    const view = resultView(asResult(toolResult('line one\nline two\nline three\nline four')))

    expect(view.chip).toBe('4 lines')
    expect(view.body).toContain('line four')
    // The collapsed row must not carry a slice of what the disclosure shows.
    expect(view.body).not.toContain(view.chip)
    expect(view.chip).not.toContain('line one')
  })

  it('shows the full output, not the 120-char preview, behind the disclosure', () => {
    const long = 'x'.repeat(400)
    const view = resultView(asResult(toolResult(`${long}\nsecond line`)))

    expect(view.body).toContain(long)
    expect(view.body).toContain('second line')
    expect(view.body).not.toContain('…')
  })

  it('shows a short single-line result inline, with nothing hidden', () => {
    const view = resultView(asResult(toolResult('42')))

    expect(view.chip).toBe('42')
    expect(view.body).toBeUndefined()
  })

  it('collapses a long single-line result to ok and hides the text', () => {
    const boilerplate =
      'The file /home/josh/code_projects/agent-task-editor/backend/internal/agent/errclass.go has been updated successfully. (file state is current in your context)'
    const view = resultView(asResult(toolResult(boilerplate)))

    expect(view.chip).toBe('ok')
    expect(view.body).toBe(boilerplate)
  })

  it('flags an error and keeps its full text', () => {
    const view = resultView(asResult(toolResult('FAIL\nexit status 1', 't1', true)))

    expect(view.chip).toBe('error')
    expect(view.isError).toBe(true)
    expect(view.body).toContain('exit status 1')
  })

  it('reports an empty result rather than an empty disclosure', () => {
    const view = resultView(asResult(toolResult('   ')))

    expect(view.chip).toBe('no output')
    expect(view.body).toBeUndefined()
  })

  it('uses the file line count reported by a Read result', () => {
    const l = log('tool_result', {
      type: 'user',
      message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't1', content: 'file body' }] },
      tool_use_result: { type: 'text', file: { filePath: '/a/b/errclass_test.go', numLines: 40 } },
    })

    const view = resultView(asResult(l))
    expect(view.chip).toBe('40 lines')
    expect(view.body).toBe('file body')
  })
})
