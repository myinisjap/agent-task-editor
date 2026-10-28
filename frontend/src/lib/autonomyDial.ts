import type {
  AutonomyBacktest,
  AskRule,
  LabelAutonomy,
  Repo,
  RiskFlagCode,
  Workflow,
  WorkflowLabel,
} from '../api/client'
import type { BadgeTone } from '../components/ui'
import { humanPathTargetLabel } from './humanGate'
import { agentTriggerableLabels } from './setupStatus'
import { draftFromWorkflow, serializeDraft } from './workflowEdit'

export type DialMode = LabelAutonomy['mode']

export const DIAL_NAME: Record<DialMode, string> = {
  ask: 'Always ask',
  on_flags: 'Ask on flags',
  auto: 'Auto',
}

export const DIAL_TONE: Record<DialMode, BadgeTone> = {
  ask: 'info',
  on_flags: 'warning',
  auto: 'success',
}

export const DIAL_MODES: DialMode[] = ['ask', 'on_flags', 'auto']

/** Flags pre-checked when a gate is first switched to "Ask on flags". */
export const DEFAULT_ASK_ON: RiskFlagCode[] = [
  'migration',
  'auth_or_security',
  'dependencies',
  'tests_removed',
  'ci_config',
]

export const HOLD_OPTIONS: { seconds: number; label: string }[] = [
  { seconds: 0, label: 'No hold' },
  { seconds: 600, label: '10 min' },
  { seconds: 3600, label: '1 h' },
]

export function formatHold(seconds: number): string {
  const known = HOLD_OPTIONS.find((o) => o.seconds === seconds)
  if (known) return known.label
  if (seconds % 3600 === 0) return `${seconds / 3600} h`
  if (seconds % 60 === 0) return `${seconds / 60} min`
  return `${seconds} s`
}

/**
 * True when the dial applies to `labelName`: not terminal, not agent_ignore,
 * at least one outgoing transition and every outgoing transition human.
 *
 * Go twin: needsme.IsHumanGate. This is deliberately stricter than
 * lib/humanGate.ts#isHumanGateLabel, which also counts agent_ignore labels.
 */
export function isAutonomyGate(workflow: Workflow | undefined, labelName: string): boolean {
  const label = workflow?.labels.find((l) => l.name === labelName)
  if (!workflow || !label) return false
  if (label.is_terminal || label.agent_ignore) return false
  const out = workflow.transitions.filter((t) => t.from_label === labelName)
  return out.length > 0 && out.every((t) => t.trigger_type === 'human')
}

export function gateMode(label: WorkflowLabel | undefined): DialMode {
  return label?.autonomy?.mode ?? 'ask'
}

/**
 * HEURISTIC: does a task at this gate already have a code diff? Nothing in the
 * workflow model says whether a stage changes code, so walk backwards over
 * non-failure transitions and look at the agent stages that can feed the gate.
 * No such stage, or only plan-named ones, means "no diff yet". Kept in one
 * place so it is easy to replace.
 */
export function gateHasDiff(workflow: Workflow, labelName: string): boolean {
  const triggerable = new Set(agentTriggerableLabels(workflow))
  const terminal = new Set(workflow.labels.filter((l) => l.is_terminal).map((l) => l.name))
  const visited = new Set<string>([labelName])
  const queue = [labelName]
  const upstream: string[] = []
  while (queue.length) {
    const cur = queue.shift() as string
    for (const t of workflow.transitions) {
      if (t.to_label !== cur || t.path === 'failure' || visited.has(t.from_label)) continue
      visited.add(t.from_label)
      queue.push(t.from_label)
      if (triggerable.has(t.from_label) && !terminal.has(t.from_label)) upstream.push(t.from_label)
    }
  }
  if (upstream.length === 0) return false
  const byName = new Map(workflow.labels.map((l) => [l.name, l]))
  return upstream.some((n) => {
    const l = byName.get(n)
    return !(/plan/i.test(n) || /plan/i.test(l?.display_name ?? ''))
  })
}

export function successTarget(workflow: Workflow, labelName: string): WorkflowLabel | undefined {
  return humanPathTargetLabel(workflow, labelName, 'success')
}

export type DialForm = {
  mode: DialMode
  ask_on: RiskFlagCode[]
  hold_seconds: number
  max_cost_usd: number | null
}

export function formFromLabel(label: WorkflowLabel | undefined): DialForm {
  const a = label?.autonomy
  return {
    mode: a?.mode ?? 'ask',
    ask_on: [...(a?.ask_on ?? [])],
    hold_seconds: a?.hold_seconds ?? 600,
    max_cost_usd: a?.max_cost_usd ?? null,
  }
}

/** Switch mode; entering on_flags with nothing selected pre-checks the defaults. */
export function withMode(form: DialForm, mode: DialMode): DialForm {
  const ask_on = mode === 'on_flags' && form.ask_on.length === 0 ? [...DEFAULT_ASK_ON] : form.ask_on
  return { ...form, mode, ask_on }
}

export function toLabelAutonomy(form: DialForm): LabelAutonomy | null {
  if (form.mode === 'ask') return null
  return {
    mode: form.mode,
    ask_on: form.mode === 'on_flags' ? form.ask_on : [],
    hold_seconds: form.hold_seconds,
    max_cost_usd: form.max_cost_usd,
  }
}

/** True when the form would save something different from the label's stored autonomy. */
export function formDiffers(form: DialForm, label: WorkflowLabel | undefined): boolean {
  return JSON.stringify(toLabelAutonomy(form)) !== JSON.stringify(toLabelAutonomy(formFromLabel(label)))
}

export type PassCategory = 'asks' | 'auto' | 'caught' | 'slipped' | 'unknown'
type Pass = AutonomyBacktest['passes'][number]

export function passCategory(pass: Pass): PassCategory {
  if (pass.unknown || pass.would_skip === null) return 'unknown'
  if (pass.outcome === 'approved') return pass.would_skip ? 'auto' : 'asks'
  return pass.would_skip ? 'slipped' : 'caught'
}

export const CATEGORY_NAME: Record<PassCategory, string> = {
  asks: 'Asks you',
  auto: 'Auto-approved',
  caught: 'Rejected, caught',
  slipped: 'Rejected, slipped through',
  unknown: 'Unknown',
}

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`
}

function suffix(bt: AutonomyBacktest) {
  return `${bt.truncated ? ' (only the latest 500 passes)' : ''}${bt.timed_out ? ' (partial: timed out)' : ''}`
}

export function backtestVerdict(bt: AutonomyBacktest): string {
  let line: string
  if (bt.evaluated === 0) line = 'No passes in the last 30 days to test against.'
  else if (bt.escaped > 0)
    line = `Would have let ${plural(bt.escaped, 'rejected task', 'rejected tasks')} through without review.`
  else if (bt.would_skip > 0)
    line = `Would have skipped ${bt.would_skip} of ${bt.evaluated} reviews and missed no rejections.`
  else line = 'Would still have asked you every time.'
  return line + suffix(bt)
}

export function backtestSummary(bt: AutonomyBacktest): string {
  if (bt.evaluated === 0) return 'Last 30 days: no passes to test against'
  return `Last 30 days: would skip ${bt.would_skip} of ${bt.evaluated} · ${bt.escaped} ${bt.escaped === 1 ? 'rejection' : 'rejections'} missed${suffix(bt)}`
}

const DAY_MS = 86_400_000

/** "0 hits in 60 days": never hit, or last hit more than 60 days ago. */
export function isRuleUnused(rule: AskRule, now: Date = new Date()): boolean {
  if (!rule.last_hit_at) return true
  return now.getTime() - new Date(rule.last_hit_at).getTime() > 60 * DAY_MS
}

/** Repos that run `workflow` and have no verify command. */
export function reposWithoutVerify(workflow: Workflow, repos: Repo[], workflows: Workflow[]): Repo[] {
  const def = workflows.find((w) => w.name === 'Default')
  return repos.filter((r) => {
    const uses = r.workflow_id ? r.workflow_id === workflow.id : def?.id === workflow.id
    return uses && !(r.verify_command ?? '').trim()
  })
}

/** Workflow YAML with `labelName`'s autonomy replaced; everything else preserved. */
export function withGateAutonomy(wf: Workflow, labelName: string, cfg: LabelAutonomy | null): string {
  const draft = draftFromWorkflow(wf)
  draft.labels = draft.labels.map((l) => (l.name === labelName ? { ...l, autonomy: cfg } : l))
  return serializeDraft(draft, { keepAgentConfig: true })
}
