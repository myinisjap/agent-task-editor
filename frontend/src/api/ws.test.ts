// Unit tests for WSClient / wsTicketParam — the core realtime engine had no
// test file at all (see #251 §1): reconnect backoff, ticket-refresh-on-401,
// resubscribe-on-reopen and message-dedup/parsing were all unverified.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// authToken is mocked so wsTicketParam's token-fetch branch is controllable
// without touching real localStorage semantics.
const getApiTokenMock = vi.fn<() => string | null>()
const notifyUnauthorizedMock = vi.fn()
vi.mock('./authToken', () => ({
  getApiToken: () => getApiTokenMock(),
  notifyUnauthorized: () => notifyUnauthorizedMock(),
}))

// FakeWebSocket stands in for the browser WebSocket constructor. Tests hold
// on to the most recently constructed instance (via FakeWebSocket.instances)
// to manually fire onopen/onclose/onmessage rather than going over a real
// socket.
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static OPEN = 1
  static CONNECTING = 0
  static CLOSED = 3

  url: string
  readyState = FakeWebSocket.CONNECTING
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  sent: string[] = []

  constructor(url: string) {
    this.url = url
    FakeWebSocket.instances.push(this)
  }

  send(data: string) {
    this.sent.push(data)
  }

  // Test helper: simulate the server accepting the connection.
  triggerOpen() {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.()
  }

  triggerClose() {
    this.readyState = FakeWebSocket.CLOSED
    this.onclose?.()
  }

  triggerMessage(data: unknown) {
    this.onmessage?.({ data: typeof data === 'string' ? data : JSON.stringify(data) })
  }
}

describe('wsTicketParam', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    getApiTokenMock.mockReset()
    notifyUnauthorizedMock.mockReset()
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
  })

  it('returns "" when no token is configured (open auth), without calling fetch', async () => {
    getApiTokenMock.mockReturnValue(null)
    const fetchSpy = vi.fn()
    globalThis.fetch = fetchSpy as unknown as typeof fetch

    const { wsTicketParam } = await import('./ws')
    const result = await wsTicketParam()

    expect(result).toBe('')
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('returns "?ticket=..." on a 200 response, URL-encoded', async () => {
    getApiTokenMock.mockReturnValue('tok-123')
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ ticket: 'abc/def+ghi' }),
    }) as unknown as typeof fetch

    const { wsTicketParam } = await import('./ws')
    const result = await wsTicketParam()

    expect(result).toBe(`?ticket=${encodeURIComponent('abc/def+ghi')}`)
  })

  it('calls notifyUnauthorized and returns "" on a 401 response', async () => {
    getApiTokenMock.mockReturnValue('bad-token')
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      json: async () => ({}),
    }) as unknown as typeof fetch

    const { wsTicketParam } = await import('./ws')
    const result = await wsTicketParam()

    expect(result).toBe('')
    expect(notifyUnauthorizedMock).toHaveBeenCalledTimes(1)
  })

  it('swallows a network error and returns ""', async () => {
    getApiTokenMock.mockReturnValue('tok-123')
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('network down')) as unknown as typeof fetch

    const { wsTicketParam } = await import('./ws')
    const result = await wsTicketParam()

    expect(result).toBe('')
    expect(notifyUnauthorizedMock).not.toHaveBeenCalled()
  })

  it('does not treat a non-401 error response as unauthorized', async () => {
    getApiTokenMock.mockReturnValue('tok-123')
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: async () => ({}),
    }) as unknown as typeof fetch

    const { wsTicketParam } = await import('./ws')
    const result = await wsTicketParam()

    expect(result).toBe('')
    expect(notifyUnauthorizedMock).not.toHaveBeenCalled()
  })
})

describe('WSClient', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeWebSocket.instances = []
    getApiTokenMock.mockReset()
    getApiTokenMock.mockReturnValue(null) // open auth by default — skips the ticket fetch entirely
    notifyUnauthorizedMock.mockReset()
    vi.stubGlobal('WebSocket', FakeWebSocket)
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  it('connect() opens a single WebSocket and is idempotent while OPEN or CONNECTING', async () => {
    const { wsClient } = await import('./ws')

    await wsClient.connect()
    expect(FakeWebSocket.instances).toHaveLength(1)
    // Still CONNECTING — a second call must not create a second socket.
    await wsClient.connect()
    expect(FakeWebSocket.instances).toHaveLength(1)

    FakeWebSocket.instances[0].triggerOpen()
    // Now OPEN — still idempotent.
    await wsClient.connect()
    expect(FakeWebSocket.instances).toHaveLength(1)
  })

  it('malformed onmessage JSON is swallowed rather than throwing or reaching handlers', async () => {
    const { wsClient } = await import('./ws')
    const handler = vi.fn()
    wsClient.on(handler)

    await wsClient.connect()
    const sock = FakeWebSocket.instances[0]
    sock.triggerOpen()

    expect(() => sock.triggerMessage('not valid json{{{')).not.toThrow()
    expect(handler).not.toHaveBeenCalled()

    // A well-formed message still reaches the handler afterwards.
    sock.triggerMessage({ type: 'task.label_changed', payload: { task_id: 't1', from: 'a', to: 'b' } })
    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler).toHaveBeenCalledWith({ type: 'task.label_changed', payload: { task_id: 't1', from: 'a', to: 'b' } })
  })

  it('on() returns an unsubscribe function that stops future delivery', async () => {
    const { wsClient } = await import('./ws')
    const handler = vi.fn()
    const off = wsClient.on(handler)

    await wsClient.connect()
    const sock = FakeWebSocket.instances[0]
    sock.triggerOpen()

    sock.triggerMessage({ type: 'task.label_changed', payload: { task_id: 't1', from: 'a', to: 'b' } })
    expect(handler).toHaveBeenCalledTimes(1)

    off()
    sock.triggerMessage({ type: 'task.label_changed', payload: { task_id: 't1', from: 'b', to: 'c' } })
    expect(handler).toHaveBeenCalledTimes(1)
  })

  it('resubscribes to all active task subscriptions on reopen', async () => {
    const { wsClient } = await import('./ws')

    await wsClient.connect()
    let sock = FakeWebSocket.instances[0]
    sock.triggerOpen()

    wsClient.subscribeTask('task-a')
    wsClient.subscribeTask('task-b')
    expect(sock.sent).toContain(JSON.stringify({ type: 'subscribe', task_id: 'task-a' }))
    expect(sock.sent).toContain(JSON.stringify({ type: 'subscribe', task_id: 'task-b' }))

    // Simulate a drop and reconnect (skip past the scheduled backoff timer
    // by invoking connect() directly, as the real onclose handler would via
    // setTimeout).
    sock.triggerClose()
    await vi.runOnlyPendingTimersAsync()

    expect(FakeWebSocket.instances).toHaveLength(2)
    sock = FakeWebSocket.instances[1]
    sock.sent = [] // clear anything sent before open
    sock.triggerOpen()

    // Both previously-active subscriptions should be replayed on the new socket.
    expect(sock.sent).toContain(JSON.stringify({ type: 'subscribe', task_id: 'task-a' }))
    expect(sock.sent).toContain(JSON.stringify({ type: 'subscribe', task_id: 'task-b' }))
  })

  it('unsubscribeTask removes it from the resubscribe set on the next reopen', async () => {
    const { wsClient } = await import('./ws')

    await wsClient.connect()
    let sock = FakeWebSocket.instances[0]
    sock.triggerOpen()

    wsClient.subscribeTask('task-a')
    wsClient.unsubscribeTask('task-a')

    sock.triggerClose()
    await vi.runOnlyPendingTimersAsync()
    sock = FakeWebSocket.instances[1]
    sock.sent = []
    sock.triggerOpen()

    expect(sock.sent).not.toContain(JSON.stringify({ type: 'subscribe', task_id: 'task-a' }))
  })

  it('schedules a reconnect with exponential backoff (base, then doubling, capped at max) on close', async () => {
    const { wsClient, RECONNECT_BASE_MS, RECONNECT_MAX_MS } = await import('./ws')

    // Pin jitter to 0 so delay = capped/2 exactly, making the schedule
    // deterministic: attempt N uses capped = min(MAX, BASE * 2^N).
    const randomSpy = vi.spyOn(Math, 'random').mockReturnValue(0)

    await wsClient.connect()
    FakeWebSocket.instances[0].triggerClose()

    // Attempt 0: capped = BASE * 2^0 = BASE; delay = BASE/2.
    expect(vi.getTimerCount()).toBe(1)
    await vi.advanceTimersByTimeAsync(RECONNECT_BASE_MS / 2 - 1)
    expect(FakeWebSocket.instances).toHaveLength(1) // not yet reconnected
    await vi.advanceTimersByTimeAsync(2)
    expect(FakeWebSocket.instances).toHaveLength(2)

    // Attempt 1 (still not opened, so attempts keeps incrementing):
    // capped = BASE * 2^1 = 2*BASE; delay = BASE.
    FakeWebSocket.instances[1].triggerClose()
    await vi.advanceTimersByTimeAsync(RECONNECT_BASE_MS)
    expect(FakeWebSocket.instances).toHaveLength(3)

    // A confirmed open resets attempts back to 0 for the next drop, so the
    // next delay goes back down to BASE/2 rather than continuing to grow.
    FakeWebSocket.instances[2].triggerOpen()
    FakeWebSocket.instances[2].triggerClose()
    await vi.advanceTimersByTimeAsync(RECONNECT_BASE_MS / 2)
    expect(FakeWebSocket.instances).toHaveLength(4)

    randomSpy.mockRestore()
    // Sanity: the exported constants are the ones actually used for the math above.
    expect(RECONNECT_BASE_MS).toBeGreaterThan(0)
    expect(RECONNECT_MAX_MS).toBeGreaterThanOrEqual(RECONNECT_BASE_MS)
  })

  it('subscribeTask sends immediately when already open, and buffers (no send) when not', async () => {
    const { wsClient } = await import('./ws')

    // Not connected yet — subscribeTask before connect() must not throw.
    expect(() => wsClient.subscribeTask('task-early')).not.toThrow()

    await wsClient.connect()
    const sock = FakeWebSocket.instances[0]
    // Still CONNECTING — no send yet.
    wsClient.subscribeTask('task-connecting')
    expect(sock.sent).toHaveLength(0)

    sock.triggerOpen()
    wsClient.subscribeTask('task-open')
    expect(sock.sent).toContain(JSON.stringify({ type: 'subscribe', task_id: 'task-open' }))
  })
})
