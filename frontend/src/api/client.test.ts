import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { setApiToken, clearApiToken } from './authToken'

// Regression test for #138 — a request going out through client.ts's
// request()/requestWithHeaders() helpers must carry the Authorization header
// when a runtime API token is stored (see authToken.ts). The WS client
// (ws.ts) and two ad-hoc raw fetch() call sites (WorkflowPage's YAML export,
// HealthPage's backup download) already did this manually; the shared
// `request()`/`requestWithHeaders()` helpers used by every other `api.*`
// call (tasks, repos, workflows, agents, ...) omitted it entirely — fixed
// alongside this test (see authedRawFetch()/authHeaders() in client.ts).
describe('client.ts Authorization header (#138)', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    clearApiToken()
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({}),
      headers: new Headers(),
    })
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    clearApiToken()
  })

  it('sends an Authorization: Bearer header when a runtime token is stored', async () => {
    setApiToken('secret-token')
    const { api } = await import('./client')

    await api.repos.list()

    expect(globalThis.fetch).toHaveBeenCalledTimes(1)
    const [, init] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const headers = new Headers(init?.headers as HeadersInit)
    expect(headers.get('Authorization')).toBe('Bearer secret-token')
  })

  it('sends the header via requestWithHeaders() too (e.g. tasks.list, a paginated endpoint)', async () => {
    setApiToken('secret-token')
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => [],
      headers: new Headers(),
    })
    const { api } = await import('./client')

    await api.tasks.list()

    const [, init] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const headers = new Headers(init?.headers as HeadersInit)
    expect(headers.get('Authorization')).toBe('Bearer secret-token')
  })

  it('omits the Authorization header when no runtime token is stored (unauthenticated mode still works)', async () => {
    const { api } = await import('./client')

    await api.repos.list()

    const [, init] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const headers = new Headers(init?.headers as HeadersInit)
    expect(headers.has('Authorization')).toBe(false)
  })

  // A caller that passes its own `init.headers` (e.g. workflows.updateYaml/
  // importYaml, which override Content-Type to 'application/yaml') must not
  // lose the Authorization header in the merge — request()'s object-spread
  // order previously clobbered the whole merged headers object with
  // init.headers whenever a caller supplied one (a latent bug that predates
  // authHeaders() but only became observable once there was an
  // Authorization header to lose).
  it('keeps the Authorization header when the caller also passes a custom Content-Type header', async () => {
    setApiToken('secret-token')
    const { api } = await import('./client')

    await api.workflows.updateYaml('wf-1', 'name: test')

    const [, init] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const headers = new Headers(init?.headers as HeadersInit)
    expect(headers.get('Authorization')).toBe('Bearer secret-token')
    expect(headers.get('Content-Type')).toBe('application/yaml')
  })
})
