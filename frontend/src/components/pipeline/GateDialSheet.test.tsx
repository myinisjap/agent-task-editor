import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { backtestResult, gateWorkflow } from './testFixtures'

const m = vi.hoisted(() => ({ backtest: vi.fn(), get: vi.fn(), updateYaml: vi.fn(), listWorkflows: vi.fn(), listRepos: vi.fn() }))
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      ...actual.api,
      workflows: { ...actual.api.workflows, get: m.get, updateYaml: m.updateYaml, list: m.listWorkflows },
      repos: { ...actual.api.repos, list: m.listRepos },
      autonomy: { ...actual.api.autonomy, backtest: m.backtest },
    },
  }
})

import GateDialSheet from './GateDialSheet'

describe('GateDialSheet', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    m.backtest.mockResolvedValue(backtestResult)
    m.updateYaml.mockResolvedValue(undefined)
    m.listWorkflows.mockResolvedValue([gateWorkflow()])
    m.listRepos.mockResolvedValue([])
  })

  it('shows the summary, flag count, Edit and Save', async () => {
    const user = userEvent.setup()
    const wf = gateWorkflow({ mode: 'on_flags', ask_on: ['migration', 'auth_or_security'], hold: '600s' })
    m.get.mockResolvedValue(wf)
    render(<MemoryRouter><GateDialSheet workflow={wf} label={wf.labels.find((l) => l.name === 'review')!} onClose={() => {}} /></MemoryRouter>)
    expect(await screen.findByText(/Last 30 days: would skip 2 of 3/)).toBeInTheDocument()
    expect(screen.getByText(/Flags that ask: 2 of 7/)).toBeInTheDocument()
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.getAllByRole('checkbox')).toHaveLength(7)
    await user.click(screen.getByRole('checkbox', { name: /dependencies/i }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(m.updateYaml).toHaveBeenCalled())
    const body = JSON.parse(m.updateYaml.mock.calls[0][1])
    expect(body.labels.find((l: { name: string }) => l.name === 'review').autonomy.ask_on).toContain('dependencies')
  })
})
