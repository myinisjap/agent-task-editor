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

describe('SubtasksPanel change set (ADR 0041)', () => {
  const cs = (over: Partial<import('../api/client').ChangeSet> = {}): import('../api/client').ChangeSet => ({
    multi_repo: true,
    parent: { task_id: 'p', title: 'Parent', repo_id: 'r1', label: 'work', rolled_up_cost_usd: 3.5 },
    merge_order: ['c1', 'c2'],
    children: [
      { task_id: 'c0', title: 'Home child', repo_id: 'r1', foreign: false, label: 'work', branch: 'ate/c0', git_state: '', pr_url: '', merge_status: 'pending', verification: 'not_run', changed: null, auto_merge: null, merge_position: null },
      { task_id: 'c1', title: 'API child', repo_id: 'r2', foreign: true, label: 'work', branch: 'ate/c1', git_state: 'pr_open', pr_url: 'https://x/pr/1', merge_status: 'pr_open', verification: 'pass', changed: { files: 3, additions: 10, deletions: 2 }, auto_merge: null, merge_position: 1 },
      { task_id: 'c2', title: 'UI child', repo_id: 'r3', foreign: true, label: 'work', branch: 'ate/c2', git_state: 'pr_open', pr_url: 'https://x/pr/2', merge_status: 'pr_open', verification: 'fail', changed: null, auto_merge: { state: 'waiting', reason: 'change_set_order', message: 'waiting on API child' }, merge_position: 2 },
    ],
    ...over,
  })
  const kids = [task({ id: 'c0', title: 'Home child', repo_id: 'r1' }), task({ id: 'c1', title: 'API child' }), task({ id: 'c2', title: 'UI child' })]

  beforeEach(() => {
    vi.mocked(api.tasks.subtasks).mockResolvedValue(kids)
    ;(api.tasks as unknown as Record<string, unknown>).changeSet = vi.fn().mockResolvedValue(cs())
  })

  it('groups children by repo (primary first) with per-row details', async () => {
    render(<MemoryRouter><SubtasksPanel task={task({ id: 'p', repo_id: 'r1', change_set_repo_count: 3 })} labels={labels} /></MemoryRouter>)
    expect(await screen.findByText('Change set · 3 repos')).toBeTruthy()
    const groups = screen.getAllByTestId('change-set-group')
    expect(groups).toHaveLength(3)
    expect(groups[0].textContent).toContain('(primary)')
    expect(groups[0].textContent).toContain('Home child')
    expect(groups[1].textContent).toContain('API child')
    const rows = screen.getAllByTestId('change-set-row')
    expect(rows).toHaveLength(3)
    expect(rows[1].textContent).toContain('3 files +10 −2')
    expect(rows[1].textContent).toContain('verify: pass')
    expect(rows[1].querySelector('a[href="https://x/pr/1"]')).toBeTruthy()
    expect(rows[1].querySelector('a[href="/tasks/c1?tab=diff"]')).toBeTruthy()
    expect(rows[2].textContent).toContain('waiting for earlier PRs')
    expect(rows[2].textContent).toContain('verify: fail')
    expect(screen.getByTestId('change-set-order').textContent).toBe('Merge order: API child → UI child')
  })

  it('shows a Part of change set line on a child', async () => {
    render(<MemoryRouter><SubtasksPanel task={task({ id: 'c1', parent_task_id: 'p', repo_id: 'r2' })} labels={labels} /></MemoryRouter>)
    const line = await screen.findByTestId('change-set-line')
    expect(line.querySelector('a[href="/tasks/p"]')).toBeTruthy()
    expect(line.querySelector('a[href="/tasks/c2"]')).toBeTruthy()
    expect(line.querySelector('a[href="/tasks/c1"]')).toBeNull()
  })

  it('does not call changeSet or render the panel for a single-repo parent', async () => {
    render(<MemoryRouter><SubtasksPanel task={task({ id: 'p', change_set_repo_count: 0 })} labels={labels} /></MemoryRouter>)
    await waitFor(() => expect(screen.getByText('Home child')).toBeTruthy())
    expect((api.tasks as unknown as { changeSet: ReturnType<typeof vi.fn> }).changeSet).not.toHaveBeenCalled()
    expect(screen.queryByTestId('change-set')).toBeNull()
  })
})
