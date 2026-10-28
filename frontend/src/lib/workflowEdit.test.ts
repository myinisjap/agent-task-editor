import { describe, it, expect } from 'vitest'
import type { Workflow } from '../api/client'
import {
  addLabel,
  addTransition,
  deleteLabel,
  draftFromWorkflow,
  isDirty,
  isGated,
  removeTransition,
  renameLabel,
  serializeDraft,
  setGate,
  setTransitionKind,
  updateLabel,
  validateDraft,
} from './workflowEdit'

const wf = {
  id: 'w1', name: 'Main', description: 'd', created_at: '', updated_at: 't1',
  labels: [
    { id: 'a', workflow_id: 'w1', name: 'work', color: '#111111', sort_order: 1, agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit_hard: 0, verify_mode: 'inherit', verify_command: '' },
    { id: 'b', workflow_id: 'w1', name: 'todo', color: '#222222', sort_order: 0, agent_ignore: 1, is_terminal: 0, create_pr: 0, wip_limit: 3, wip_limit_hard: 1, verify_mode: 'inherit', verify_command: '' },
    { id: 'c', workflow_id: 'w1', name: 'done', color: '', sort_order: 2, agent_ignore: 0, is_terminal: 1, create_pr: 1, wip_limit_hard: 0, verify_mode: 'custom', verify_command: 'make test' },
  ],
  transitions: [
    { id: 't1', workflow_id: 'w1', from_label: 'todo', to_label: 'work', trigger_type: 'human', path: null },
    { id: 't2', workflow_id: 'w1', from_label: 'work', to_label: 'done', trigger_type: 'agent', path: 'success' },
  ],
} as unknown as Workflow

const base = () => draftFromWorkflow(wf)

describe('workflowEdit', () => {
  it('builds a draft sorted by sort_order with booleans', () => {
    const d = base()
    expect(d.labels.map((l) => l.name)).toEqual(['todo', 'work', 'done'])
    expect(d.labels[0]).toMatchObject({ agent_ignore: true, wip_limit: 3, wip_limit_hard: true })
    expect(d.labels[2]).toMatchObject({ is_terminal: true, create_pr: true, verify_mode: 'custom' })
  })

  it('adds a label and rejects empty, duplicate and bad-colour names', () => {
    const d = base()
    const ok = addLabel(d, ' review ', '#abcdef')
    expect(ok.error).toBeUndefined()
    expect(ok.draft.labels.at(-1)).toMatchObject({ name: 'review', color: '#abcdef' })
    expect(d.labels).toHaveLength(3) // input not mutated
    expect(addLabel(d, '').error).toMatch(/required/)
    expect(addLabel(d, 'work').error).toMatch(/already exists/)
    expect(addLabel(d, 'x', 'red').error).toMatch(/hex/)
    expect(addLabel(d, 'work').draft).toBe(d)
  })

  it('renames a label and rewrites transitions; rejects duplicates', () => {
    const d = base()
    const r = renameLabel(d, 'work', 'build')
    expect(r.draft.labels.map((l) => l.name)).toContain('build')
    expect(r.draft.transitions.map((t) => [t.from_label, t.to_label])).toEqual([['todo', 'build'], ['build', 'done']])
    expect(renameLabel(d, 'work', 'done').error).toMatch(/already exists/)
    expect(renameLabel(d, 'work', '  ').error).toMatch(/required/)
    expect(renameLabel(d, 'nope', 'x').error).toMatch(/No label/)
    expect(renameLabel(d, 'work', 'work').draft).toBe(d)
  })

  it('deletes a label together with its transitions', () => {
    const d = deleteLabel(base(), 'work')
    expect(d.labels.map((l) => l.name)).toEqual(['todo', 'done'])
    expect(d.transitions).toEqual([])
  })

  it('adds and removes transitions with validation', () => {
    const d = base()
    const r = addTransition(d, 'work', 'todo', 'agent_failure')
    expect(r.draft.transitions.at(-1)).toMatchObject({ from_label: 'work', to_label: 'todo', trigger_type: 'agent', path: 'failure' })
    expect(addTransition(d, 'work', 'ghost', 'human').error).toMatch(/not a label/)
    expect(addTransition(d, 'ghost', 'work', 'human').error).toMatch(/not a label/)
    expect(addTransition(d, 'work', 'work', 'human').error).toMatch(/itself/)
    expect(addTransition(d, 'work', 'done', 'agent_success').error).toMatch(/already exists/)
    expect(removeTransition(r.draft, 0).transitions).toHaveLength(2)
  })

  it('changes a transition kind', () => {
    const d = setTransitionKind(base(), 0, 'agent_success')
    expect(d.transitions[0]).toMatchObject({ trigger_type: 'agent', path: 'success' })
  })

  it('toggles a human gate on outgoing non-failure transitions', () => {
    let d = base()
    expect(isGated(d, 'work')).toBe(false)
    d = setGate(d, 'work', true)
    expect(isGated(d, 'work')).toBe(true)
    expect(d.transitions[1]).toMatchObject({ trigger_type: 'human', path: null })
    d = setGate(d, 'work', false)
    expect(d.transitions[1]).toMatchObject({ trigger_type: 'agent', path: 'success' })
  })

  it('updateLabel keeps create_pr unique', () => {
    const d = updateLabel(base(), 'work', { create_pr: true })
    expect(d.labels.filter((l) => l.create_pr).map((l) => l.name)).toEqual(['work'])
  })

  it('detects dirtiness', () => {
    const d = base()
    expect(isDirty(d, base())).toBe(false)
    expect(isDirty(updateLabel(d, 'work', { is_terminal: true }), d)).toBe(true)
  })

  it('validates mirrored server rules', () => {
    expect(validateDraft(base())).toEqual([])
    const bad = {
      ...base(),
      labels: [
        { ...base().labels[0], name: 'a', color: 'blue', wip_limit: 0 },
        { ...base().labels[0], name: 'a', verify_mode: 'custom' as const, verify_command: ' ', create_pr: true },
        { ...base().labels[0], name: 'z', create_pr: true, is_terminal: true },
      ],
      transitions: [
        { from_label: 'a', to_label: 'ghost', trigger_type: 'human' as const, path: null, agent_config_id: null },
        { from_label: 'z', to_label: 'a', trigger_type: 'human' as const, path: null, agent_config_id: null },
      ],
    }
    const msgs = validateDraft(bad).map((e) => e.message).join('\n')
    expect(msgs).toMatch(/Duplicate label name "a"/)
    expect(msgs).toMatch(/hex/)
    expect(msgs).toMatch(/WIP limit/)
    expect(msgs).toMatch(/custom verify mode needs a command/)
    expect(msgs).toMatch(/At most one label may open a PR/)
    expect(msgs).toMatch(/unknown label "ghost"/)
    expect(msgs).toMatch(/Terminal label "z"/)
    expect(validateDraft({ ...base(), labels: [] })[0].message).toMatch(/at least one/)
  })

  it('round-trips display_name and validates its length', () => {
    const d = base()
    const withName = updateLabel(d, 'work', { display_name: '  Doing  ' })
    expect(JSON.parse(serializeDraft(withName)).labels[1].display_name).toBe('Doing')
    expect(JSON.parse(serializeDraft(d)).labels[1]).not.toHaveProperty('display_name')
    const long = updateLabel(d, 'work', { display_name: 'x'.repeat(65) })
    expect(validateDraft(long).map((e) => e.message).join()).toMatch(/display name/)
  })

  it('serialises to the yaml-shaped document', () => {
    const doc = JSON.parse(serializeDraft(base()))
    expect(doc.name).toBe('Main')
    expect(doc.labels[0]).toEqual({ name: 'todo', color: '#222222', sort_order: 0, agent_ignore: true, wip_limit: 3, wip_limit_hard: true })
    expect(doc.labels[2]).toMatchObject({ is_terminal: true, create_pr: true, verify_mode: 'custom', verify_command: 'make test' })
    expect(doc.transitions[0]).toEqual({ from: 'todo', to: 'work', trigger: 'human' })
    expect(doc.transitions[1]).toEqual({ from: 'work', to: 'done', trigger: 'agent', path: 'success' })
  })

  it('preserves a label autonomy block through a draft save', () => {
    const d = base()
    expect(JSON.parse(serializeDraft(d)).labels[1]).not.toHaveProperty('autonomy')
    const withAuto = updateLabel(d, 'work', {
      autonomy: { mode: 'on_flags', ask_on: ['migration'], hold_seconds: 600, max_cost_usd: 5 },
    })
    expect(JSON.parse(serializeDraft(withAuto)).labels[1].autonomy).toEqual({
      mode: 'on_flags', ask_on: ['migration'], hold: '600s', max_cost_usd: 5,
    })
    const ask = updateLabel(d, 'work', { autonomy: { mode: 'ask', ask_on: [], hold_seconds: 600, max_cost_usd: null } })
    expect(JSON.parse(serializeDraft(ask)).labels[1]).not.toHaveProperty('autonomy')
  })
})
