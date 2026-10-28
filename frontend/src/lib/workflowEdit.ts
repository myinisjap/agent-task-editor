// Pure model-edit helpers for the Pipeline graph editor. A WorkflowDraft is the
// editable shape of a workflow (booleans instead of 0/1 ints, no ids); every
// function returns a new draft and never mutates its input. Persistence goes
// through the same YAML endpoint the Workflow tab uses (PUT /workflows/{id}/yaml),
// because that is the one write path that round-trips create_pr, wip_limit and
// verify_* together (see serializeDraft).

import type { Workflow } from '../api/client'
import type { components } from '../api/types'

export type LabelAutonomy = components['schemas']['LabelAutonomy']

export type VerifyMode = 'inherit' | 'custom' | 'disabled'
export type TriggerType = 'agent' | 'human' | 'both'
export type TransitionPath = 'success' | 'failure' | 'either'

export const MAX_DISPLAY_NAME = 64

export type DraftLabel = {
  name: string
  display_name: string
  color: string
  agent_ignore: boolean
  is_terminal: boolean
  create_pr: boolean
  wip_limit: number | null
  wip_limit_hard: boolean
  verify_mode: VerifyMode
  verify_command: string
  /** Per-gate autonomy policy; null = ask. Carried through so graph saves do not drop it. */
  autonomy: LabelAutonomy | null
}

export type DraftTransition = {
  from_label: string
  to_label: string
  trigger_type: TriggerType
  /** null = unset (the "either" default for agent edges; meaningless for human edges). */
  path: TransitionPath | null
  /** Carried through for display only: the YAML save path cannot persist it. */
  agent_config_id: string | null
}

/** Label order in the array is the sort order. */
export type WorkflowDraft = {
  name: string
  description: string
  labels: DraftLabel[]
  transitions: DraftTransition[]
}

export type DraftError = { label?: string; message: string }

/** The transition kinds the editor offers, as one pick-list. */
export type TransitionKind = 'agent_success' | 'agent_failure' | 'agent_either' | 'human' | 'both'

export const TRANSITION_KINDS: { value: TransitionKind; label: string }[] = [
  { value: 'agent_success', label: 'Agent: on success' },
  { value: 'agent_failure', label: 'Agent: on failure' },
  { value: 'agent_either', label: 'Agent: either outcome' },
  { value: 'human', label: 'Human (approval gate)' },
  { value: 'both', label: 'Agent or human' },
]

export const DEFAULT_LABEL_COLOR = '#6366f1'
const COLOR_RE = /^#[0-9a-fA-F]{3,8}$/

export function draftFromWorkflow(wf: Workflow): WorkflowDraft {
  const labels = [...(wf.labels ?? [])].sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0))
  return {
    name: wf.name,
    description: wf.description ?? '',
    labels: labels.map((l) => ({
      name: l.name,
      display_name: l.display_name ?? '',
      color: l.color ?? '',
      agent_ignore: Boolean(l.agent_ignore),
      is_terminal: Boolean(l.is_terminal),
      create_pr: Boolean(l.create_pr),
      wip_limit: l.wip_limit ?? null,
      wip_limit_hard: Boolean(l.wip_limit_hard),
      verify_mode: (l.verify_mode ?? 'inherit') as VerifyMode,
      verify_command: l.verify_command ?? '',
      autonomy: l.autonomy ?? null,
    })),
    transitions: (wf.transitions ?? []).map((t) => ({
      from_label: t.from_label,
      to_label: t.to_label,
      trigger_type: t.trigger_type as TriggerType,
      path: (t.path ?? null) as TransitionPath | null,
      agent_config_id: t.agent_config_id ?? null,
    })),
  }
}

/**
 * Build a Workflow-shaped object from a draft so the read-only flowchart and
 * staffing helpers can render unsaved edits. Ids are synthetic.
 */
export function draftToWorkflow(draft: WorkflowDraft, base: Workflow): Workflow {
  return {
    ...base,
    name: draft.name,
    description: draft.description,
    labels: draft.labels.map((l, i) => ({
      id: `draft-${l.name}`,
      workflow_id: base.id,
      name: l.name,
      display_name: l.display_name,
      color: l.color,
      sort_order: i,
      agent_ignore: l.agent_ignore ? 1 : 0,
      is_terminal: l.is_terminal ? 1 : 0,
      create_pr: l.create_pr ? 1 : 0,
      wip_limit: l.wip_limit,
      wip_limit_hard: l.wip_limit_hard ? 1 : 0,
      verify_mode: l.verify_mode,
      verify_command: l.verify_command,
      autonomy: l.autonomy,
    })),
    transitions: draft.transitions.map((t, i) => ({
      id: `draft-t-${i}`,
      workflow_id: base.id,
      from_label: t.from_label,
      to_label: t.to_label,
      trigger_type: t.trigger_type,
      agent_config_id: t.agent_config_id,
      path: t.path,
    })),
  }
}

export function isDirty(a: WorkflowDraft, b: WorkflowDraft): boolean {
  return JSON.stringify(a) !== JSON.stringify(b)
}

export function newLabel(name: string, color: string = DEFAULT_LABEL_COLOR): DraftLabel {
  return {
    name: name.trim(),
    display_name: '',
    color: color.trim(),
    agent_ignore: false,
    is_terminal: false,
    create_pr: false,
    wip_limit: null,
    wip_limit_hard: false,
    verify_mode: 'inherit',
    verify_command: '',
    autonomy: null,
  }
}

/** Appends a label. Invalid input (empty / duplicate name, bad colour) is returned as an error, not applied. */
export function addLabel(
  draft: WorkflowDraft,
  name: string,
  color: string = DEFAULT_LABEL_COLOR,
): { draft: WorkflowDraft; error?: string } {
  const label = newLabel(name, color)
  const error = checkLabelName(draft, label.name) ?? checkColor(label.name, label.color)
  if (error) return { draft, error }
  return { draft: { ...draft, labels: [...draft.labels, label] } }
}

function checkLabelName(draft: WorkflowDraft, name: string, ignore?: string): string | undefined {
  if (!name) return 'Label name is required.'
  if (draft.labels.some((l) => l.name === name && l.name !== ignore)) {
    return `A label named "${name}" already exists.`
  }
  return undefined
}

function checkColor(name: string, color: string): string | undefined {
  if (color !== '' && !COLOR_RE.test(color)) {
    return `Label "${name}": color must be a hex value like #6366f1.`
  }
  return undefined
}

/** Renames a label and rewrites every transition that references it. */
export function renameLabel(
  draft: WorkflowDraft,
  from: string,
  to: string,
): { draft: WorkflowDraft; error?: string } {
  const next = to.trim()
  if (next === from) return { draft }
  if (!draft.labels.some((l) => l.name === from)) return { draft, error: `No label named "${from}".` }
  const error = checkLabelName(draft, next, from)
  if (error) return { draft, error }
  return {
    draft: {
      ...draft,
      labels: draft.labels.map((l) => (l.name === from ? { ...l, name: next } : l)),
      transitions: draft.transitions.map((t) => ({
        ...t,
        from_label: t.from_label === from ? next : t.from_label,
        to_label: t.to_label === from ? next : t.to_label,
      })),
    },
  }
}

/** Removes a label and every transition touching it. */
export function deleteLabel(draft: WorkflowDraft, name: string): WorkflowDraft {
  return {
    ...draft,
    labels: draft.labels.filter((l) => l.name !== name),
    transitions: draft.transitions.filter((t) => t.from_label !== name && t.to_label !== name),
  }
}

/** Number of transitions that deleting `name` would also remove. */
export function transitionsTouching(draft: WorkflowDraft, name: string): number {
  return draft.transitions.filter((t) => t.from_label === name || t.to_label === name).length
}

export function updateLabel(
  draft: WorkflowDraft,
  name: string,
  patch: Partial<Omit<DraftLabel, 'name'>>,
): WorkflowDraft {
  // create_pr is workflow-unique (server rejects two): turning it on elsewhere moves it.
  const labels = draft.labels.map((l) => {
    if (l.name === name) return { ...l, ...patch }
    if (patch.create_pr === true && l.create_pr) return { ...l, create_pr: false }
    return l
  })
  return { ...draft, labels }
}

export function kindOf(t: Pick<DraftTransition, 'trigger_type' | 'path'>): TransitionKind {
  if (t.trigger_type === 'human') return 'human'
  if (t.trigger_type === 'both') return 'both'
  if (t.path === 'success') return 'agent_success'
  if (t.path === 'failure') return 'agent_failure'
  return 'agent_either'
}

function kindFields(kind: TransitionKind): Pick<DraftTransition, 'trigger_type' | 'path'> {
  switch (kind) {
    case 'agent_success': return { trigger_type: 'agent', path: 'success' }
    case 'agent_failure': return { trigger_type: 'agent', path: 'failure' }
    case 'agent_either': return { trigger_type: 'agent', path: null }
    case 'human': return { trigger_type: 'human', path: null }
    case 'both': return { trigger_type: 'both', path: null }
  }
}

/** Adds a transition. Rejects unknown labels, self-loops, and exact from/to/kind duplicates. */
export function addTransition(
  draft: WorkflowDraft,
  from: string,
  to: string,
  kind: TransitionKind,
): { draft: WorkflowDraft; error?: string } {
  const names = new Set(draft.labels.map((l) => l.name))
  if (!names.has(from)) return { draft, error: `Transition source "${from}" is not a label.` }
  if (!names.has(to)) return { draft, error: `Transition target "${to}" is not a label.` }
  if (from === to) return { draft, error: 'A transition cannot loop a label onto itself.' }
  const fields = kindFields(kind)
  if (
    draft.transitions.some(
      (t) => t.from_label === from && t.to_label === to && t.trigger_type === fields.trigger_type && t.path === fields.path,
    )
  ) {
    return { draft, error: `That transition from "${from}" to "${to}" already exists.` }
  }
  return {
    draft: {
      ...draft,
      transitions: [...draft.transitions, { from_label: from, to_label: to, agent_config_id: null, ...fields }],
    },
  }
}

export function removeTransition(draft: WorkflowDraft, index: number): WorkflowDraft {
  return { ...draft, transitions: draft.transitions.filter((_, i) => i !== index) }
}

export function setTransitionKind(draft: WorkflowDraft, index: number, kind: TransitionKind): WorkflowDraft {
  const fields = kindFields(kind)
  return { ...draft, transitions: draft.transitions.map((t, i) => (i === index ? { ...t, ...fields } : t)) }
}

/** True when the label has success-style exits and every one of them is human-triggered. */
export function isGated(draft: WorkflowDraft, name: string): boolean {
  const exits = draft.transitions.filter((t) => t.from_label === name && t.path !== 'failure')
  return exits.length > 0 && exits.every((t) => t.trigger_type === 'human')
}

/**
 * Human approval gate: make the transitions out of `name` human-triggered
 * (failure edges are left alone), or hand them back to the agent (on success).
 */
export function setGate(draft: WorkflowDraft, name: string, gated: boolean): WorkflowDraft {
  return {
    ...draft,
    transitions: draft.transitions.map((t) => {
      if (t.from_label !== name || t.path === 'failure') return t
      return gated
        ? { ...t, trigger_type: 'human', path: null }
        : t.trigger_type === 'human' ? { ...t, trigger_type: 'agent', path: 'success' } : t
    }),
  }
}

/** Client-side mirror of the server's workflow rules (handlers/workflow_yaml.go + repos.go). */
export function validateDraft(draft: WorkflowDraft): DraftError[] {
  const errors: DraftError[] = []
  if (draft.labels.length === 0) return [{ message: 'Workflow must have at least one label.' }]

  const seen = new Set<string>()
  for (const l of draft.labels) {
    if (!l.name.trim()) {
      errors.push({ message: 'Every label needs a name.' })
      continue
    }
    if (seen.has(l.name)) errors.push({ label: l.name, message: `Duplicate label name "${l.name}".` })
    seen.add(l.name)
    const colorErr = checkColor(l.name, l.color)
    if (colorErr) errors.push({ label: l.name, message: colorErr })
    if (l.wip_limit !== null && (!Number.isInteger(l.wip_limit) || l.wip_limit <= 0)) {
      errors.push({ label: l.name, message: `Label "${l.name}": WIP limit must be a positive whole number.` })
    }
    if (l.display_name.length > MAX_DISPLAY_NAME) {
      errors.push({ label: l.name, message: `Label "${l.name}": display name must be at most ${MAX_DISPLAY_NAME} characters.` })
    }
    if (l.verify_mode === 'custom' && !l.verify_command.trim()) {
      errors.push({ label: l.name, message: `Label "${l.name}": custom verify mode needs a command.` })
    }
  }
  const prLabels = draft.labels.filter((l) => l.create_pr)
  if (prLabels.length > 1) errors.push({ message: 'At most one label may open a PR.' })

  for (const t of draft.transitions) {
    for (const ref of [t.from_label, t.to_label]) {
      if (!seen.has(ref)) errors.push({ message: `Transition references unknown label "${ref}".` })
    }
  }
  for (const l of draft.labels) {
    if (l.is_terminal && draft.transitions.some((t) => t.from_label === l.name)) {
      errors.push({ label: l.name, message: `Terminal label "${l.name}" has outgoing transitions.` })
    }
  }
  return errors
}

/**
 * Serialises a draft as the workflow YAML document. JSON is valid YAML, and it
 * sidesteps quoting rules for arbitrary names and commands, so the body is the
 * JSON encoding of the yaml-shaped object, sent as application/yaml.
 */
export function serializeDraft(draft: WorkflowDraft): string {
  const doc = {
    name: draft.name,
    description: draft.description || undefined,
    labels: draft.labels.map((l, i) => ({
      name: l.name,
      display_name: l.display_name.trim() || undefined,
      color: l.color,
      sort_order: i,
      agent_ignore: l.agent_ignore || undefined,
      is_terminal: l.is_terminal || undefined,
      create_pr: l.create_pr || undefined,
      wip_limit: l.wip_limit ?? undefined,
      wip_limit_hard: l.wip_limit !== null && l.wip_limit_hard ? true : undefined,
      verify_mode: l.verify_mode === 'inherit' ? undefined : l.verify_mode,
      verify_command: l.verify_mode === 'custom' ? l.verify_command.trim() : undefined,
      autonomy:
        l.autonomy && l.autonomy.mode !== 'ask'
          ? {
              mode: l.autonomy.mode,
              ask_on: l.autonomy.ask_on,
              hold: `${l.autonomy.hold_seconds}s`,
              max_cost_usd: l.autonomy.max_cost_usd ?? undefined,
            }
          : undefined,
    })),
    transitions: draft.transitions.map((t) => ({
      from: t.from_label,
      to: t.to_label,
      trigger: t.trigger_type,
      path: t.path ?? undefined,
    })),
  }
  return JSON.stringify(doc)
}
