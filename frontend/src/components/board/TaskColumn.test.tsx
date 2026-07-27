// TaskColumn WIP-limit badge rendering: count-vs-limit display and the
// soft/hard over-limit visual states. See issue #257.
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import TaskColumn from './TaskColumn'
import type { Task, WorkflowLabel } from '../../api/client'

function label(overrides: Partial<WorkflowLabel> = {}): WorkflowLabel {
  return {
    id: 'l1',
    workflow_id: 'wf',
    name: 'review',
    color: '#000',
    sort_order: 0,
    agent_ignore: 0,
    is_terminal: 0,
    create_pr: 0,
    wip_limit: null,
    wip_limit_hard: 0,
    ...overrides,
  }
}

function task(id: string): Task {
  return {
    id,
    title: `Task ${id}`,
    description: '',
    type: 'feature',
    label: 'review',
    repo_id: 'r1',
    workflow_id: 'wf',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  }
}

function renderColumn(l: WorkflowLabel, tasks: Task[]) {
  return render(
    <MemoryRouter>
      <TaskColumn label={l} tasks={tasks} runningTaskIds={new Set()} />
    </MemoryRouter>,
  )
}

describe('TaskColumn WIP limit badge', () => {
  it('renders just the count when no wip_limit is set', () => {
    renderColumn(label({ wip_limit: null }), [task('1'), task('2')])
    expect(screen.getByTestId('column-count-badge').textContent).toBe('2')
  })

  it('renders count / limit when wip_limit is set and under capacity', () => {
    renderColumn(label({ wip_limit: 5 }), [task('1'), task('2')])
    expect(screen.getByTestId('column-count-badge').textContent).toBe('2 / 5')
  })

  it('applies soft over-limit styling (amber) when over a soft (non-hard) limit', () => {
    renderColumn(label({ wip_limit: 1, wip_limit_hard: 0 }), [task('1'), task('2')])
    const badge = screen.getByTestId('column-count-badge')
    expect(badge.textContent).toBe('2 / 1')
    expect(badge.className).toContain('amber')
    expect(badge.title).toMatch(/visual warning only/i)
  })

  it('applies hard over-limit styling (red) when over a hard limit', () => {
    renderColumn(label({ wip_limit: 1, wip_limit_hard: 1 }), [task('1'), task('2')])
    const badge = screen.getByTestId('column-count-badge')
    expect(badge.textContent).toBe('2 / 1')
    expect(badge.className).toContain('red')
    expect(badge.title).toMatch(/dispatcher will hold/i)
  })

  it('flags hard limit when count equals the limit exactly (matches dispatcher, which blocks at >= limit)', () => {
    renderColumn(label({ wip_limit: 2, wip_limit_hard: 1 }), [task('1'), task('2')])
    const badge = screen.getByTestId('column-count-badge')
    expect(badge.textContent).toBe('2 / 2')
    expect(badge.className).toContain('red')
    expect(badge.title).toMatch(/dispatcher will hold/i)
  })

  it('flags soft limit when count equals the limit exactly', () => {
    renderColumn(label({ wip_limit: 2, wip_limit_hard: 0 }), [task('1'), task('2')])
    const badge = screen.getByTestId('column-count-badge')
    expect(badge.textContent).toBe('2 / 2')
    expect(badge.className).toContain('amber')
  })

  it('does not flag when count is below the limit', () => {
    renderColumn(label({ wip_limit: 3, wip_limit_hard: 1 }), [task('1'), task('2')])
    const badge = screen.getByTestId('column-count-badge')
    expect(badge.textContent).toBe('2 / 3')
    expect(badge.className).not.toContain('red')
    expect(badge.className).not.toContain('amber')
  })

  it('treats a non-positive wip_limit as unlimited', () => {
    renderColumn(label({ wip_limit: 0 }), [task('1')])
    expect(screen.getByTestId('column-count-badge').textContent).toBe('1')
  })
})
