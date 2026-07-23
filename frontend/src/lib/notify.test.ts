import { describe, it, expect, beforeEach, vi } from 'vitest'
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

  it('is a no-op when the Notifications API is unsupported', () => {
    vi.unstubAllGlobals()
    delete (window as unknown as Record<string, unknown>).Notification
    showHumanNeededNotification({ title: 't', body: 'b', taskId: 'task-1' })
    expect(FakeNotification.instances).toHaveLength(0)
  })

  it('is a no-op when the user has not enabled notifications', () => {
    useNotificationsStore.setState({ enabled: false, permission: 'granted' })
    showHumanNeededNotification({ title: 't', body: 'b', taskId: 'task-2' })
    expect(FakeNotification.instances).toHaveLength(0)
  })

  it('is a no-op when permission is not granted', () => {
    useNotificationsStore.setState({ enabled: true, permission: 'default' })
    showHumanNeededNotification({ title: 't', body: 'b', taskId: 'task-3' })
    expect(FakeNotification.instances).toHaveLength(0)
  })

  it('constructs a Notification with the expected title/body/tag when active', () => {
    showHumanNeededNotification({ title: 'Human needed', body: 'Task X needs you', taskId: 'task-4' })
    expect(FakeNotification.instances).toHaveLength(1)
    const n = FakeNotification.instances[0]
    expect(n.title).toBe('Human needed')
    expect(n.body).toBe('Task X needs you')
    expect(n.tag).toBe('human-needed-task-4')
  })

  it('de-dupes repeated notifications for the same task within the TTL window', () => {
    showHumanNeededNotification({ title: 'a', body: 'a', taskId: 'task-5' })
    showHumanNeededNotification({ title: 'b', body: 'b', taskId: 'task-5' })
    expect(FakeNotification.instances).toHaveLength(1)
  })

  it('does not de-dupe notifications for different tasks', () => {
    showHumanNeededNotification({ title: 'a', body: 'a', taskId: 'task-6' })
    showHumanNeededNotification({ title: 'b', body: 'b', taskId: 'task-7' })
    expect(FakeNotification.instances).toHaveLength(2)
  })
})
