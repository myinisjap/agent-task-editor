import { useEffect, useRef, useState } from 'react'
import { wsClient } from '../api/ws'
import { formatTickerEvent, pushTicker, type TickerCtx, type TickerItem } from './homeTicker'

/** Last few live events, client-side only: starts empty and is never persisted. */
export function useHomeTicker(ctx: TickerCtx): TickerItem[] {
  const [items, setItems] = useState<TickerItem[]>([])
  const ctxRef = useRef(ctx)
  ctxRef.current = ctx
  useEffect(() => {
    return wsClient.on((ev) => {
      const item = formatTickerEvent(ev, ctxRef.current)
      if (item) setItems((l) => pushTicker(l, item))
    })
  }, [])
  return items
}
