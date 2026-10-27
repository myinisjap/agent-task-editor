import { describe, it, expect, vi } from 'vitest'
import { buildTaskCommands, type TaskCommandHandlers } from './taskCommands'
import type { Task, Workflow } from '../api/client'

const wf = {
  id: 'w', name: 'W',
  labels: [
    { name: 'todo', sort_order: 0 }, { name: 'review', sort_order: 1 }, { name: 'done', sort_order: 2 },
  ],
  transitions: [
    { from_label: 'review', to_label: 'done', trigger_type: 'human', path: 'success' },
    { from_label: 'review', to_label: 'todo', trigger_type: 'human', path: 'failure' },
    { from_label: 'todo', to_label: 'review', trigger_type: 'agent' },
  ],
} as unknown as Workflow

const handlers = (): TaskCommandHandlers => ({
  moveLabel: vi.fn(), approve: vi.fn(), requestChanges: vi.fn(), rerun: vi.fn(),
  createPR: vi.fn(), openPR: vi.fn(), togglePause: vi.fn(), toggleArchive: vi.fn(),
})
const task = (o: Partial<Task> = {}) => ({ id: 't', label: 'review', paused: false, archived: false, ...o }) as Task
const labels = (c: ReturnType<typeof buildTaskCommands>) => c.map((x) => x.label)

describe('buildTaskCommands', () => {
  it('offers only legal moves, approve and request changes', () => {
    const l = labels(buildTaskCommands({ task: task(), workflow: wf, canRerun: false, handlers: handlers() }))
    expect(l).toContain('Move to Done')
    expect(l).toContain('Move to Todo')
    expect(l).not.toContain('Move to Review')
    expect(l).toContain('Approve')
    expect(l).toContain('Request changes')
  })
  it('hides approve/reject off a gate and gates rerun', () => {
    const c = buildTaskCommands({ task: task({ label: 'todo' }), workflow: wf, canRerun: false, handlers: handlers() })
    expect(labels(c)).not.toContain('Approve')
    expect(labels(c)).not.toContain('Request changes')
    expect(labels(c)).not.toContain('Rerun')
    const c2 = buildTaskCommands({ task: task(), workflow: wf, canRerun: true, handlers: handlers() })
    expect(labels(c2)).toContain('Rerun')
  })
  it('toggles labels and PR commands', () => {
    const l = labels(buildTaskCommands({ task: task({ paused: true, archived: true, pr_url: 'https://x/pr/1' }), workflow: wf, canRerun: false, handlers: handlers() }))
    expect(l).toContain('Resume'); expect(l).toContain('Unarchive'); expect(l).toContain('Open PR')
    const l2 = labels(buildTaskCommands({ task: task({ branch: 'b' }), workflow: wf, canRerun: false, handlers: handlers() }))
    expect(l2).toContain('Pause'); expect(l2).toContain('Archive'); expect(l2).toContain('Create PR')
  })
  it('run calls the right handler', () => {
    const h = handlers()
    const c = buildTaskCommands({ task: task(), workflow: wf, canRerun: true, handlers: h })
    c.find((x) => x.label === 'Move to Done')!.run()
    expect(h.moveLabel).toHaveBeenCalledWith('done')
    c.find((x) => x.label === 'Approve')!.run()
    expect(h.approve).toHaveBeenCalled()
    c.find((x) => x.label === 'Archive')!.run()
    expect(h.toggleArchive).toHaveBeenCalled()
    c.find((x) => x.label === 'Request changes')!.run()
    expect(h.requestChanges).toHaveBeenCalled()
  })
})
