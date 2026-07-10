import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getApiToken, setApiToken, clearApiToken, authHeaders, onUnauthorized, notifyUnauthorized } from './authToken'

const STORAGE_KEY = 'ate_api_token'

// The default vitest environment here is `node`, which has no `localStorage`
// global (unlike a browser/jsdom environment). Rather than pull in jsdom as
// a new dependency just for this one module, provide a minimal in-memory
// localStorage polyfill for the duration of this test file.
class MemoryStorage implements Storage {
  private store = new Map<string, string>()
  get length() { return this.store.size }
  clear() { this.store.clear() }
  getItem(key: string) { return this.store.has(key) ? this.store.get(key)! : null }
  key(index: number) { return Array.from(this.store.keys())[index] ?? null }
  removeItem(key: string) { this.store.delete(key) }
  setItem(key: string, value: string) { this.store.set(key, value) }
}
vi.stubGlobal('localStorage', new MemoryStorage())

describe('authToken', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.unstubAllEnvs()
  })

  it('returns null when nothing is stored and no build-time default', () => {
    vi.stubEnv('VITE_API_TOKEN', '')
    expect(getApiToken()).toBeNull()
  })

  it('seeds localStorage from VITE_API_TOKEN once when nothing is stored', () => {
    vi.stubEnv('VITE_API_TOKEN', 'dev-default-token')
    expect(getApiToken()).toBe('dev-default-token')
    expect(localStorage.getItem(STORAGE_KEY)).toBe('dev-default-token')

    // Subsequent reads come from storage, not re-evaluating the env var.
    vi.stubEnv('VITE_API_TOKEN', 'different-token')
    expect(getApiToken()).toBe('dev-default-token')
  })

  it('setApiToken persists and getApiToken reads it back', () => {
    setApiToken('my-token')
    expect(getApiToken()).toBe('my-token')
    expect(authHeaders()).toEqual({ Authorization: 'Bearer my-token' })
  })

  it('clearApiToken removes the stored token', () => {
    setApiToken('my-token')
    clearApiToken()
    vi.stubEnv('VITE_API_TOKEN', '')
    expect(getApiToken()).toBeNull()
    expect(authHeaders()).toEqual({})
  })

  it('notifyUnauthorized clears the token and fires listeners', () => {
    setApiToken('my-token')
    const listener = vi.fn()
    const unsubscribe = onUnauthorized(listener)

    notifyUnauthorized()

    expect(localStorage.getItem(STORAGE_KEY)).toBeNull()
    expect(listener).toHaveBeenCalledTimes(1)

    unsubscribe()
    notifyUnauthorized()
    expect(listener).toHaveBeenCalledTimes(1)
  })
})
