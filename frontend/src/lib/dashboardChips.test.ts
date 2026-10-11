import { describe, it, expect } from 'vitest'
import { buildChipRows } from './dashboardChips'
import type { Workflow } from '../api/client'

const wf = (id: string, name: string, labels: [string, string, number][]): Workflow =>
  ({
    id,
    name,
    description: '',
    labels: labels.map(([n, color, sort_order]) => ({ id: `${id}-${n}`, workflow_id: id, name: n, color, sort_order })),
    transitions: [],
  }) as unknown as Workflow

describe('buildChipRows', () => {
  it('uses the label configured color, falling back to gray', () => {
    const rows = buildChipRows([wf('w1', 'Default', [['work', '#123456', 0]])], { work: 2, ghost: 1 })
    expect(rows).toHaveLength(1)
    expect(rows[0].title).toBeUndefined()
    expect(rows[0].chips.find((c) => c.name === 'work')?.color).toBe('#123456')
    expect(rows[0].chips.find((c) => c.name === 'ghost')?.color).toBe('#6B7280')
  })

  it('renders one titled row per workflow when several exist, with per-workflow counts', () => {
    const rows = buildChipRows(
      [wf('w1', 'Alpha', [['work', '#111111', 0]]), wf('w2', 'Beta', [['work', '#222222', 0], ['done', '#333333', 1]])],
      { work: 5, done: 1 },
      { w1: { work: 2 }, w2: { work: 3, done: 1 } },
    )
    expect(rows.map((r) => r.title)).toEqual(['Alpha', 'Beta'])
    expect(rows[0].chips).toEqual([{ key: 'w1-work', name: 'work', color: '#111111', count: 2 }])
    expect(rows[1].chips.map((c) => [c.name, c.color, c.count])).toEqual([
      ['work', '#222222', 3],
      ['done', '#333333', 1],
    ])
  })
})
