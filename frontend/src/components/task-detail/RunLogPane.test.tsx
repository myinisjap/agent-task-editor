import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import fixtures from '../../lib/__fixtures__/run-timeline-runs.json'

/* eslint-disable @typescript-eslint/no-explicit-any */
const F = fixtures as any
const runLogsMock = vi.fn()

vi.mock('../../api/client', () => ({
  api: { tasks: { runLogs: (...a: unknown[]) => runLogsMock(...a) } },
}))
vi.mock('../../api/ws', () => ({
  wsClient: { on: () => () => {} },
}))
// jsdom has no layout, so render every item.
vi.mock('@tanstack/react-virtual', () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    scrollToIndex: vi.fn(),
    getTotalSize: () => count * 40,
    getVirtualItems: () => Array.from({ length: count }, (_, index) => ({ index, start: index * 40 })),
    measureElement: vi.fn(),
  }),
}))

import RunLogPane from './RunLogPane'

function mount(name: string, extra: Record<string, unknown> = {}) {
  const { run, logs } = F[name]
  runLogsMock.mockResolvedValue({ items: logs, hasMore: false })
  return render(
    <MemoryRouter>
      <RunLogPane taskId="t1" runId={run.id} isRunning={run.status === 'running'} run={run} {...extra} />
    </MemoryRouter>,
  )
}

describe('RunLogPane timeline', () => {
  beforeEach(() => runLogsMock.mockReset())

  it('shows the summary card, grouped steps, and expands a group', async () => {
    mount('success')
    expect(await screen.findByText('Finished successfully')).toBeInTheDocument()
    const header = await screen.findByRole('button', { name: /Edited 2 files/ })
    expect(header).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryAllByTestId('tool-row')).toHaveLength(0) // all groups collapsed
  })

  it('expands a group on click and shows rows', async () => {
    mount('success')
    const header = await screen.findByRole('button', { name: /Read 2 files/ })
    await userEvent.click(header)
    expect(header).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getAllByTestId('tool-row').length).toBeGreaterThanOrEqual(2)
  })

  it('Debug shows the flat list', async () => {
    mount('success')
    await screen.findByText('Finished successfully')
    await userEvent.click(screen.getByLabelText('Debug'))
    await waitFor(() => expect(screen.queryAllByTestId('step-group')).toHaveLength(0))
    expect(screen.getAllByTestId('tool-row').length).toBe(6)
  })

  it('renders a fix link to Health for auth_required', async () => {
    runLogsMock.mockResolvedValue({ items: [], hasMore: false })
    const { run } = F.auth_required
    render(
      <MemoryRouter>
        <RunLogPane taskId="t1" runId="run-wait" isRunning={false} run={run} />
      </MemoryRouter>,
    )
    const link = await screen.findByRole('link', { name: 'Open Health' })
    expect(link).toHaveAttribute('href', '/settings/health')
  })

  it('shows Retry only when a handler is supplied, and the live step', async () => {
    const onRetry = vi.fn()
    mount('test_failure', { onRetry })
    await userEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalled()
  })

  it('shows the current step for a live run', async () => {
    mount('live')
    expect(await screen.findByTestId('current-step')).toHaveTextContent('Now: Editing /repo/src/api/tasks.js')
    expect(screen.queryByTestId('next-step')).toBeNull()
  })
})
