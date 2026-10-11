import { create } from 'zustand'
import { api, type PausedProvider, type ProviderCheck } from '../api/client'

type HealthState = {
  checks: ProviderCheck[]
  /** Providers whose dispatch the circuit breaker paused after an auth failure. */
  paused: PausedProvider[]
  loaded: boolean
  fetch: () => Promise<void>
  resume: (provider: string) => Promise<void>
}

// Shared by the global HealthWarningBanner (which polls) and the nav's Health
// dot (which only reads), independent of the Get Started checklist.
export const useHealthStore = create<HealthState>((set, get) => ({
  checks: [],
  paused: [],
  loaded: false,
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
