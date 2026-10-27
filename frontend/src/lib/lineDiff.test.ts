import { describe, it, expect } from 'vitest'
import { diffLines } from './lineDiff'

describe('diffLines', () => {
  it('returns context only for identical text', () => {
    expect(diffLines('a\nb', 'a\nb')).toEqual([
      { type: 'context', text: 'a' },
      { type: 'context', text: 'b' },
    ])
  })

  it('detects additions and removals', () => {
    expect(diffLines('a\nb\nc', 'a\nx\nc')).toEqual([
      { type: 'context', text: 'a' },
      { type: 'remove', text: 'b' },
      { type: 'add', text: 'x' },
      { type: 'context', text: 'c' },
    ])
  })

  it('handles empty sides', () => {
    expect(diffLines('', 'a')).toEqual([{ type: 'add', text: 'a' }])
    expect(diffLines('a', '')).toEqual([{ type: 'remove', text: 'a' }])
    expect(diffLines('', '')).toEqual([])
  })
})
