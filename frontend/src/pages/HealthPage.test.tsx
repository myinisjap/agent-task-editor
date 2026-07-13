// HealthPage backup-settings form tests: loads current interval/keep,
// validates the 10-minute floor client-side before submitting, and saves
// valid values via api.backup.updateSettings.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import HealthPage from './HealthPage'

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    authedRawFetch: vi.fn(),
    api: {
      health: {
        providers: vi.fn().mockResolvedValue({ checks: [] }),
      },
      backup: {
        url: () => '/api/v1/backup',
        getSettings: vi.fn(),
        updateSettings: vi.fn(),
      },
    },
  }
})

import { api } from '../api/client'

beforeEach(() => {
  vi.clearAllMocks()
  ;(api.backup.getSettings as ReturnType<typeof vi.fn>).mockResolvedValue({
    interval_seconds: 86400,
    keep: 7,
    updated_at: '2026-01-01T00:00:00Z',
  })
  ;(api.backup.updateSettings as ReturnType<typeof vi.fn>).mockImplementation(
    async (body: { interval_seconds: number; keep: number }) => ({
      ...body,
      updated_at: '2026-01-02T00:00:00Z',
    })
  )
})

describe('HealthPage backup settings', () => {
  it('loads and displays the current interval (in minutes) and keep count', async () => {
    render(<HealthPage />)

    const intervalInput = await screen.findByLabelText(/Backup frequency/i)
    await waitFor(() => expect(intervalInput).toHaveValue(1440)) // 86400s = 1440min = once a day

    const keepInput = screen.getByLabelText(/Backups to keep/i)
    expect(keepInput).toHaveValue(7)
  })

  it('rejects an interval below the 10 minute floor without calling the API', async () => {
    const user = userEvent.setup()
    render(<HealthPage />)

    const intervalInput = await screen.findByLabelText(/Backup frequency/i)
    await waitFor(() => expect(intervalInput).toHaveValue(1440))

    await user.clear(intervalInput)
    await user.type(intervalInput, '5')
    await user.click(screen.getByRole('button', { name: /Save backup settings/i }))

    expect(await screen.findByText(/at least 10 minutes/i)).toBeInTheDocument()
    expect(api.backup.updateSettings).not.toHaveBeenCalled()
  })

  it('saves valid settings via the API', async () => {
    const user = userEvent.setup()
    render(<HealthPage />)

    const intervalInput = await screen.findByLabelText(/Backup frequency/i)
    await waitFor(() => expect(intervalInput).toHaveValue(1440))

    await user.clear(intervalInput)
    await user.type(intervalInput, '60')
    const keepInput = screen.getByLabelText(/Backups to keep/i)
    await user.clear(keepInput)
    await user.type(keepInput, '3')
    await user.click(screen.getByRole('button', { name: /Save backup settings/i }))

    await waitFor(() =>
      expect(api.backup.updateSettings).toHaveBeenCalledWith({ interval_seconds: 3600, keep: 3 })
    )
    expect(await screen.findByText(/Backup settings saved/i)).toBeInTheDocument()
  })
})
