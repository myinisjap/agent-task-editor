import { describe, it, expect } from 'vitest'
import { inboxKeyAction } from './inboxKeys'

const ev = (key: string, over: Record<string, unknown> = {}, target: EventTarget | null = document.body) =>
  ({ key, metaKey: false, ctrlKey: false, altKey: false, defaultPrevented: false, target, ...over }) as Parameters<typeof inboxKeyAction>[0]

describe('inboxKeyAction', () => {
  it('maps keys', () => {
    expect(inboxKeyAction(ev('j'))).toBe('next')
    expect(inboxKeyAction(ev('ArrowDown'))).toBe('next')
    expect(inboxKeyAction(ev('k'))).toBe('prev')
    expect(inboxKeyAction(ev('ArrowUp'))).toBe('prev')
    expect(inboxKeyAction(ev('a'))).toBe('approve')
    expect(inboxKeyAction(ev('r'))).toBe('reject')
    expect(inboxKeyAction(ev('Enter'))).toBe('open')
    expect(inboxKeyAction(ev('o'))).toBe('open')
    expect(inboxKeyAction(ev('Escape'))).toBe('escape')
    expect(inboxKeyAction(ev('?'))).toBeNull()
    expect(inboxKeyAction(ev('x'))).toBeNull()
  })

  it('ignores typing targets and modifiers', () => {
    for (const tag of ['input', 'textarea', 'select']) {
      expect(inboxKeyAction(ev('j', {}, document.createElement(tag)))).toBeNull()
    }
    const ce = document.createElement('div')
    Object.defineProperty(ce, 'isContentEditable', { value: true })
    expect(inboxKeyAction(ev('j', {}, ce))).toBeNull()
    expect(inboxKeyAction(ev('r', { metaKey: true }))).toBeNull()
    expect(inboxKeyAction(ev('a', { ctrlKey: true }))).toBeNull()
    expect(inboxKeyAction(ev('j', { altKey: true }))).toBeNull()
    expect(inboxKeyAction(ev('j', { defaultPrevented: true }))).toBeNull()
  })

  it('leaves Enter on links and buttons alone', () => {
    expect(inboxKeyAction(ev('Enter', {}, document.createElement('button')))).toBeNull()
    expect(inboxKeyAction(ev('Enter', {}, document.createElement('a')))).toBeNull()
    expect(inboxKeyAction(ev('j', {}, document.createElement('button')))).toBe('next')
  })
})
