import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useNotificationsStore, notificationsActive, notificationsSupported, notificationsUnavailableReason } from './notifications'

class FakeNotification {
  static permission: NotificationPermission = 'default'
  static requestPermission = vi.fn(async () => FakeNotification.permission)
}

describe('notifications store', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.unstubAllGlobals()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('reports unsupported when the Notification global is absent', () => {
    expect(notificationsSupported()).toBe(false)
    expect(notificationsActive()).toBe(false)
  })

  it('defaults to disabled even when supported', () => {
    FakeNotification.permission = 'granted'
    vi.stubGlobal('Notification', FakeNotification)
    useNotificationsStore.setState({ enabled: false, permission: 'granted' })
    expect(notificationsActive()).toBe(false)
  })

  it('requestPermission enables only when permission is granted', async () => {
    FakeNotification.permission = 'granted'
    vi.stubGlobal('Notification', FakeNotification)
    useNotificationsStore.setState({ enabled: false, permission: 'default' })

    await useNotificationsStore.getState().requestPermission()

    expect(useNotificationsStore.getState().enabled).toBe(true)
    expect(useNotificationsStore.getState().permission).toBe('granted')
    expect(notificationsActive()).toBe(true)
    expect(localStorage.getItem('notifications.enabled')).toBe('1')
  })

  it('requestPermission leaves enabled false when permission is denied', async () => {
    FakeNotification.permission = 'denied'
    vi.stubGlobal('Notification', FakeNotification)
    useNotificationsStore.setState({ enabled: false, permission: 'default' })

    await useNotificationsStore.getState().requestPermission()

    expect(useNotificationsStore.getState().enabled).toBe(false)
    expect(useNotificationsStore.getState().permission).toBe('denied')
    expect(notificationsActive()).toBe(false)
  })

  it('requestPermission is a no-op when unsupported', async () => {
    useNotificationsStore.setState({ enabled: false, permission: 'default' })
    await useNotificationsStore.getState().requestPermission()
    expect(useNotificationsStore.getState().enabled).toBe(false)
  })

  it('toggle() disables when already enabled without re-requesting permission', () => {
    FakeNotification.permission = 'granted'
    vi.stubGlobal('Notification', FakeNotification)
    useNotificationsStore.setState({ enabled: true, permission: 'granted' })

    useNotificationsStore.getState().toggle()

    expect(useNotificationsStore.getState().enabled).toBe(false)
    expect(localStorage.getItem('notifications.enabled')).toBe('0')
  })

  describe('notificationsUnavailableReason', () => {
    const realUA = navigator.userAgent

    afterEach(() => {
      Object.defineProperty(navigator, 'userAgent', { configurable: true, value: realUA })
      delete (window as unknown as Record<string, unknown>).isSecureContext
    })

    it('is null when the Notifications API is available in a secure context', () => {
      vi.stubGlobal('Notification', FakeNotification)
      expect(notificationsUnavailableReason()).toBeNull()
    })

    it('reports insecure over plain HTTP even when the API exists', () => {
      vi.stubGlobal('Notification', FakeNotification)
      Object.defineProperty(window, 'isSecureContext', { configurable: true, value: false })
      expect(notificationsUnavailableReason()).toBe('insecure')
    })

    it('asks iPhone Safari users to add the app to the Home Screen', () => {
      Object.defineProperty(navigator, 'userAgent', {
        configurable: true,
        value: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
      })
      expect(notificationsUnavailableReason()).toBe('ios-install')
    })

    it('reports unsupported for other browsers without the API', () => {
      expect(notificationsUnavailableReason()).toBe('unsupported')
    })
  })
})
