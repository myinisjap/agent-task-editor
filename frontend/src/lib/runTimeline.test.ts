import { describe, it, expect } from 'vitest'
import fixtures from './__fixtures__/run-timeline-runs.json'
import { groupLogRows } from './groupAgentLog'
import {
  classifyRow, commandOutcome, countReworkLoops, currentStep, deriveRunSummary, groupSteps,
  isTestCommand, truncateOutput,
} from './runTimeline'

/* eslint-disable @typescript-eslint/no-explicit-any */
const F = fixtures as any
const agentConfigs = [{ id: 'agent-1', name: 'Coder', provider_config_id: 'prov-1' }] as any
const providerConfigs = [{ id: 'prov-1', name: 'Anthropic', model: 'claude-opus-4' }] as any
const NOW = Date.parse('2024-01-01T10:02:00Z')

function load(name: string) {
  const { run, logs } = F[name]
  const rows = groupLogRows(logs, false, run.status === 'running')
  return { run, rows }
}
function summary(name: string, extra: Record<string, unknown> = {}) {
  const { run, rows } = load(name)
  return deriveRunSummary({ run: { ...run, ...extra }, rows, agentConfigs, providerConfigs, labelHistory: F.label_history, now: NOW })
}

describe('isTestCommand', () => {
  it.each(['npm test', 'cd frontend && npm run test', 'npx vitest run', 'go test ./...', 'pytest -q', 'cargo test', 'pnpm test', 'make test'])(
    'matches %s', (c) => expect(isTestCommand(c)).toBe(true))
  it.each(['ls src', 'npm install', 'git status', 'echo contest', 'cat testfile.txt'])(
    'rejects %s', (c) => expect(isTestCommand(c)).toBe(false))
})

describe('deriveRunSummary', () => {
  it('summarises a successful run', () => {
    const s = summary('success')
    expect(s.outcome).toMatchObject({ tone: 'success', text: 'Finished successfully' })
    expect(s.filesChanged).toEqual(['/repo/src/api/tasks.js', '/repo/src/api/api.ts'])
    expect(s.commands).toEqual({ total: 0, failed: 0 })
    expect(s.tests).toEqual({ total: 1, passed: 1, failed: 0, running: 0 })
    expect(s.durationSecs).toBe(192)
    expect(s.costUsd).toBe(0.42)
    expect(s.agentName).toBe('Coder')
    expect(s.providerModel).toBe('claude-opus-4')
    expect(s.nextStep?.action).toBe('diff')
    expect(s.currentStep).toBeNull()
  })

  it('explains a failed run from the last error event', () => {
    const s = summary('test_failure')
    expect(s.outcome.tone).toBe('failed')
    expect(s.outcome.why).toContain('Run failed: claude exited with an error')
    expect(s.tests).toMatchObject({ total: 1, failed: 1, passed: 0 })
    expect(s.commands).toEqual({ total: 1, failed: 0 })
    expect(s.nextStep?.action).toBe('retry')
  })

  it('falls back to run notes when no error event exists', () => {
    const { run } = load('test_failure')
    const s = deriveRunSummary({ run, rows: [] })
    expect(s.outcome.why).toBe('fallback note')
  })

  it('reports an agent question with a reply next step', () => {
    const s = summary('escalated')
    expect(s.outcome.tone).toBe('waiting')
    expect(s.outcome.text).toBe('Agent asked a question')
    expect(s.outcome.why).toContain('Which database should I use?')
    expect(s.nextStep?.action).toBe('reply')
  })

  it('links auth_required to Health', () => {
    const s = summary('auth_required')
    expect(s.nextStep).toMatchObject({ action: 'fix', fix: { to: '/settings/health', text: 'Open Health' } })
  })

  it('shows the live current step and ticking duration', () => {
    const s = summary('live')
    expect(s.outcome.tone).toBe('running')
    expect(s.currentStep).toBe('Editing /repo/src/api/tasks.js')
    expect(s.durationSecs).toBe(120)
    expect(s.nextStep).toBeNull()
    expect(summary('live_test').currentStep).toBe('Running npm test')
    expect(summary('live_test').tests.running).toBe(1)
  })

  it('handles cost unknown, missing config and no start', () => {
    const s = summary('success', { cost_unknown: true, agent_config_id: null, provider_config_id: null, started_at: null })
    expect(s.costUnknown).toBe(true)
    expect(s.agentName).toBeUndefined()
    expect(s.durationSecs).toBeNull()
  })

  it('counts rework loops up to the run creation time', () => {
    expect(summary('success').reworkLoop).toBeNull()
    expect(summary('success', { created_at: '2024-01-01T11:00:00Z' }).reworkLoop).toBe(1)
  })

  it.each([
    ['cancelled', 'Stopped'], ['interrupted', 'Interrupted (server restarted)'], ['pending', 'Queued'],
  ])('maps %s', (status, text) => {
    expect(summary('success', { status }).outcome.text).toBe(text)
  })
})

describe('commandOutcome', () => {
  it('parses exit codes', () => {
    const { rows } = load('test_failure')
    const bashRows = rows.filter((r) => r.parsed.kind === 'tool_call')
    expect(commandOutcome(bashRows[0])).toEqual({ state: 'failed', exitCode: 1 })
    expect(commandOutcome(bashRows[1])).toEqual({ state: 'passed', exitCode: 0 })
    expect(commandOutcome(load('live_test').rows[1]).state).toBe('pending')
  })
})

describe('groupSteps', () => {
  it('merges consecutive reads/edits and isolates tests and messages', () => {
    const groups = groupSteps(load('success').rows)
    expect(groups.map((g) => g.kind)).toEqual(['system', 'message', 'read', 'edit', 'test', 'system'])
    expect(groups[2].summary).toBe('Read 2 files: src/api/tasks.js, src/api/client.ts')
    expect(groups[3].rows).toHaveLength(3)
    expect(groups[3].summary).toBe('Edited 2 files: src/api/tasks.js, src/api/api.ts')
    expect(groups[4].summary).toBe('Tests: `npm test` — passed')
    expect(groups[3].key).toBe(groups[3].rows[0].key)
  })

  it('flags failures and summarises commands', () => {
    const groups = groupSteps(load('test_failure').rows)
    const test = groups.find((g) => g.kind === 'test')!
    expect(test.hasError).toBe(true)
    expect(test.summary).toBe('Tests: `npm test` — failed (exit 1)')
    expect(groups.find((g) => g.kind === 'command')!.summary).toBe('Ran `ls src` — exit 0')
    expect(groups[groups.length - 1]).toMatchObject({ kind: 'error', hasError: true })
  })

  it('classifies human tool calls and shows +N more', () => {
    const rows = load('escalated').rows
    expect(classifyRow(rows[1])).toBe('human')
    expect(groupSteps(rows)[1].summary).toBe('Asked you: Which database should I use?')
    const logs = ['a', 'b', 'c', 'd', 'e'].map((f, i) => ({
      ...F.success.logs[0], id: `x${i}`, type: 'tool_call',
      content: JSON.stringify({ type: 'assistant', message: { role: 'assistant', content: [{ type: 'tool_use', id: `t${i}`, name: 'Read', input: { file_path: `/r/${f}.ts` } }] } }),
    }))
    expect(groupSteps(groupLogRows(logs, false, false))[0].summary).toBe('Read 5 files: r/a.ts, r/b.ts, r/c.ts +2 more')
  })

  it('marks the trailing live group pending', () => {
    const groups = groupSteps(load('live').rows)
    expect(groups[groups.length - 1]).toMatchObject({ kind: 'edit', pending: true })
  })
})

describe('currentStep', () => {
  it('is null when not running and Starting… without rows', () => {
    expect(currentStep(load('live').rows, false)).toBeNull()
    expect(currentStep([], true)).toBe('Starting…')
  })
})

describe('countReworkLoops', () => {
  it('counts revisits and respects upTo', () => {
    expect(countReworkLoops(F.label_history)).toBe(1)
    expect(countReworkLoops(F.label_history, '2024-01-01T10:00:00Z')).toBe(0)
    expect(countReworkLoops([])).toBe(0)
  })
})

describe('truncateOutput', () => {
  it('leaves short text alone', () => {
    expect(truncateOutput('a\nb')).toEqual({ head: 'a\nb', truncated: false, hiddenLines: 0 })
  })
  it('truncates by lines', () => {
    const text = Array.from({ length: 30 }, (_, i) => `l${i}`).join('\n')
    const r = truncateOutput(text, 20)
    expect(r.truncated).toBe(true)
    expect(r.hiddenLines).toBe(10)
    expect(r.head.split('\n')).toHaveLength(20)
  })
  it('truncates by chars', () => {
    const r = truncateOutput('x'.repeat(5000), 20, 2000)
    expect(r.truncated).toBe(true)
    expect(r.head).toHaveLength(2000)
  })
})
