import type { AgentLog } from '../api/client'

// How many log entries to fetch per page (initial tail + each "load earlier").
export const LOG_PAGE_SIZE = 200

// toLog normalises a log-ish payload (from a REST page, the batched replay, or
// a live agent.log event) into an AgentLog. Live events carry the timestamp as
// `at` and may omit the id, so fill both in.
export function toLog(e: any): AgentLog {
  return {
    id: e.id ?? crypto.randomUUID(),
    agent_run_id: e.agent_run_id ?? '',
    timestamp: e.timestamp ?? e.at ?? '',
    type: e.type,
    content: e.content,
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
  return Array.from(byId.values()).sort((a, b) => {
    const ta = Date.parse(a.timestamp) || 0
    const tb = Date.parse(b.timestamp) || 0
    if (ta !== tb) return ta - tb
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
  })
}
