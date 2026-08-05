import { useCallback, useEffect, useRef } from 'react'

/**
 * Returns a trailing-debounced wrapper around `fn` with a stable identity
 * across renders (safe to drop in a `useEffect` dep array without churning
 * the effect). The debounce timer is cleared automatically on unmount so a
 * pending call can't fire after the component using it is gone.
 *
 * The wrapped function always calls the *latest* `fn` passed on the most
 * recent render (via a ref), so callers don't need to worry about stale
 * closures the way they would with a plain `useCallback`-memoized debounce.
 */
export function useDebouncedCallback<Args extends unknown[]>(
  fn: (...args: Args) => void,
  ms: number,
): (...args: Args) => void {
  const fnRef = useRef(fn)
  fnRef.current = fn

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (timerRef.current != null) clearTimeout(timerRef.current)
    }
  }, [])

  return useCallback(
    (...args: Args) => {
      if (timerRef.current != null) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => {
        timerRef.current = null
        fnRef.current(...args)
      }, ms)
    },
    [ms],
  )
}
