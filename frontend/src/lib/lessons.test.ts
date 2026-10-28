import { describe, it, expect } from 'vitest'
import { lessonPrefill, parseList, formatList } from './lessons'

describe('lessonPrefill', () => {
  it('trims whitespace', () => expect(lessonPrefill('  hi \n')).toBe('hi'))
  it('keeps exactly 300 chars', () => expect(lessonPrefill('a'.repeat(300))).toHaveLength(300))
  it('cuts beyond 300', () => expect(lessonPrefill('a'.repeat(400))).toHaveLength(300))
  it('handles empty', () => expect(lessonPrefill('   ')).toBe(''))
})

describe('parseList', () => {
  it('splits on commas and newlines, trims, drops empties and dupes', () => {
    expect(parseList(' a/**, b ,\n,a/**\nc ')).toEqual(['a/**', 'b', 'c'])
  })
  it('returns [] for blank', () => expect(parseList('  ')).toEqual([]))
})

describe('formatList', () => {
  it('joins with comma', () => expect(formatList(['a', 'b'])).toBe('a, b'))
})
