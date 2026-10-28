import type { RiskFlagCode } from '../api/client'

const LABELS: Record<RiskFlagCode, string> = {
  migration: 'Migration',
  auth_or_security: 'Auth/security',
  dependencies: 'Dependencies',
  tests_removed: 'Tests removed',
  ci_config: 'CI config',
  large_change: 'Large change',
  outside_plan_scope: 'Outside plan',
}

// Red for flags that most often hide real damage, amber for the rest.
const RED: ReadonlySet<string> = new Set(['tests_removed', 'auth_or_security', 'migration'])

export function riskFlagLabel(code: string): string {
  return (LABELS as Record<string, string>)[code] ?? code
}

export function riskFlagClass(code: string): string {
  return RED.has(code) ? 'bg-red-900/60 text-red-200' : 'bg-amber-900/60 text-amber-200'
}

/** Every flag code, in the backend's order (reviewbrief/flags.go). */
export const RISK_FLAG_CODES: RiskFlagCode[] = [
  'migration',
  'auth_or_security',
  'dependencies',
  'tests_removed',
  'ci_config',
  'large_change',
  'outside_plan_scope',
]
