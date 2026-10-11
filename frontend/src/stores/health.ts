import { create } from 'zustand'
import { api, type ProviderCheck } from '../api/client'

type HealthState = {
  checks: ProviderCheck[]
  loaded: boolean
  fetch: () => Promise<void>
}

// Shared by the global HealthWarningBanner (which polls) and the nav's Health
// dot (which only reads), independent of the Get Started checklist.
export const useHealthStore = create<HealthState>((set) => ({
  checks: [],
  loaded: false,
  fetch: async () => {
    try {
      const r = await api.health.providers()
      set({ checks: r.checks ?? [], loaded: true })
    } catch {
      // Keep the last known checks; an unreachable endpoint is not a
      // provider failure.
      set({ loaded: true })
    }
  },
}))

/** Worst severity among non-ok checks, or null when everything is ok. */
export function worstHealth(checks: ProviderCheck[]): 'error' | 'warn' | null {
  if (checks.some((c) => c.status === 'error')) return 'error'
  if (checks.some((c) => c.status === 'warn')) return 'warn'
  return null
}
