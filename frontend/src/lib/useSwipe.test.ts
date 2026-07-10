import { describe, it, expect } from 'vitest'
import { detectSwipeDirection } from './useSwipe'

describe('detectSwipeDirection', () => {
  it('detects a left swipe past the threshold', () => {
    const result = detectSwipeDirection({ x: 200, y: 100 }, { x: 100, y: 105 })
    expect(result).toBe('left')
  })

  it('detects a right swipe past the threshold', () => {
    const result = detectSwipeDirection({ x: 100, y: 100 }, { x: 200, y: 95 })
    expect(result).toBe('right')
  })

  it('ignores movement below the horizontal threshold', () => {
    const result = detectSwipeDirection({ x: 100, y: 100 }, { x: 130, y: 100 })
    expect(result).toBeNull()
  })

  it('ignores a predominantly-vertical drag (e.g. scrolling)', () => {
    const result = detectSwipeDirection({ x: 100, y: 100 }, { x: 60, y: 220 })
    expect(result).toBeNull()
  })

  it('respects a custom threshold', () => {
    expect(detectSwipeDirection({ x: 0, y: 0 }, { x: 30, y: 0 }, 20)).toBe('right')
    expect(detectSwipeDirection({ x: 0, y: 0 }, { x: 10, y: 0 }, 20)).toBeNull()
  })
})
