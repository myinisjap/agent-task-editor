import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { backtestResult, gateWorkflow } from './testFixtures'

const m = vi.hoisted(() => ({
  backtest: vi.fn(), history: vi.fn(), rulesList: vi.fn(), rulesDelete: vi.fn(),
  get: vi.fn(), updateYaml: vi.fn(), listWorkflows: vi.fn(), listRepos: vi.fn(),
}))

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      workflows: { ...actual.api.workflows, get: m.get, updateYaml: m.updateYaml, list: m.listWorkflows },
      repos: { ...actual.api.repos, list: m.listRepos },
      autonomy: {
        ...actual.api.autonomy,
        backtest: m.backtest,
        history: m.history,
        askRules: { list: m.rulesList, delete: m.rulesDelete },
      },
    },
  }
})

import GateDialPanel from './GateDialPanel'
import { useReposStore } from '../../stores/repos'
import { useWorkflowStore } from '../../stores/workflow'

const emptyHistory = { workflow_id: 'w1', label: 'review', current_mode: 'ask', changes: [], escapes: [], step_down: { escapes_counted: 0, threshold: 2, window_days: 14 } }

function renderPanel(labelName = 'review', autonomy?: Record<string, unknown>) {
  const wf = gateWorkflow(autonomy)
  const label = wf.labels.find((l) => l.name === labelName)!
  m.get.mockResolvedValue(wf)
  return render(<MemoryRouter><GateDialPanel workflow={wf} label={label} onClose={() => {}} /></MemoryRouter>)
}

describe('GateDialPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m.backtest.mockResolvedValue(backtestResult)
    m.history.mockResolvedValue(emptyHistory)
    m.rulesList.mockResolvedValue([])
    m.updateYaml.mockResolvedValue(undefined)
    m.listWorkflows.mockResolvedValue([gateWorkflow()])
    m.listRepos.mockResolvedValue([{ id: 'r1', name: 'repoA', workflow_id: 'w1', verify_command: '' }])
    useReposStore.setState({ repos: [], loading: false, error: null })
    useWorkflowStore.setState({ workflows: [], selectedId: null })
  })
  afterEach(() => vi.useRealTimers())

  it('disables flag checkboxes unless Ask on flags', async () => {
    const user = userEvent.setup()
    renderPanel()
    const box = screen.getByRole('checkbox', { name: /migration/i })
    expect(box).toBeDisabled()
    await user.click(screen.getByRole('radio', { name: 'Ask on flags' }))
    expect(screen.getByRole('checkbox', { name: /migration/i })).toBeEnabled()
    expect(screen.getAllByRole('checkbox')).toHaveLength(7)
  })

  it('hides Ask on flags on a gate with no diff yet', () => {
    renderPanel('review-plan')
    expect(screen.queryByRole('radio', { name: 'Ask on flags' })).not.toBeInTheDocument()
    expect(screen.getByText(/no code changes yet/i)).toBeInTheDocument()
    expect(screen.queryByText('Ask when the brief flags')).not.toBeInTheDocument()
  })

  it('warns about PRs on Auto and about repos without a verify command', async () => {
    const user = userEvent.setup()
    renderPanel()
    expect(screen.queryByText(/PRs will open/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: 'Auto' }))
    expect(screen.getByText(/PRs will open without anyone reading the diff/)).toBeInTheDocument()
    expect(await screen.findByText(/No verify command for: repoA/)).toBeInTheDocument()
  })

  it('debounces the backtest and renders numbers, strip, legend and verdict', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    renderPanel()
    await act(async () => { await vi.advanceTimersByTimeAsync(450) })
    expect(m.backtest).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('radio', { name: 'Auto' }))
    fireEvent.click(screen.getByRole('radio', { name: 'Ask on flags' }))
    fireEvent.click(screen.getByRole('radio', { name: 'Auto' }))
    expect(m.backtest).toHaveBeenCalledTimes(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(450) })
    expect(m.backtest).toHaveBeenCalledTimes(2)
    expect(m.backtest.mock.calls[1][2]).toMatchObject({ mode: 'auto', days: 30 })
    const card = screen.getByRole('region', { name: /backtest/i })
    expect(within(card).getByText('Would skip')).toBeInTheDocument()
    expect(within(card).getAllByRole('listitem').length).toBeGreaterThanOrEqual(3)
    expect(within(card).getByRole('list', { name: 'Passes, oldest first' }).children).toHaveLength(3)
    expect(within(card).getByRole('list', { name: 'Legend' })).toBeInTheDocument()
    expect(within(card).getByText(/Would have skipped 2 of 3 reviews/)).toBeInTheDocument()
  })

  it('lists ask rules with Unused chip and source task, and deletes after confirming', async () => {
    const user = userEvent.setup()
    m.rulesList.mockResolvedValue([{
      id: 'ar1', repo_id: null, workflow_id: 'w1', label: null, path_globs: ['db/**'], task_type: null,
      min_changed_lines: null, created_by: 'josh', source_task_id: 'task9', created_at: '2024-01-01T00:00:00Z',
      hit_count: 0, last_hit_at: null, summary: 'Touches db/**',
    }])
    renderPanel()
    expect(await screen.findByText('Unused')).toBeInTheDocument()
    expect(screen.getByText('All gates')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'from task' })).toHaveAttribute('href', '/tasks/task9')
    await user.click(screen.getByRole('button', { name: /delete rule/i }))
    expect(m.rulesDelete).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Delete rule' }))
    await waitFor(() => expect(m.rulesDelete).toHaveBeenCalledWith('ar1'))
  })

  it('shows a step-down and Change back saves the previous mode', async () => {
    const user = userEvent.setup()
    m.history.mockResolvedValue({
      ...emptyHistory, current_mode: 'on_flags',
      changes: [{ id: 'c1', workflow_id: 'w1', label: 'review', from_mode: 'auto', to_mode: 'on_flags', actor: 'policy', reason: '', created_at: '2025-01-05T00:00:00Z' }],
      step_down: { escapes_counted: 2, threshold: 2, window_days: 14 },
    })
    renderPanel('review', { mode: 'on_flags', ask_on: ['migration'], hold: '600s' })
    expect(await screen.findByText(/2 escapes in 14 days. Moved from Auto to Ask on flags/)).toBeInTheDocument()
    expect(screen.getByText(/Escapes counted: 2 of 2 in last 14 days/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Change back' }))
    await waitFor(() => expect(m.updateYaml).toHaveBeenCalled())
    const body = JSON.parse(m.updateYaml.mock.calls[0][1])
    expect(body.labels.find((l: { name: string }) => l.name === 'review').autonomy.mode).toBe('auto')
  })

  it('Save re-fetches the workflow and writes a body that keeps everything else', async () => {
    const user = userEvent.setup()
    renderPanel()
    const save = screen.getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()
    await user.click(screen.getByRole('radio', { name: 'Auto' }))
    await user.click(save)
    await waitFor(() => expect(m.updateYaml).toHaveBeenCalledTimes(1))
    expect(m.get).toHaveBeenCalledWith('w1')
    const body = JSON.parse(m.updateYaml.mock.calls[0][1])
    expect(body.labels.map((l: { name: string }) => l.name)).toEqual(['plan', 'review-plan', 'work', 'review', 'done'])
    expect(body.labels.find((l: { name: string }) => l.name === 'done').create_pr).toBe(true)
    expect(body.transitions).toHaveLength(5)
    expect(body.labels.find((l: { name: string }) => l.name === 'review').autonomy.mode).toBe('auto')
  })

  it('shows server errors inline', async () => {
    const user = userEvent.setup()
    m.updateYaml.mockRejectedValue(new Error('bad autonomy'))
    renderPanel()
    await user.click(screen.getByRole('radio', { name: 'Auto' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('bad autonomy')
  })
})
