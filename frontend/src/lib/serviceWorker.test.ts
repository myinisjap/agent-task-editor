import { describe, it, expect, afterEach, vi } from 'vitest'
import { registerServiceWorker } from './serviceWorker'

describe('registerServiceWorker', () => {
  afterEach(() => {
    delete (navigator as unknown as Record<string, unknown>).serviceWorker
    delete (window as unknown as Record<string, unknown>).isSecureContext
  })

  it('registers sw.js at the app base in a secure context', () => {
    const register = vi.fn(async () => ({}))
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: { register } })
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true })
    registerServiceWorker()
    expect(register).toHaveBeenCalledWith('/sw.js', { scope: '/' })
  })

  it('does nothing over plain HTTP', () => {
    const register = vi.fn(async () => ({}))
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: { register } })
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: false })
    registerServiceWorker()
    expect(register).not.toHaveBeenCalled()
  })

  it('does nothing when service workers are unsupported', () => {
    expect(() => registerServiceWorker()).not.toThrow()
  })
})
