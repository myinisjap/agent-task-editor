import { describe, it, expect } from 'vitest'
import { globalShortcutAction, isPaletteToggle, formatKeys, isTypingTarget, shortcutHint } from './shortcuts'

const ev = (key: string, extra: Record<string, unknown> = {}, target: EventTarget | null = document.body) =>
  ({ key, metaKey: false, ctrlKey: false, altKey: false, defaultPrevented: false, target, ...extra }) as Parameters<typeof globalShortcutAction>[0]

describe('globalShortcutAction', () => {
  it('resolves single keys', () => {
    expect(globalShortcutAction(ev('c'), null).action).toBe('new-task')
    expect(globalShortcutAction(ev('/'), null).action).toBe('focus-search')
    expect(globalShortcutAction(ev('?', { shiftKey: true }), null).action).toBe('help')
  })
  it('resolves g sequences and clears pending on other keys', () => {
    const g = globalShortcutAction(ev('g'), null)
    expect(g).toEqual({ action: null, pending: 'g' })
    expect(globalShortcutAction(ev('i'), 'g').action).toBe('review-queue')
    expect(globalShortcutAction(ev('b'), 'g').action).toBe('go-board')
    expect(globalShortcutAction(ev('p'), 'g').action).toBe('go-pipeline')
    expect(globalShortcutAction(ev('s'), 'g').action).toBe('go-settings')
    expect(globalShortcutAction(ev('x'), 'g')).toEqual({ action: null, pending: null })
  })
  it('ignores modifiers and repeats', () => {
    expect(globalShortcutAction(ev('c', { ctrlKey: true }), null).action).toBeNull()
    expect(globalShortcutAction(ev('c', { metaKey: true }), null).action).toBeNull()
    expect(globalShortcutAction(ev('c', { altKey: true }), null).action).toBeNull()
    expect(globalShortcutAction(ev('c', { repeat: true }), null).action).toBeNull()
  })
  it('is suppressed while typing', () => {
    for (const tag of ['input', 'textarea', 'select']) {
      expect(globalShortcutAction(ev('c', {}, document.createElement(tag)), null).action).toBeNull()
    }
    const ce = document.createElement('div')
    Object.defineProperty(ce, 'isContentEditable', { value: true })
    expect(globalShortcutAction(ev('c', {}, ce), null).action).toBeNull()
    const x = document.createElement('div'); x.className = 'xterm'
    const inner = document.createElement('span'); x.appendChild(inner)
    expect(isTypingTarget(inner)).toBe(true)
    expect(globalShortcutAction(ev('c', {}, inner), null).action).toBeNull()
  })
})

describe('isPaletteToggle', () => {
  it('matches Meta+K and Ctrl+K', () => {
    expect(isPaletteToggle({ key: 'k', metaKey: true, ctrlKey: false, altKey: false })).toBe(true)
    expect(isPaletteToggle({ key: 'K', metaKey: false, ctrlKey: true, altKey: false })).toBe(true)
    expect(isPaletteToggle({ key: 'k', metaKey: false, ctrlKey: false, altKey: false })).toBe(false)
  })
})

describe('formatKeys', () => {
  it('formats per platform', () => {
    expect(formatKeys(['Mod', 'K'], true)).toBe('⌘K')
    expect(formatKeys(['Mod', 'K'], false)).toBe('Ctrl+K')
    expect(formatKeys(['g', 'i'], false)).toBe('G then I')
    expect(formatKeys(['c'], false)).toBe('C')
  })
  it('builds tooltips', () => {
    expect(shortcutHint('new-task')).toBe('New task (C)')
  })
})
