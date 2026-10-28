/**
 * Severity + human wording for system events in the run log.
 *
 * Severity drives colour (error red, warning amber, info neutral) and the
 * Debug toggle: debug-level events only show when Debug is on.
 */

export type LogSeverity = 'error' | 'warning' | 'info' | 'debug'

export interface SystemText {
  severity: LogSeverity
  message: string
  detail?: string
}

function sentence(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

type Rule = { re: RegExp; severity: LogSeverity; message: (m: RegExpMatchArray, text: string) => string }

// Ordered: first match wins. Covers the plain-text lines the backend emits via
// LogSystem (providers, pool, dispatcher, verify).
const RULES: Rule[] = [
  { re: /^started (\w+) pid=(\d+)/, severity: 'debug', message: (m) => `Started ${m[1]} (pid ${m[2]})` },
  { re: /^requested effort=(\S+)/, severity: 'debug', message: (m) => `Effort: ${m[1]}` },
  { re: /^step (started|finished)/i, severity: 'debug', message: (_m, t) => sentence(t) },
  { re: /^Human reply: /, severity: 'info', message: (_m, t) => t },
  { re: /^Verification passed/, severity: 'info', message: (_m, t) => t },
  { re: /^Verification (failed|error)/, severity: 'error', message: (_m, t) => t },
  // New wording, and the legacy contradictory wording so old runs read correctly.
  { re: /^Run failed: /, severity: 'error', message: (_m, t) => t },
  {
    re: /^(\w+) exited with error but had (?:parsed )?outcome "(.+)" — treating as failed/,
    severity: 'error',
    message: (m) => `Run failed: ${m[1]} exited with an error (it had reported "${m[2]}")`,
  },
  { re: /^(\w+) exited: (.+)/, severity: 'error', message: (m) => `${m[1]} exited with an error: ${m[2]}` },
  { re: /agent timed out/i, severity: 'error', message: () => 'Run timed out' },
  { re: /max-turns limit|reached max_turns/i, severity: 'error', message: (_m, t) => sentence(t) },
  { re: /cost budget exceeded|cost watchdog: .*cancelling/i, severity: 'error', message: (_m, t) => sentence(t) },
  { re: /cost watchdog: .*warning threshold/i, severity: 'warning', message: (_m, t) => sentence(t) },
  { re: /rate limit resets at/i, severity: 'warning', message: (_m, t) => sentence(t) },
  { re: /could not resume session|no saved session/i, severity: 'warning', message: (_m, t) => sentence(t) },
  { re: /output stream truncated/i, severity: 'warning', message: (_m, t) => sentence(t) },
  { re: /used its default model/i, severity: 'info', message: (_m, t) => sentence(t) },
]

export function describeSystemText(text: string): SystemText {
  const t = text.trim()
  for (const r of RULES) {
    const m = t.match(r.re)
    if (m) return { severity: r.severity, message: r.message(m, t) }
  }
  return { severity: 'info', message: text }
}

/**
 * Severity for CLI JSON `system` subtypes. Every user-relevant subtype is mapped
 * explicitly; unrecognised subtypes default to debug because they are CLI
 * bookkeeping that would otherwise bury the events that matter.
 */
export function severityForSubtype(subtype: string, status?: string): LogSeverity {
  switch (subtype) {
    case 'init':
    case 'vcs_state_changed':
    case 'code_change_published':
    case 'task_started':
    case 'compact_boundary':
      return 'info'
    case 'task_notification':
      return status === 'completed' ? 'info' : 'warning'
    case 'api_retry':
      return 'warning'
    default:
      return 'debug'
  }
}
