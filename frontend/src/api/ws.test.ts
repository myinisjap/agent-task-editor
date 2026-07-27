import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// Minimal WebSocket stand-in: records instances so tests can drive
// onopen/onclose from outside, without a real network connection.
class MockWebSocket {
  static instances: MockWebSocket[] = []
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3

  readyState = MockWebSocket.CONNECTING
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: MessageEvent) => void) | null = null
  sent: string[] = []
  url: string

  constructor(url: string) {
    this.url = url
    MockWebSocket.instances.push(this)
  }

  send(data: string) {
    this.sent.push(data)
  }

  close() {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  }

  // Test helper — simulate the server accepting the connection.
  triggerOpen() {
    this.readyState = MockWebSocket.OPEN
    this.onopen?.()
  }

  // Test helper — simulate an unexpected disconnect.
  triggerClose() {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  }
}

describe('wsClient reconnect backoff', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
    // jsdom doesn't implement fetch by default in this suite's setup for
    // ws-ticket; no token is stored so wsTicketParam() short-circuits
    // without calling fetch at all (see authToken.ts / ws.ts).
    localStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  it('reconnects with growing, capped, jittered delays and resets attempts on open', async () => {
    const { wsClient, RECONNECT_BASE_MS, RECONNECT_MAX_MS } = await import('./ws')

    await wsClient.connect()
    expect(MockWebSocket.instances).toHaveLength(1)
    expect(wsClient.getStatus()).toBe('connecting')

    // First close — schedules a reconnect at ~[base/2, base].
    MockWebSocket.instances[0].triggerClose()
    expect(wsClient.getStatus()).toBe('closed')

    await vi.advanceTimersByTimeAsync(RECONNECT_BASE_MS)
    expect(MockWebSocket.instances).toHaveLength(2)

    // Second close without an intervening open — backoff must have grown
    // (attempts incremented), so advancing only by the base delay must NOT
    // be enough to trigger the third connect yet.
    MockWebSocket.instances[1].triggerClose()
    await vi.advanceTimersByTimeAsync(RECONNECT_BASE_MS)
    expect(MockWebSocket.instances).toHaveLength(2)

    // But advancing well past the (still-capped) max possible delay does.
    await vi.advanceTimersByTimeAsync(RECONNECT_MAX_MS)
    expect(MockWebSocket.instances).toHaveLength(3)

    // A confirmed open resets the attempt counter, so the next close/reconnect
    // cycle goes back to a short (~base) delay instead of continuing to grow.
    MockWebSocket.instances[2].triggerOpen()
    expect(wsClient.getStatus()).toBe('open')

    MockWebSocket.instances[2].triggerClose()
    await vi.advanceTimersByTimeAsync(RECONNECT_BASE_MS)
    expect(MockWebSocket.instances).toHaveLength(4)
  })

  it('caps the reconnect delay so it never exceeds RECONNECT_MAX_MS', async () => {
    const { wsClient, RECONNECT_MAX_MS } = await import('./ws')

    await wsClient.connect()
    // Drive many consecutive failures to push attempts well past the point
    // where 2^attempts would exceed the cap without Math.min.
    for (let i = 0; i < 10; i++) {
      const inst = MockWebSocket.instances[MockWebSocket.instances.length - 1]
      inst.triggerClose()
      await vi.advanceTimersByTimeAsync(RECONNECT_MAX_MS)
    }
    // Every scheduled reconnect fired within RECONNECT_MAX_MS of the previous
    // close, i.e. the delay never exceeded the cap.
    expect(MockWebSocket.instances.length).toBe(11)
  })

  it('reports connection status transitions', async () => {
    const { wsClient } = await import('./ws')
    const statuses: string[] = []
    const unsubscribe = wsClient.onStatusChange((s) => statuses.push(s))

    await wsClient.connect()
    expect(statuses).toEqual(['connecting'])

    MockWebSocket.instances[0].triggerOpen()
    expect(statuses).toEqual(['connecting', 'open'])
    expect(wsClient.getStatus()).toBe('open')

    MockWebSocket.instances[0].triggerClose()
    expect(statuses).toEqual(['connecting', 'open', 'closed'])
    expect(wsClient.getStatus()).toBe('closed')

    unsubscribe()
  })
})
