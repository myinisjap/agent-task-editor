import { useRef } from 'react'
import type { TouchEvent } from 'react'
import { detectSwipeDirection } from '../../lib/useSwipe'

// A swipe must be clearly horizontal (dx at least this many times dy).
const HORIZONTAL_RATIO = 2
// Touches held at least this long are a dnd-kit long-press drag, not a swipe.
const LONG_PRESS_MS = 200

/** Pure: direction of a clearly-horizontal, quick swipe, else null. */
export function columnSwipeDirection(
  start: { x: number; y: number },
  end: { x: number; y: number },
  elapsedMs: number,
) {
  if (elapsedMs >= LONG_PRESS_MS * 3) return null
  if (Math.abs(end.x - start.x) < HORIZONTAL_RATIO * Math.abs(end.y - start.y)) return null
  return detectSwipeDirection(start, end)
}

/** Touch handlers for column paging; ignored while a card drag is active. */
export function useColumnSwipe({
  onSwipeLeft,
  onSwipeRight,
  disabled = false,
}: {
  onSwipeLeft: () => void
  onSwipeRight: () => void
  disabled?: boolean
}) {
  const start = useRef<{ x: number; y: number; t: number } | null>(null)
  return {
    onTouchStart(e: TouchEvent) {
      const p = e.touches[0]
      start.current = { x: p.clientX, y: p.clientY, t: Date.now() }
    },
    onTouchEnd(e: TouchEvent) {
      const s = start.current
      start.current = null
      if (!s || disabled) return
      const p = e.changedTouches[0]
      const dir = columnSwipeDirection(s, { x: p.clientX, y: p.clientY }, Date.now() - s.t)
      if (dir === 'left') onSwipeLeft()
      else if (dir === 'right') onSwipeRight()
    },
    onTouchCancel() {
      start.current = null
    },
  }
}
