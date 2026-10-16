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

  it('closes the mobile drawer when a nav link is clicked', () => {
    render(
      <MemoryRouter initialEntries={['/board']}>
        <NavSidebar />
      </MemoryRouter>,
    )

    // Open the mobile drawer.
    fireEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    expect(screen.getByRole('button', { name: 'Close menu' }).closest('aside')?.className)
      .toEqual(expect.stringContaining('translate-x-0'))

    fireEvent.click(screen.getByRole('link', { name: 'Settings' }))

    expect(screen.queryByRole('button', { name: 'Close menu' })?.closest('aside')?.className)
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

    it('shows a disabled, blocked state when permission is denied', () => {
      useNotificationsStore.setState({ enabled: false, permission: 'denied' })
      render(
        <MemoryRouter initialEntries={['/settings/health']}>
          <NavSidebar />
        </MemoryRouter>,
      )

      const toggle = screen.getByRole('button', { name: 'Enable notifications' })
      expect(toggle).toBeDisabled()
      expect(screen.getByText('Notifications blocked')).toBeInTheDocument()
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
