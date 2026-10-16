import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { showHumanNeededNotification } from './notify'
import { useNotificationsStore } from '../stores/notifications'

class FakeNotification {
  static permission: NotificationPermission = 'granted'
  static requestPermission = vi.fn(async () => FakeNotification.permission)
  static instances: FakeNotification[] = []

  title: string
  body?: string
  tag?: string
  icon?: string
  onclick: (() => void) | null = null

  constructor(title: string, options?: NotificationOptions) {
    this.title = title
    this.body = options?.body
    this.tag = options?.tag
    this.icon = options?.icon
    FakeNotification.instances.push(this)
  }
}

describe('showHumanNeededNotification', () => {
  beforeEach(() => {
    FakeNotification.instances = []
    FakeNotification.permission = 'granted'
    vi.stubGlobal('Notification', FakeNotification)
    useNotificationsStore.setState({ enabled: true, permission: 'granted' })
  })

  it('is a no-op when the Notifications API is unsupported', async () => {
    vi.unstubAllGlobals()
    delete (window as unknown as Record<string, unknown>).Notification
    await showHumanNeededNotification({ title: 't', body: 'b', taskId: 'task-1' })
    expect(FakeNotification.instances).toHaveLength(0)
  })

  it('is a no-op when the user has not enabled notifications', async () => {
    useNotificationsStore.setState({ enabled: false, permission: 'granted' })
    await showHumanNeededNotification({ title: 't', body: 'b', taskId: 'task-2' })
    expect(FakeNotification.instances).toHaveLength(0)
  })

  it('is a no-op when permission is not granted', async () => {
    useNotificationsStore.setState({ enabled: true, permission: 'default' })
    await showHumanNeededNotification({ title: 't', body: 'b', taskId: 'task-3' })
    expect(FakeNotification.instances).toHaveLength(0)
  })

  it('constructs a Notification with the expected title/body/tag when active', async () => {
    await showHumanNeededNotification({ title: 'Human needed', body: 'Task X needs you', taskId: 'task-4' })
    expect(FakeNotification.instances).toHaveLength(1)
    const n = FakeNotification.instances[0]
    expect(n.title).toBe('Human needed')
    expect(n.body).toBe('Task X needs you')
    expect(n.tag).toBe('human-needed-task-4')
  })

  describe('with a service worker registration', () => {
    const showNotification = vi.fn(async () => {})

    beforeEach(() => {
      showNotification.mockReset()
      Object.defineProperty(navigator, 'serviceWorker', {
        configurable: true,
        value: { getRegistration: async () => ({ showNotification }) },
      })
    })

    afterEach(() => {
      delete (navigator as unknown as Record<string, unknown>).serviceWorker
    })

    it('shows the notification through the service worker (required on mobile)', async () => {
      await showHumanNeededNotification({ title: 'Human needed', body: 'Task Y', taskId: 'task-sw-1' })
      expect(showNotification).toHaveBeenCalledTimes(1)
      expect(showNotification).toHaveBeenCalledWith('Human needed', expect.objectContaining({
        body: 'Task Y',
        tag: 'human-needed-task-sw-1',
        data: { url: `${window.location.origin}/tasks/task-sw-1` },
      }))
      expect(FakeNotification.instances).toHaveLength(0)
    })

    it('falls back to the Notification constructor if showNotification fails', async () => {
      showNotification.mockRejectedValueOnce(new Error('nope'))
      await showHumanNeededNotification({ title: 't', body: 'b', taskId: 'task-sw-2' })
      expect(FakeNotification.instances).toHaveLength(1)
    })
  })

  it('de-dupes repeated notifications for the same task within the TTL window', async () => {
    await showHumanNeededNotification({ title: 'a', body: 'a', taskId: 'task-5' })
    await showHumanNeededNotification({ title: 'b', body: 'b', taskId: 'task-5' })
    expect(FakeNotification.instances).toHaveLength(1)
  })

  it('does not de-dupe notifications for different tasks', async () => {
    await showHumanNeededNotification({ title: 'a', body: 'a', taskId: 'task-6' })
    await showHumanNeededNotification({ title: 'b', body: 'b', taskId: 'task-7' })
    expect(FakeNotification.instances).toHaveLength(2)
  })
})
