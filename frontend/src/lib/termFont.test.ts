import { test, expect, beforeEach } from 'vitest'
import { clampFont, stepFont, loadFontSize, saveFontSize, defaultFontSize, looksLikePrompt } from './termFont'

beforeEach(() => localStorage.clear())

test('clamps and steps within 9..18', () => {
  expect(clampFont(2)).toBe(9)
  expect(clampFont(40)).toBe(18)
  expect(stepFont(18, 1)).toBe(18)
  expect(stepFont(9, -1)).toBe(9)
  expect(stepFont(11, 1)).toBe(12)
})

test('defaults and per-screen persistence', () => {
  expect(loadFontSize(true)).toBe(defaultFontSize(true))
  expect(loadFontSize(false)).toBe(13)
  saveFontSize(true, 15)
  expect(loadFontSize(true)).toBe(15)
  expect(loadFontSize(false)).toBe(13)
})

test('garbage in storage falls back to default', () => {
  localStorage.setItem('chat.fontSize.wide', 'abc')
  expect(loadFontSize(false)).toBe(13)
})

test('prompt detection', () => {
  expect(looksLikePrompt('Proceed? [y/n]')).toBe(true)
  expect(looksLikePrompt('Continue (y/N)')).toBe(true)
  expect(looksLikePrompt('❯ 1. Yes')).toBe(true)
  expect(looksLikePrompt('  2) No')).toBe(true)
  expect(looksLikePrompt('compiling 3 files')).toBe(false)
  expect(looksLikePrompt('')).toBe(false)
})
