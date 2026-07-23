import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import NavSidebar from './NavSidebar'
import { useNotificationsStore } from '../../stores/notifications'

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

  it('renders all top-level and grouped destinations', () => {
    render(
      <MemoryRouter initialEntries={['/health']}>
        <NavSidebar />
      </MemoryRouter>,
    )

    // Dashboard is always visible (top-level link, not inside a group).
    expect(screen.getByRole('link', { name: 'Dashboard' })).toBeInTheDocument()

    // The group containing the active route (System -> Health) is expanded
    // by default, so its link is visible without any interaction.
    expect(screen.getByRole('link', { name: 'Health' })).toBeInTheDocument()

    // A group that does not contain the active route starts collapsed, so
    // its links are not present in the DOM.
    expect(screen.queryByRole('link', { name: 'Board' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Work/i })).toHaveAttribute('aria-expanded', 'false')
  })

  it('toggles a collapsed group open to reveal its links', () => {
    render(
      <MemoryRouter initialEntries={['/health']}>
        <NavSidebar />
      </MemoryRouter>,
    )

    const workToggle = screen.getByRole('button', { name: /Work/i })
    expect(workToggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('link', { name: 'Board' })).not.toBeInTheDocument()

    fireEvent.click(workToggle)

    expect(workToggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('link', { name: 'Board' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Chat' })).toBeInTheDocument()
  })

  it('closes the mobile drawer when a nav link is clicked', () => {
    render(
      <MemoryRouter initialEntries={['/health']}>
        <NavSidebar />
      </MemoryRouter>,
    )

    // Open the mobile drawer.
    fireEvent.click(screen.getByRole('button', { name: 'Open menu' }))

    const healthLink = screen.getByRole('link', { name: 'Health' })
    fireEvent.click(healthLink)

    // The hamburger button should be usable again (i.e. drawer closed state
    // is reflected by the "Open menu" button still being present/clickable,
    // and the backdrop being removed).
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
        <MemoryRouter initialEntries={['/health']}>
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
        <MemoryRouter initialEntries={['/health']}>
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
        <MemoryRouter initialEntries={['/health']}>
          <NavSidebar />
        </MemoryRouter>,
      )

      const toggle = screen.getByRole('button', { name: 'Enable notifications' })
      expect(toggle).toBeDisabled()
      expect(screen.getByText('Notifications blocked')).toBeInTheDocument()
    })

    it('hides the toggle entirely when the Notifications API is unsupported', () => {
      vi.unstubAllGlobals()
      delete (window as unknown as Record<string, unknown>).Notification

      render(
        <MemoryRouter initialEntries={['/health']}>
          <NavSidebar />
        </MemoryRouter>,
      )

      expect(screen.queryByRole('button', { name: 'Enable notifications' })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Disable notifications' })).not.toBeInTheDocument()
    })
  })
})
