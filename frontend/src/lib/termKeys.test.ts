import { test, expect } from 'vitest'
import { TERMINAL_KEYS, toControlChar } from './termKeys'

test('every key sends a non-empty sequence under a unique label', () => {
  const labels = TERMINAL_KEYS.map((k) => k.label)
  expect(new Set(labels).size).toBe(labels.length)
  for (const k of TERMINAL_KEYS) {
    expect(k.seq.length).toBeGreaterThan(0)
    expect(k.title).toBeTruthy()
  }
})

test('sends the sequences a physical keyboard would', () => {
  const seq = (label: string) => TERMINAL_KEYS.find((k) => k.label === label)?.seq
  expect(seq('Esc')).toBe('\x1b')
  expect(seq('Tab')).toBe('\t')
  // Claude CLI uses Shift+Tab to cycle modes — the whole reason the bar exists.
  expect(seq('⇧Tab')).toBe('\x1b[Z')
  expect(seq('↑')).toBe('\x1b[A')
  expect(seq('↓')).toBe('\x1b[B')
  expect(seq('←')).toBe('\x1b[D')
  expect(seq('→')).toBe('\x1b[C')
  expect(seq('^C')).toBe('\x03')
})

test('toControlChar folds letters into control codes', () => {
  expect(toControlChar('c')).toBe('\x03')
  expect(toControlChar('C')).toBe('\x03')
  expect(toControlChar('a')).toBe('\x01')
  expect(toControlChar('r')).toBe('\x12')
  expect(toControlChar('[')).toBe('\x1b')
  expect(toControlChar(' ')).toBe('\x00')
  expect(toControlChar('?')).toBe('\x7f')
})

test('toControlChar returns null for input with no control equivalent', () => {
  // Caller sends these through unchanged rather than swallowing the keystroke.
  expect(toControlChar('1')).toBeNull()
  expect(toControlChar('')).toBeNull()
  expect(toControlChar('\x1b[A')).toBeNull() // a paste or an escape sequence
})
