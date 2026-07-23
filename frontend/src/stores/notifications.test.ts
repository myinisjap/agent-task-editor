import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useNotificationsStore, notificationsActive, notificationsSupported } from './notifications'

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
})
