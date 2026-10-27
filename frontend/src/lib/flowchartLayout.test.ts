import { describe, expect, it } from 'vitest'
import {
  FONT, MIN_ZOOM, MIN_RENDERED_TEXT_PX, NODE_WIDTH, DETAILED_NODE_HEIGHT,
  edgeHandles, graphOverflows, initialViewport, isLoopBack, layoutOptions, layoutWorkflow, nodeBadges, rankBySortOrder,
} from './flowchartLayout'

const names = ['intake', 'plan', 'work', 'verify', 'review', 'approve', 'merge', 'done']
const labels = names.map((name, sort_order) => ({ name, sort_order }))
const rankOf = rankBySortOrder(labels)
const tr = (from_label: string, to_label: string, path: string | null, trigger_type: string | null = 'agent') =>
  ({ from_label, to_label, path, trigger_type }) as never
const transitions = [
  ...names.slice(0, -1).map((n, i) => tr(n, names[i + 1], 'success')),
  tr('verify', 'work', 'failure'),
  tr('review', 'plan', 'failure', 'human'),
]

describe('flowchartLayout', () => {
  it('layout options follow direction', () => {
    expect(layoutOptions('LR').rankdir).toBe('LR')
    expect(layoutOptions('TB').rankdir).toBe('TB')
  })

  it('lays out left-to-right in sort order on one main line', () => {
    const { nodes } = layoutWorkflow(names.map((id) => ({ id })), transitions, rankOf, 'LR', DETAILED_NODE_HEIGHT)
    const xs = nodes.map((n) => n.position.x)
    for (let i = 1; i < xs.length; i++) expect(xs[i]).toBeGreaterThan(xs[i - 1])
    expect(new Set(nodes.map((n) => n.position.y)).size).toBe(1)
  })

  it('TB layout stacks in sort order', () => {
    const { nodes } = layoutWorkflow(names.map((id) => ({ id })), transitions, rankOf, 'TB', DETAILED_NODE_HEIGHT)
    const ys = nodes.map((n) => n.position.y)
    for (let i = 1; i < ys.length; i++) expect(ys[i]).toBeGreaterThan(ys[i - 1])
  })

  it('detects loop-backs', () => {
    expect(isLoopBack(tr('verify', 'work', 'failure'), rankOf)).toBe(true)
    expect(isLoopBack(tr('review', 'plan', null, 'human'), rankOf)).toBe(true)
    expect(isLoopBack(tr('work', 'verify', 'success'), rankOf)).toBe(false)
  })

  it('routes handles', () => {
    expect(edgeHandles(tr('verify', 'work', 'failure', 'agent'), true)).toEqual({ source: 'loop-b', target: 't-loop-b' })
    expect(edgeHandles(tr('review', 'plan', 'failure', 'human'), true)).toEqual({ source: 'loop-a', target: 't-loop-a' })
    expect(edgeHandles(tr('a', 'b', 'success', 'human'), false)).toEqual({ source: 'out-a', target: 'in-a' })
    expect(edgeHandles(tr('a', 'b', 'success', 'agent'), false)).toEqual({ source: 'out-b', target: 'in-b' })
    expect(edgeHandles(tr('a', 'b', 'success', 'both'), false)).toEqual({ source: 'out', target: 'in' })
  })

  it('zoom floor keeps text at or above the minimum', () => {
    expect(MIN_ZOOM * Math.min(...Object.values(FONT))).toBeGreaterThanOrEqual(MIN_RENDERED_TEXT_PX - 1e-9)
  })

  it('initialViewport centers small graphs and start-aligns wide ones', () => {
    const small = { x: 0, y: 0, width: 300, height: 100 }
    const v = initialViewport(small, { width: 1400, height: 600 })
    expect(v.zoom).toBeLessThanOrEqual(1)
    expect(v.x).toBeCloseTo((1400 - 300 * v.zoom) / 2)
    expect(graphOverflows(small, { width: 1400, height: 600 }, v.zoom)).toBe(false)

    const wide = { x: 0, y: 0, width: 8 * NODE_WIDTH + 7 * 64, height: DETAILED_NODE_HEIGHT }
    const w = initialViewport(wide, { width: 1400, height: 600 })
    expect(w.zoom).toBe(MIN_ZOOM)
    expect(w.x).toBe(24)
    expect(graphOverflows(wide, { width: 1400, height: 600 }, w.zoom)).toBe(true)
    expect(graphOverflows(wide, { width: 0, height: 0 }, w.zoom)).toBe(false)
  })

  it('nodeBadges', () => {
    const base = { is_terminal: 0, create_pr: 0, wip_limit: null, wip_limit_hard: 0, verify_mode: 'inherit', agent_ignore: 0 } as never
    expect(nodeBadges(base)).toEqual([])
    expect(nodeBadges({ ...(base as object), wip_limit: 0 } as never)).toEqual([])
    expect(nodeBadges({ ...(base as object), wip_limit: 3 } as never)).toEqual(['WIP 3'])
    expect(nodeBadges({ ...(base as object), wip_limit: 3, wip_limit_hard: 1, create_pr: 1, is_terminal: 1, verify_mode: 'custom' } as never))
      .toEqual(['Terminal', 'Opens PR', 'WIP 3 (hard)', 'Verify: custom'])
  })
})
