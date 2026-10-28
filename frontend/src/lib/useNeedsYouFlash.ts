import { useEffect, useRef, useState } from 'react'
import { wsClient } from '../api/ws'

export const FLASH_MS = 3600 // 3 pulses x 1.2s

export function shouldFlash(prev: number | undefined, next: number): boolean {
  return prev !== undefined && next > prev
}

/** Flashes when the needs-you count rises (after the first loaded value) or on a WS task.needs_human. */
export function useNeedsYouFlash(count: number, loaded: boolean): { flashing: boolean; flashKey: number } {
  const [state, setState] = useState({ flashing: false, key: 0 })
  const prev = useRef<number | undefined>(undefined)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const trigger = useRef(() => {})
  trigger.current = () => {
    if (timer.current) clearTimeout(timer.current)
    setState((s) => ({ flashing: true, key: s.key + 1 }))
    timer.current = setTimeout(() => setState((s) => ({ ...s, flashing: false })), FLASH_MS)
  }

  useEffect(() => {
    if (!loaded) return
    if (shouldFlash(prev.current, count)) trigger.current()
    prev.current = count
  }, [count, loaded])

  useEffect(() => {
    const off = wsClient.on((ev) => {
      if (ev.type === 'task.needs_human') trigger.current()
    })
    return () => {
      off()
      if (timer.current) clearTimeout(timer.current)
    }
  }, [])

  return { flashing: state.flashing, flashKey: state.key }
}
