import type { PausedProvider, ProviderCheck } from '../api/client'

// The one place that maps backend health check ids to UX tiers:
//   1. blocking  - dispatch paused (StatusBanner, driven by `paused`, not checks)
//   2. attention - counted dot on Settings / Health tab, no banner
//   3. advice    - only on Settings -> Health, snoozable per check
export type HealthTier = 'blocking' | 'attention' | 'advice'
export type CheckTier = Exclude<HealthTier, 'blocking'>

export const ADVICE_CHECK_IDS = new Set(['auto_backup', 'db_size', 'update_check', 'version'])

/** Tier of a check, or null when it is ok. Unknown ids default to attention so new backend checks are never hidden. */
export function healthTier(check: ProviderCheck): CheckTier | null {
  if (check.status === 'ok') return null
  if (check.id === 'repo_base_dir') return check.status === 'warn' ? 'advice' : 'attention'
  if (check.id && ADVICE_CHECK_IDS.has(check.id)) return 'advice'
  return 'attention'
}

// api_auth is tier 2 (security), so it is never snoozable.
export function isSnoozable(check: ProviderCheck): boolean {
  return healthTier(check) === 'advice'
}

export function visibleIssues(checks: ProviderCheck[], snoozed: string[]): ProviderCheck[] {
  return checks.filter((c) => {
    const tier = healthTier(c)
    if (tier === null) return false
    if (tier === 'attention') return true
    return !snoozed.includes(c.id)
  })
}

export function healthSummary(
  checks: ProviderCheck[],
  snoozed: string[],
  paused: PausedProvider[],
): { count: number; severity: 'error' | 'warn' | null } {
  const issues = visibleIssues(checks, snoozed)
  const count = issues.length
  const severity =
    paused.length > 0 || issues.some((c) => c.status === 'error') ? 'error' : count > 0 ? 'warn' : null
  return { count, severity }
}

export function pausedSignature(paused: PausedProvider[]): string {
  return paused.map((p) => `${p.provider}@${p.paused_at}`).sort().join('|')
}

export const STATUS_BANNER_DISMISS_KEY = 'ate:status-banner-dismissed'
export const HEALTH_SNOOZE_KEY = 'ate:health-snoozed'

export function readBannerDismissed(): string | null {
  try {
    return sessionStorage.getItem(STATUS_BANNER_DISMISS_KEY)
  } catch {
    return null
  }
}

export function writeBannerDismissed(sig: string): void {
  try {
    sessionStorage.setItem(STATUS_BANNER_DISMISS_KEY, sig)
  } catch {
    // ignore storage errors
  }
}

export function readSnoozed(): string[] {
  try {
    const raw = localStorage.getItem(HEALTH_SNOOZE_KEY)
    if (!raw) return []
    const v = JSON.parse(raw)
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

export function writeSnoozed(ids: string[]): void {
  try {
    localStorage.setItem(HEALTH_SNOOZE_KEY, JSON.stringify(ids))
  } catch {
    // ignore storage errors
  }
}
