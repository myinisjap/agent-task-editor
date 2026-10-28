import type { WorkflowLabel } from '../api/client'
import type { WSEvent } from '../api/ws'
import { labelDisplayNameByName } from './labelDisplay'

export type TickerItem = { id: string; text: string; at: number; tone?: 'gate' | 'success' | 'danger' | 'neutral' }
export type TickerCtx = { titleFor: (taskId: string) => string | undefined; labels?: readonly WorkflowLabel[] }

let counter = 0

const DONE: Record<string, [string, TickerItem['tone']]> = {
  completed: ['agent finished', 'success'],
  failed: ['agent failed', 'danger'],
  cancelled: ['run cancelled', 'neutral'],
  waiting_human: ['agent waiting on you', 'gate'],
  interrupted: ['run interrupted', 'neutral'],
}
// The task.verification payload carries no test count, so "(42 tests)" would need a backend field.
const VERIFY: Record<string, [string, TickerItem['tone']]> = {
  pass: ['verify passed', 'success'],
  fail: ['verify failed', 'danger'],
  timeout: ['verify timed out', 'danger'],
  error: ['verify errored', 'danger'],
}
const REASON: Record<string, string> = { agent_question: 'has a question' }

export function formatTickerEvent(ev: WSEvent, ctx: TickerCtx, now = Date.now()): TickerItem | null {
  const name = (id: string) => ctx.titleFor(id) ?? 'Task ' + id.slice(0, 8)
  const mk = (text: string, tone?: TickerItem['tone']): TickerItem => ({ id: `${ev.type}-${now}-${counter++}`, text, at: now, tone })
  switch (ev.type) {
    case 'task.label_changed': {
      const p = ev.payload
      return mk(`${name(p.task_id)} moved ${labelDisplayNameByName(p.from, ctx.labels)} → ${labelDisplayNameByName(p.to, ctx.labels)}`)
    }
    case 'task.agent_done': {
      const [t, tone] = DONE[ev.payload.status] ?? ['run ended', 'neutral']
      return mk(`${name(ev.payload.task_id)}: ${t}`, tone)
    }
    case 'task.verification': {
      const v = VERIFY[ev.payload.status]
      return v ? mk(`${name(ev.payload.task_id)}: ${v[0]}`, v[1]) : null
    }
    case 'task.pr_mergeable_changed': {
      const m = ev.payload.pr_mergeable
      if (m === 'conflicting') return mk(`${name(ev.payload.task_id)}: PR has conflicts`, 'danger')
      if (m === 'mergeable') return mk(`${name(ev.payload.task_id)}: PR is mergeable`, 'success')
      return null
    }
    case 'task.needs_human': {
      const r = REASON[ev.payload.reason]
      return mk(`${name(ev.payload.task_id)} ${r ?? 'needs you'}`, 'gate')
    }
    case 'provider.pause_changed':
      return mk(`${ev.payload.provider} ${ev.payload.paused ? 'paused' : 'resumed'}`, ev.payload.paused ? 'danger' : 'success')
    default:
      return null
  }
}

export function pushTicker(list: TickerItem[], item: TickerItem, max = 3): TickerItem[] {
  return [item, ...list].slice(0, max)
}

export function formatTickerTime(at: number): string {
  return new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
