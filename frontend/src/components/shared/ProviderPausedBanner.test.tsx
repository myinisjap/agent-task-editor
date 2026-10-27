import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import ProviderPausedBanner from './ProviderPausedBanner'
import { useHealthStore } from '../../stores/health'
import { pausedSignature } from '../../lib/healthTiers'

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
  sessionStorage.clear()
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

  const claude = { provider: 'claude', reason: 'Not logged in', paused_at: '2026-01-01T00:00:00Z' }
  const codex = { provider: 'codex_cli', reason: 'No key', paused_at: '2026-01-02T00:00:00Z' }

  it('shows exactly one alert in the demo state (paused + warnings)', () => {
    useHealthStore.setState({
      paused: [claude],
      checks: ['claude_cli', 'mcp_sidecar', 'api_auth', 'repo_base_dir', 'auto_backup'].map(
        (id) => ({ id, name: id, status: 'warn', detail: 'x' }) as never,
      ),
    })
    renderBanner()
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.queryByTestId('health-warning-banner')).not.toBeInTheDocument()
  })

  it('dismisses for the session and reappears when the paused set changes', async () => {
    useHealthStore.setState({ paused: [claude] })
    const { unmount } = renderBanner()
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss for this session' }))
    expect(screen.queryByTestId('provider-paused-banner')).not.toBeInTheDocument()
    expect(sessionStorage.getItem('ate:status-banner-dismissed')).toBe(pausedSignature([claude]))
    unmount()

    const second = renderBanner()
    expect(screen.queryByTestId('provider-paused-banner')).not.toBeInTheDocument()
    second.unmount()

    useHealthStore.setState({ paused: [claude, codex] })
    renderBanner()
    expect(screen.getByTestId('provider-paused-banner')).toHaveTextContent('Claude, Codex aren')
  })

  it('reappears for the same provider paused again later', () => {
    sessionStorage.setItem('ate:status-banner-dismissed', pausedSignature([claude]))
    useHealthStore.setState({ paused: [{ ...claude, paused_at: '2026-02-01T00:00:00Z' }] })
    renderBanner()
    expect(screen.getByTestId('provider-paused-banner')).toBeInTheDocument()
  })

  it('toggles details and resumes all providers', async () => {
    useHealthStore.setState({ paused: [claude, codex] })
    resumeMock.mockResolvedValue({ resumed: true })
    healthMock.mockResolvedValue({ checks: [], paused_providers: [claude, codex] })
    renderBanner()
    const details = screen.getByRole('button', { name: 'Details' })
    expect(details).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(details)
    expect(details).toHaveAttribute('aria-expanded', 'true')
    await userEvent.click(screen.getByRole('button', { name: 'Resume all' }))
    expect(resumeMock).toHaveBeenCalledWith('claude')
    expect(resumeMock).toHaveBeenCalledWith('codex_cli')
  })
})
