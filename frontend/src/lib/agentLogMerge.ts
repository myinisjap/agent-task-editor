import type { AgentLog } from '../api/client'

// How many log entries to fetch per page (initial tail + each "load earlier").
export const LOG_PAGE_SIZE = 200

// toLog normalises a log-ish payload (from a REST page, the batched replay, or
// a live agent.log event) into an AgentLog. Live events carry the timestamp as
// `at`, so fill that in when `timestamp` is absent.
//
// The backend now stamps live `agent.log` entries with the same id as the
// persisted `agent_logs` row (see backend/internal/agent/pool.go
// `persistLogs`), so `e.id` should normally be present and this is the id we
// dedupe on in `mergeLogs`. The fallback below only matters against an old
// backend that predates that fix (or any payload that otherwise omits an
// id): it must be a *deterministic* function of the entry's content, not a
// random id, so that if the same entry later reappears (e.g. via
// agent.log_replay on reconnect) it produces the same key and dedupes
// correctly. A random id would silently defeat dedup and duplicate the
// visible log on every reconnect.
export function toLog(e: any): AgentLog {
  const timestamp = e.timestamp ?? e.at ?? ''
  return {
    id: e.id ?? `live:${timestamp}|${e.type}|${e.content}`,
    agent_run_id: e.agent_run_id ?? '',
    timestamp,
    type: e.type,
    content: e.content,
    seq: typeof e.seq === 'number' ? e.seq : undefined,
  }
}

// mergeLogs unions two log lists by id (deduping) and returns them in
// chronological order. Used when combining the initial page with the batched
// replay or with an older "load earlier" page. Ordering is by timestamp, with
// id as a stable tiebreaker for entries that share a timestamp.
export function mergeLogs(prev: AgentLog[], incoming: AgentLog[]): AgentLog[] {
  if (incoming.length === 0) return prev
  const byId = new Map<string, AgentLog>()
  for (const l of prev) byId.set(l.id, l)
  for (const l of incoming) byId.set(l.id, l)
  return Array.from(byId.values()).sort(compareLogs)
}

// compareLogs orders log entries chronologically. Timestamps have one-second
// resolution, so entries in the same second are ordered by the backend's
// monotonic `seq` (when both have one), then by id as a last resort.
export function compareLogs(a: AgentLog, b: AgentLog): number {
  const ta = Date.parse(a.timestamp) || 0
  const tb = Date.parse(b.timestamp) || 0
  if (ta !== tb) return ta - tb
  if (a.seq != null && b.seq != null && a.seq !== b.seq) return a.seq - b.seq
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}
