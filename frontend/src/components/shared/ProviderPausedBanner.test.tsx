import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import ProviderPausedBanner from './ProviderPausedBanner'
import { useHealthStore } from '../../stores/health'

const healthMock = vi.fn()
const resumeMock = vi.fn()
vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      health: {
        providers: (...a: unknown[]) => healthMock(...a),
        resumeProvider: (...a: unknown[]) => resumeMock(...a),
      },
    },
  }
})

const renderBanner = () => render(<MemoryRouter><ProviderPausedBanner /></MemoryRouter>)

beforeEach(() => {
  useHealthStore.setState({ checks: [], paused: [], loaded: false })
  healthMock.mockReset()
  resumeMock.mockReset()
})

describe('ProviderPausedBanner', () => {
  it('renders nothing when no provider is paused', () => {
    renderBanner()
    expect(screen.queryByTestId('provider-paused-banner')).not.toBeInTheDocument()
  })

  it('names the paused provider, links to Health and resumes on click', async () => {
    useHealthStore.setState({
      paused: [{ provider: 'claude', reason: 'Not logged in', paused_at: '2026-01-01T00:00:00Z' }],
    })
    resumeMock.mockResolvedValue({ resumed: true })
    healthMock.mockResolvedValue({ checks: [], paused_providers: [] })
    renderBanner()

    const banner = screen.getByTestId('provider-paused-banner')
    expect(banner).toHaveTextContent("Claude isn't logged in — dispatch paused")
    expect(screen.getByRole('link', { name: /View health/ })).toHaveAttribute('href', '/settings/health')

    await userEvent.click(screen.getByRole('button', { name: 'Resume' }))
    expect(resumeMock).toHaveBeenCalledWith('claude')
    await waitFor(() => expect(screen.queryByTestId('provider-paused-banner')).not.toBeInTheDocument())
  })
})
