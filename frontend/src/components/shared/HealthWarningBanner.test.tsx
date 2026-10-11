import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import HealthWarningBanner from './HealthWarningBanner'
import { useHealthStore } from '../../stores/health'

const healthMock = vi.fn()
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return { ...actual, api: { health: { providers: (...a: unknown[]) => healthMock(...a) } } }
})

const renderBanner = () => render(<MemoryRouter><HealthWarningBanner /></MemoryRouter>)

beforeEach(() => {
  useHealthStore.setState({ checks: [], loaded: false })
  healthMock.mockReset()
})

describe('HealthWarningBanner', () => {
  it('shows failing checks with a link to the Health page', async () => {
    healthMock.mockResolvedValue({
      checks: [
        { id: 'claude_cli', name: 'Claude CLI', status: 'warn', detail: 'no credentials detected' },
        { id: 'x', name: 'Fine', status: 'ok', detail: 'ok' },
      ],
    })
    renderBanner()
    const banner = await screen.findByTestId('health-warning-banner')
    expect(banner).toHaveTextContent('Claude CLI: no credentials detected')
    expect(banner).not.toHaveTextContent('Fine')
    expect(screen.getByRole('link', { name: /View health/ })).toHaveAttribute('href', '/health')
  })

  it('renders nothing when all checks pass', async () => {
    healthMock.mockResolvedValue({ checks: [{ id: 'x', name: 'X', status: 'ok', detail: 'ok' }] })
    renderBanner()
    await waitFor(() => expect(healthMock).toHaveBeenCalled())
    expect(screen.queryByTestId('health-warning-banner')).not.toBeInTheDocument()
  })

  it('renders nothing when the endpoint is unreachable', async () => {
    healthMock.mockRejectedValue(new Error('down'))
    renderBanner()
    await waitFor(() => expect(healthMock).toHaveBeenCalled())
    expect(screen.queryByTestId('health-warning-banner')).not.toBeInTheDocument()
  })
})
