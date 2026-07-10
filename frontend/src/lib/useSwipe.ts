import { useRef } from 'react'
import type { TouchEvent } from 'react'

const SWIPE_THRESHOLD = 50

export type SwipeDirection = 'left' | 'right' | null

/**
 * Pure helper: given the start/end touch coordinates, determines whether the
 * gesture was a predominantly-horizontal swipe past the threshold. Returns
 * null for vertical drags (e.g. scrolling) or short/ambiguous movements, so
 * callers can leave those untouched.
 */
export function detectSwipeDirection(
  start: { x: number; y: number },
  end: { x: number; y: number },
  threshold = SWIPE_THRESHOLD,
): SwipeDirection {
  const dx = end.x - start.x
  const dy = end.y - start.y

  if (Math.abs(dx) < threshold || Math.abs(dx) <= Math.abs(dy)) return null

  return dx < 0 ? 'left' : 'right'
}

/**
 * Returns touch event handlers that detect a predominantly-horizontal swipe
 * gesture and call onSwipeLeft / onSwipeRight accordingly. Vertical drags
 * (e.g. scrolling a task list) are ignored so they don't fight with the
 * element's own vertical scrolling.
 */
export function useSwipe({
  onSwipeLeft,
  onSwipeRight,
  threshold = SWIPE_THRESHOLD,
}: {
  onSwipeLeft: () => void
  onSwipeRight: () => void
  threshold?: number
}) {
  const start = useRef<{ x: number; y: number } | null>(null)

  function onTouchStart(e: TouchEvent) {
    const t = e.touches[0]
    start.current = { x: t.clientX, y: t.clientY }
  }

  function onTouchEnd(e: TouchEvent) {
    if (!start.current) return
    const t = e.changedTouches[0]
    const direction = detectSwipeDirection(start.current, { x: t.clientX, y: t.clientY }, threshold)
    start.current = null

    if (direction === 'left') onSwipeLeft()
    else if (direction === 'right') onSwipeRight()
  }

  return { onTouchStart, onTouchEnd }
}
