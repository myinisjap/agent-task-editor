// Global Vitest/Testing Library setup, loaded via vite.config.ts's
// test.setupFiles for every *.test.ts(x) file.
import '@testing-library/jest-dom/vitest'
import { afterEach, vi } from 'vitest'
import { cleanup } from '@testing-library/react'

// Newer Node versions (22.4+) ship their own experimental global
// `localStorage`/`sessionStorage` (see `node --help` -> `--webstorage`).
// Depending on the exact Node/jsdom/Vitest version combination, that global
// can end up shadowing — or otherwise breaking — jsdom's `window.localStorage`
// (it silently resolves to `undefined` instead of a working Storage object,
// with no thrown error, so `localStorage.getItem(...)` fails with "Cannot
// read properties of undefined"). Every test that touches token storage
// (api/authToken.ts, and anything that calls it transitively, e.g. the authed
// fetch helpers in api/client.ts) then fails regardless of whether the code
// under test is correct. Provide our own minimal, synchronous, in-memory
// Storage polyfill and force both globals to it so tests are deterministic
// across Node/jsdom versions and CLI flags.
class MemoryStorage implements Storage {
  private store = new Map<string, string>()

  get length(): number {
    return this.store.size
  }

  clear(): void {
    this.store.clear()
  }

  getItem(key: string): string | null {
    return this.store.has(key) ? this.store.get(key)! : null
  }

  key(index: number): string | null {
    return Array.from(this.store.keys())[index] ?? null
  }

  removeItem(key: string): void {
    this.store.delete(key)
  }

  setItem(key: string, value: string): void {
    this.store.set(key, String(value))
  }
}

for (const target of [globalThis, window] as const) {
  Object.defineProperty(target, 'localStorage', {
    configurable: true,
    value: new MemoryStorage(),
  })
  Object.defineProperty(target, 'sessionStorage', {
    configurable: true,
    value: new MemoryStorage(),
  })
}

// Testing Library doesn't auto-cleanup between tests when globals are on in
// some setups — be explicit so component trees don't leak across tests.
afterEach(() => {
  cleanup()
})

// jsdom doesn't implement matchMedia; useIsMobile() (and anything else that
// probes viewport width) needs a stub or it throws "matchMedia is not a
// function". Default: never "mobile" (matches: false) unless a test
// overrides it via vi.stubGlobal('matchMedia', ...) or a fresh
// Object.defineProperty.
if (!window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(), // deprecated
      removeListener: vi.fn(), // deprecated
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }),
  })
}

// jsdom (as of the version pinned here) does not implement PointerEvent.
// @dnd-kit's sensors listen for pointer events, so simulating a drag via
// fireEvent.pointerDown/Move/Up needs a minimal polyfill.
if (typeof window.PointerEvent === 'undefined') {
  class PointerEventPolyfill extends MouseEvent {
    pointerId: number
    width: number
    height: number
    pressure: number
    tangentialPressure: number
    tiltX: number
    tiltY: number
    twist: number
    pointerType: string
    isPrimary: boolean

    constructor(type: string, params: PointerEventInit = {}) {
      super(type, params)
      this.pointerId = params.pointerId ?? 0
      this.width = params.width ?? 1
      this.height = params.height ?? 1
      this.pressure = params.pressure ?? 0
      this.tangentialPressure = params.tangentialPressure ?? 0
      this.tiltX = params.tiltX ?? 0
      this.tiltY = params.tiltY ?? 0
      this.twist = params.twist ?? 0
      this.pointerType = params.pointerType ?? 'mouse'
      this.isPrimary = params.isPrimary ?? true
    }
  }
  // @ts-expect-error -- assigning the polyfill onto the jsdom global
  window.PointerEvent = PointerEventPolyfill
}

// jsdom doesn't implement these either; @dnd-kit's sensors call them when
// setting up pointer capture on drag start.
if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false
}
if (!Element.prototype.setPointerCapture) {
  Element.prototype.setPointerCapture = () => {}
}
if (!Element.prototype.releasePointerCapture) {
  Element.prototype.releasePointerCapture = () => {}
}

// Element.getBoundingClientRect defaults to all-zero in jsdom, which is fine
// for most tests but @dnd-kit's collision detection needs non-degenerate
// rects to resolve a droppable under the pointer in some flows; individual
// tests can override this per-element as needed.

// TaskBoard's blocked-task-move confirmation and several delete/stop actions
// call window.confirm — default to "confirmed" so tests don't hang on a
// real browser dialog; override per-test with vi.spyOn(window, 'confirm').
vi.stubGlobal('confirm', vi.fn(() => true))
