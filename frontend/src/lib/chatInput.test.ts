import { test, expect } from 'vitest'
import { buildSendPayload, isTap, pixelsToLines } from './chatInput'

test('single line sends text + CR', () => {
  expect(buildSendPayload('hello')).toBe('hello\r')
})
test('empty sends only CR', () => {
  expect(buildSendPayload('')).toBe('\r')
  expect(buildSendPayload('  \n ')).toBe('\r')
})
test('multi-line is a bracketed paste then CR', () => {
  expect(buildSendPayload('a\nb')).toBe('\x1b[200~a\nb\x1b[201~\r')
  expect(buildSendPayload('a\r\nb')).toBe('\x1b[200~a\nb\x1b[201~\r')
})
test('isTap distinguishes taps from drags and long presses', () => {
  expect(isTap(2, 3, 100)).toBe(true)
  expect(isTap(0, 40, 100)).toBe(false)
  expect(isTap(0, 0, 800)).toBe(false)
})
test('pixelsToLines carries remainder', () => {
  expect(pixelsToLines(25, 0, 10)).toEqual({ lines: 2, remainder: 5 })
  expect(pixelsToLines(6, 5, 10)).toEqual({ lines: 1, remainder: 1 })
  expect(pixelsToLines(-25, 0, 10)).toEqual({ lines: -2, remainder: -5 })
})
