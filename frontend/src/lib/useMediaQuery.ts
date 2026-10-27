import { useEffect, useState } from 'react'

/** Tracks a CSS media query. Returns false where matchMedia is unavailable. */
export function useMediaQuery(query: string): boolean {
  const get = () => (typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(query).matches : false)
  const [matches, setMatches] = useState(get)
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const mql = window.matchMedia(query)
    const onChange = () => setMatches(mql.matches)
    onChange()
    mql.addEventListener?.('change', onChange)
    return () => mql.removeEventListener?.('change', onChange)
  }, [query])
  return matches
}

/** ≥1280px: the decision-first review layout on Task Detail. */
export const useIsWideDesktop = () => useMediaQuery('(min-width: 1280px)')
