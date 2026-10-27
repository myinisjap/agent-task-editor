import { useEffect } from 'react'
import { create } from 'zustand'
import { readSnoozed, writeSnoozed } from '../lib/healthTiers'
import { api, type PausedProvider, type ProviderCheck } from '../api/client'

type HealthState = {
  checks: ProviderCheck[]
  /** Providers whose dispatch the circuit breaker paused after an auth failure. */
  paused: PausedProvider[]
  loaded: boolean
  /** Advice check ids the user chose not to be reminded about (localStorage). */
  snoozed: string[]
  setSnoozed: (id: string, snoozed: boolean) => void
  fetch: () => Promise<void>
  resume: (provider: string) => Promise<void>
}

// Shared by the StatusBanner, the nav/Settings Health dot and the Get Started
// checklist. Polling is done once by useHealthPolling() in App.
export const useHealthStore = create<HealthState>((set, get) => ({
  checks: [],
  paused: [],
  loaded: false,
  snoozed: readSnoozed(),
  setSnoozed: (id, on) => {
    const rest = get().snoozed.filter((x) => x !== id)
    const next = on ? [...rest, id] : rest
    writeSnoozed(next)
    set({ snoozed: next })
  },
  fetch: async () => {
    try {
      const r = await api.health.providers()
      set({ checks: r.checks ?? [], paused: r.paused_providers ?? [], loaded: true })
    } catch {
      // Keep the last known checks; an unreachable endpoint is not a
      // provider failure.
      set({ loaded: true })
    }
  },
  resume: async (provider) => {
    try {
      await api.health.resumeProvider(provider)
    } finally {
      await get().fetch()
    }
  },
}))

/** Worst severity among non-ok checks, or null when everything is ok. */
export function worstHealth(checks: ProviderCheck[]): 'error' | 'warn' | null {
  if (checks.some((c) => c.status === 'error')) return 'error'
  if (checks.some((c) => c.status === 'warn')) return 'warn'
  return null
}

const POLL_MS = 60_000

/** Fetch health on mount, every 60s and on window focus. Call once at app level. */
export function useHealthPolling() {
  const fetchHealth = useHealthStore((s) => s.fetch)
  useEffect(() => {
    fetchHealth()
    const id = setInterval(fetchHealth, POLL_MS)
    const onFocus = () => fetchHealth()
    window.addEventListener('focus', onFocus)
    return () => {
      clearInterval(id)
      window.removeEventListener('focus', onFocus)
    }
  }, [fetchHealth])
}
