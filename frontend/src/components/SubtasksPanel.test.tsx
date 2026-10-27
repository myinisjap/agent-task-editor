import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import SubtasksPanel, { subtaskStatus } from './SubtasksPanel'
import type { Task, WorkflowLabel } from '../api/client'

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return { ...actual, api: { tasks: { subtasks: vi.fn(), bulk: vi.fn() } } }
})
vi.mock('../api/ws', () => ({ wsClient: { on: vi.fn(() => () => {}) } }))

import { api } from '../api/client'

const label = (name: string, extra: Partial<WorkflowLabel> = {}): WorkflowLabel =>
  ({ id: name, workflow_id: 'wf', name, color: '#000', sort_order: 0, agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit: null, wip_limit_hard: 0, verify_mode: 'inherit', verify_command: '', ...extra }) as WorkflowLabel

const labels = [label('gate', { agent_ignore: 1 }), label('work'), label('done', { is_terminal: 1 })]

const task = (over: Partial<Task>): Task => ({ id: 't', title: 'T', label: 'work', ...over }) as Task

describe('subtaskStatus', () => {
  it('derives the status chip', () => {
    expect(subtaskStatus(task({ active_run_status: 'running' }), labels)).toBe('running')
    expect(subtaskStatus(task({ label: 'done' }), labels)).toBe('done')
    expect(subtaskStatus(task({ label: 'gate' }), labels)).toBe('awaiting release')
    expect(subtaskStatus(task({ block_reason: { code: 'dependency', message: 'x' } }), labels)).toBe('blocked')
    expect(subtaskStatus(task({}), labels)).toBe('queued')
  })
})

describe('SubtasksPanel', () => {
  beforeEach(() => {
    vi.mocked(api.tasks.subtasks).mockResolvedValue([
      task({ id: 'a', title: 'Child A', file_scope: ['backend/a/**'], active_run_status: 'running' }),
      task({ id: 'b', title: 'Child B', file_scope: [], label: 'gate' }),
    ])
  })

  it('renders scope chips, status chips and the integration badge', async () => {
    render(
      <MemoryRouter>
        <SubtasksPanel task={task({ id: 'p', integration_status: 'pending' })} labels={labels} />
      </MemoryRouter>,
    )
    await waitFor(() => expect(screen.getByText('Child A')).toBeTruthy())
    expect(screen.getByText('backend/a/**')).toBeTruthy()
    expect(screen.getByText('no scope')).toBeTruthy()
    const chips = screen.getAllByTestId('subtask-status').map((e) => e.textContent)
    expect(chips).toEqual(['running', 'awaiting release'])
    expect(screen.getByText('integration pending')).toBeTruthy()
  })
})
