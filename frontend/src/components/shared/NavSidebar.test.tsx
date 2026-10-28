import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import NavSidebar from './NavSidebar'
import { useNotificationsStore } from '../../stores/notifications'
import { useHealthStore } from '../../stores/health'

class FakeNotification {
  static permission: NotificationPermission = 'default'
  static requestPermission = vi.fn(async () => FakeNotification.permission)
}

describe('NavSidebar', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  afterEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('renders the trimmed top-level destinations', () => {
    render(
      <MemoryRouter initialEntries={['/board']}>
        <NavSidebar />
      </MemoryRouter>,
    )

    for (const name of ['Inbox', 'Board', 'Chat', 'Pipeline', 'Repos', 'Settings']) {
      expect(screen.getByRole('link', { name })).toBeInTheDocument()
    }
    // Folded into Pipeline / Settings tabs, so no longer top-level links.
    for (const name of ['Workflow', 'Agents', 'Providers', 'Pricing', 'Templates', 'Intake Rules', 'Health']) {
      expect(screen.queryByRole('link', { name })).not.toBeInTheDocument()
    }
    expect(screen.getByRole('link', { name: 'Pipeline' })).toHaveAttribute('href', '/pipeline')
    expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/settings')
  })

  it('keeps the Overview dashboard reachable inside a collapsed Insights group', () => {
    render(
      <MemoryRouter initialEntries={['/board']}>
        <NavSidebar />
      </MemoryRouter>,
    )

    const toggle = screen.getByRole('button', { name: /Insights/i })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('link', { name: 'Overview' })).not.toBeInTheDocument()

    fireEvent.click(toggle)

    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('link', { name: 'Overview' })).toHaveAttribute('href', '/dashboard')
    expect(screen.getByRole('link', { name: 'Cost & Usage' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Performance' })).toBeInTheDocument()
  })

  it('shows a failing-health dot on the Settings item', () => {
    useHealthStore.setState({
      checks: [{ name: 'claude_cli', status: 'error', message: 'not authenticated' }] as never,
    })
    render(
      <MemoryRouter initialEntries={['/board']}>
        <NavSidebar />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: /Settings/ })).toContainElement(
      screen.getByRole('img', { name: 'Health check failing' }),
    )
    useHealthStore.setState({ checks: [] })
  })

  it('shows a count pill and ignores snoozed advice', () => {
    useHealthStore.setState({
      checks: [
        { id: 'mcp_sidecar', name: 'MCP', status: 'warn', detail: '' },
        { id: 'auto_backup', name: 'Backup', status: 'warn', detail: '' },
      ] as never,
      snoozed: ['auto_backup'],
      paused: [],
    })
    render(
      <MemoryRouter initialEntries={['/board']}>
        <NavSidebar />
      </MemoryRouter>,
    )
    const dot = screen.getByRole('img', { name: 'Health check warning' })
    expect(dot).toHaveTextContent('1')
    useHealthStore.setState({ checks: [], snoozed: [] })
  })

  it('closes the mobile drawer when a nav link is clicked', () => {
    const onOpenChange = vi.fn()
    render(
      <MemoryRouter initialEntries={['/board']}>
        <NavSidebar open onOpenChange={onOpenChange} />
      </MemoryRouter>,
    )

    expect(screen.getByRole('button', { name: 'Close menu' }).closest('aside')?.className)
      .toEqual(expect.stringContaining('translate-x-0'))

    fireEvent.click(screen.getByRole('link', { name: 'Settings' }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('is off-screen on mobile by default and has no floating hamburger', () => {
    render(
      <MemoryRouter initialEntries={['/board']}>
        <NavSidebar />
      </MemoryRouter>,
    )
    expect(screen.queryByRole('button', { name: 'Open menu' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Close menu' }).closest('aside')?.className)
      .toEqual(expect.stringContaining('-translate-x-full'))
  })

  describe('notifications toggle', () => {
    beforeEach(() => {
      FakeNotification.permission = 'default'
      FakeNotification.requestPermission.mockClear()
      vi.stubGlobal('Notification', FakeNotification)
      useNotificationsStore.setState({ enabled: false, permission: 'default' })
    })

    it('requests permission and enables on click when currently off', async () => {
      FakeNotification.permission = 'granted'
      render(
        <MemoryRouter initialEntries={['/settings/health']}>
          <NavSidebar />
        </MemoryRouter>,
      )

      const toggle = screen.getByRole('button', { name: 'Enable notifications' })
      fireEvent.click(toggle)

      await vi.waitFor(() => {
        expect(useNotificationsStore.getState().enabled).toBe(true)
      })
      expect(FakeNotification.requestPermission).toHaveBeenCalled()
    })

    it('disables without re-requesting permission when currently on', () => {
      useNotificationsStore.setState({ enabled: true, permission: 'granted' })
      render(
        <MemoryRouter initialEntries={['/settings/health']}>
          <NavSidebar />
        </MemoryRouter>,
      )

      const toggle = screen.getByRole('button', { name: 'Disable notifications' })
      fireEvent.click(toggle)

      expect(useNotificationsStore.getState().enabled).toBe(false)
      expect(FakeNotification.requestPermission).not.toHaveBeenCalled()
    })

    it('shows an actionable blocked state that explains how to re-enable', () => {
      useNotificationsStore.setState({ enabled: false, permission: 'denied' })
      render(
        <MemoryRouter initialEntries={['/settings/health']}>
          <NavSidebar />
        </MemoryRouter>,
      )

      const btn = screen.getByRole('button', { name: /Notifications blocked/ })
      expect(btn).toBeEnabled()
      expect(btn).toHaveAttribute('aria-expanded', 'false')
      expect(screen.queryByRole('dialog', { name: 'How to enable notifications' })).not.toBeInTheDocument()
      fireEvent.click(btn)
      expect(btn).toHaveAttribute('aria-expanded', 'true')
      expect(screen.getByRole('dialog', { name: 'How to enable notifications' })).toHaveTextContent(/site-settings icon/)
      expect(FakeNotification.requestPermission).not.toHaveBeenCalled()
      fireEvent.keyDown(document, { key: 'Escape' })
      expect(screen.queryByRole('dialog', { name: 'How to enable notifications' })).not.toBeInTheDocument()
    })

    it('explains instead of offering the toggle when the Notifications API is unsupported', () => {
      vi.unstubAllGlobals()
      delete (window as unknown as Record<string, unknown>).Notification

      render(
        <MemoryRouter initialEntries={['/settings/health']}>
          <NavSidebar />
        </MemoryRouter>,
      )

      expect(screen.queryByRole('button', { name: 'Enable notifications' })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Disable notifications' })).not.toBeInTheDocument()
      expect(screen.getByText('Notifications unsupported')).toBeInTheDocument()
    })

    it('says HTTPS is needed when the page is not a secure context', () => {
      Object.defineProperty(window, 'isSecureContext', { configurable: true, value: false })
      try {
        render(
          <MemoryRouter initialEntries={['/settings/health']}>
            <NavSidebar />
          </MemoryRouter>,
        )
        expect(screen.queryByRole('button', { name: 'Enable notifications' })).not.toBeInTheDocument()
        expect(screen.getByText('Notifications need HTTPS')).toBeInTheDocument()
      } finally {
        delete (window as unknown as Record<string, unknown>).isSecureContext
      }
    })
  })
})
