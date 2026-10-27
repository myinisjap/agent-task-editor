import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AgentLog } from '../../api/client'
import { groupLogRows } from '../../lib/groupAgentLog'
import AgentLogEntry from './AgentLogEntry'

const GREP_OUTPUT = [
  '20:      {"gemini resource_exhausted", ClassRateLimit},',
  '27:      {"gemini invalid api key", ClassAuth},',
  '28:      {"gemini no auth method", ClassAuth},',
  '31:      {"gemini quota", ClassRateLimit},',
].join('\n')

function logs(...entries: [AgentLog['type'], unknown][]): AgentLog[] {
  return entries.map(([type, payload], i) => ({
    id: `log-${i}`,
    agent_run_id: 'run-1',
    timestamp: new Date(1700000000000 + i * 1000).toISOString(),
    type,
    content: JSON.stringify(payload),
  }))
}

function callAndResult(command: string, output: string, isError = false) {
  return logs(
    ['tool_call', {
      type: 'assistant',
      message: { role: 'assistant', content: [{ type: 'tool_use', id: 't1', name: 'Bash', input: { command } }] },
    }],
    ['tool_result', {
      type: 'user',
      message: {
        role: 'user',
        content: [{ type: 'tool_result', tool_use_id: 't1', ...(isError ? { is_error: true } : {}), content: output }],
      },
    }],
  )
}

function renderRows(entries: AgentLog[], isRunning = false) {
  const rows = groupLogRows(entries, false, isRunning)
  return { rows, ...render(<>{rows.map((r) => <AgentLogEntry key={r.key} row={r} />)}</>) }
}

describe('AgentLogEntry — merged tool rows', () => {
  it('renders one row for a call and its result', () => {
    const { rows } = renderRows(callAndResult('grep -n "gemini" errclass_test.go', GREP_OUTPUT))

    expect(rows).toHaveLength(1)
    expect(screen.getAllByTestId('tool-row')).toHaveLength(1)
    expect(screen.queryByTestId('orphan-result')).toBeNull()
  })

  it('shows the command and the outcome, but no output, until expanded', () => {
    renderRows(callAndResult('grep -n "gemini" errclass_test.go', GREP_OUTPUT))

    expect(screen.getByText('Bash')).toBeInTheDocument()
    expect(screen.getByText('grep -n "gemini" errclass_test.go')).toBeInTheDocument()
    expect(screen.getByText('4 lines')).toBeInTheDocument()
    expect(screen.queryByText(/resource_exhausted/)).toBeNull()
  })

  it('reveals the full output once on expand', async () => {
    const user = userEvent.setup()
    renderRows(callAndResult('grep -n "gemini" errclass_test.go', GREP_OUTPUT))

    await user.click(screen.getByRole('button'))

    const shown = screen.getAllByText(/resource_exhausted/)
    expect(shown).toHaveLength(1)
    expect(shown[0].textContent).toContain('quota')
  })

  it('auto-expands a failure', () => {
    renderRows(callAndResult('go test ./...', '--- FAIL: TestClassify\nexit status 1', true))

    expect(screen.getByText(/--- FAIL: TestClassify/)).toBeInTheDocument()
    expect(screen.getByRole('button')).toHaveAttribute('aria-expanded', 'true')
  })

  it('offers no disclosure when the chip already says everything', () => {
    renderRows(callAndResult('git rev-parse --short HEAD', 'a1b2c3d'))

    expect(screen.getByText('a1b2c3d')).toBeInTheDocument()
    expect(screen.getByRole('button')).toBeDisabled()
  })

  it('marks an unanswered call as running while the run is live', () => {
    const [call] = callAndResult('go build ./...', '')
    renderRows([call], true)

    expect(screen.getByText('running')).toBeInTheDocument()
    expect(screen.getByRole('button')).toBeDisabled()
  })
})

describe('AgentLogEntry system events', () => {
  it('styles by severity', () => {
    const l = logs(
      ['system', 'Run failed: claude exited with an error (it had reported "success")'],
      ['system', 'Human reply: ok'],
    )
    // logs() JSON-encodes content; undo for plain-text system lines
    l.forEach((e) => { e.content = JSON.parse(e.content) })
    const rows = groupLogRows(l, false, false)
    render(<>{rows.map((r) => <AgentLogEntry key={r.key} row={r} />)}</>)
    const [err, info] = screen.getAllByTestId('system-event')
    expect(err.dataset.severity).toBe('error')
    expect(err.innerHTML).toContain('text-red-')
    expect(info.dataset.severity).toBe('info')
    expect(info.innerHTML).not.toContain('text-red-')
  })
})

describe('AgentLogEntry — long output', () => {
  it('truncates long output behind Show more / Show less', async () => {
    const long = Array.from({ length: 50 }, (_, i) => `line ${i}`).join('\n')
    renderRows(callAndResult('cat big.txt', long, true))
    // errors auto-expand
    expect(screen.getByText(/line 0/)).toBeInTheDocument()
    expect(screen.queryByText(/line 49/)).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: /Show more \(30 more lines\)/ }))
    expect(screen.getByText(/line 49/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Show less' }))
    expect(screen.queryByText(/line 49/)).toBeNull()
  })
})
