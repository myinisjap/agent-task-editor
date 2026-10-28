import dagre from 'dagre'
import type { WorkflowLabel, WorkflowTransition } from '../api/client'
import type { LabelStaffing } from './staffing'

export const NODE_WIDTH = 220
export const SIMPLE_NODE_HEIGHT = 76
export const DETAILED_NODE_HEIGHT = 140

/** Font sizes (px) used inside nodes and edge labels. */
export const FONT = { title: 16, body: 14, meta: 13 } as const

export const MIN_RENDERED_TEXT_PX = 12
/** Zoom floor: the smallest font times this never drops below MIN_RENDERED_TEXT_PX. */
export const MIN_ZOOM = MIN_RENDERED_TEXT_PX / Math.min(...Object.values(FONT))
export const MAX_ZOOM = 1.5
export const FIT_MAX_ZOOM = 1

export type FlowDirection = 'LR' | 'TB'

export function layoutOptions(dir: FlowDirection) {
  return { rankdir: dir, ranksep: dir === 'LR' ? 64 : 56, nodesep: 40, marginx: 0, marginy: 0 }
}

export function rankBySortOrder(labels: Pick<WorkflowLabel, 'name' | 'sort_order'>[]): Map<string, number> {
  const sorted = [...labels].sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0))
  return new Map(sorted.map((l, i) => [l.name, i]))
}

type TransitionLike = Pick<WorkflowTransition, 'from_label' | 'to_label' | 'path' | 'trigger_type'>

export function isLoopBack(t: TransitionLike, rankOf: Map<string, number>): boolean {
  if (t.path === 'failure') return true
  return (rankOf.get(t.to_label ?? '') ?? 0) < (rankOf.get(t.from_label ?? '') ?? 0)
}

export interface Bounds { x: number; y: number; width: number; height: number }

export function layoutWorkflow<N extends { id: string }>(
  nodes: N[],
  transitions: TransitionLike[],
  rankOf: Map<string, number>,
  dir: FlowDirection,
  nodeHeight: number,
): { nodes: (N & { position: { x: number; y: number } })[]; bounds: Bounds } {
  const g = new dagre.graphlib.Graph()
  g.setGraph(layoutOptions(dir))
  g.setDefaultEdgeLabel(() => ({}))
  nodes.forEach((n) => g.setNode(n.id, { width: NODE_WIDTH, height: nodeHeight }))
  const ids = new Set(nodes.map((n) => n.id))
  transitions.forEach((t) => {
    const f = t.from_label ?? '', to = t.to_label ?? ''
    if (!ids.has(f) || !ids.has(to)) return
    // Only forward edges shape the columns; loop-backs are routed around them.
    if ((rankOf.get(f) ?? 0) < (rankOf.get(to) ?? 0)) g.setEdge(f, to)
  })
  dagre.layout(g)

  const lr = dir === 'LR'
  // Group by rank coordinate; single-node ranks share one main-line coordinate.
  const groups = new Map<number, string[]>()
  nodes.forEach((n) => {
    const p = g.node(n.id)
    const k = lr ? p.x : p.y
    groups.set(k, [...(groups.get(k) ?? []), n.id])
  })
  const cross = nodes.map((n) => (lr ? g.node(n.id).y : g.node(n.id).x))
  const mainLine = (Math.min(...cross) + Math.max(...cross)) / 2

  const out = nodes.map((n) => {
    const p = g.node(n.id)
    const alone = (groups.get(lr ? p.x : p.y)?.length ?? 0) === 1
    const cx = lr ? p.x : alone ? mainLine : p.x
    const cy = lr ? (alone ? mainLine : p.y) : p.y
    return { ...n, position: { x: cx - NODE_WIDTH / 2, y: cy - nodeHeight / 2 } }
  })
  const xs = out.map((n) => n.position.x)
  const ys = out.map((n) => n.position.y)
  const x = Math.min(...xs), y = Math.min(...ys)
  return {
    nodes: out,
    bounds: { x, y, width: Math.max(...xs) + NODE_WIDTH - x, height: Math.max(...ys) + nodeHeight - y },
  }
}

// ── Handles (IDs are direction-neutral; the node maps them to Positions) ──────

export interface EdgeHandles { source: string; target: string }

export function edgeHandles(t: TransitionLike, loopBack: boolean): EdgeHandles {
  if (loopBack) {
    // LR: agent below, human/other above. TB: agent right, human/other left.
    const side = t.trigger_type === 'agent' ? 'b' : 'a'
    return { source: `loop-${side}`, target: `t-loop-${side}` }
  }
  const lane = t.trigger_type === 'human' ? '-a' : t.trigger_type === 'agent' ? '-b' : ''
  return { source: `out${lane}`, target: `in${lane}` }
}

export function loopOffset(span: number): number {
  return 20 + span * 24
}

// ── Viewport ─────────────────────────────────────────────────────────────────

export interface ViewportSize { width: number; height: number }
interface ZoomOpts { minZoom?: number; maxZoom?: number; padding?: number }

export function initialViewport(bounds: Bounds, vp: ViewportSize, opts: ZoomOpts = {}) {
  const { minZoom = MIN_ZOOM, maxZoom = FIT_MAX_ZOOM, padding = 24 } = opts
  const fit = Math.min((vp.width - 2 * padding) / bounds.width, (vp.height - 2 * padding) / bounds.height)
  const zoom = Math.min(maxZoom, Math.max(minZoom, fit))
  const axis = (b: number, o: number, v: number) =>
    b * zoom + 2 * padding <= v ? (v - b * zoom) / 2 - o * zoom : padding - o * zoom
  return { x: axis(bounds.width, bounds.x, vp.width), y: axis(bounds.height, bounds.y, vp.height), zoom }
}

export function graphOverflows(bounds: Bounds, vp: ViewportSize, zoom: number, padding = 24): boolean {
  if (!vp.width || !vp.height) return false
  return bounds.width * zoom + 2 * padding > vp.width + 0.5 || bounds.height * zoom + 2 * padding > vp.height + 0.5
}

// ── Node content ─────────────────────────────────────────────────────────────

export function nodeBadges(l: Pick<WorkflowLabel, 'is_terminal' | 'create_pr' | 'wip_limit' | 'wip_limit_hard' | 'verify_mode' | 'agent_ignore'>): string[] {
  const out: string[] = []
  if (l.is_terminal) out.push('Terminal')
  if (l.create_pr) out.push('Opens PR')
  if (l.wip_limit && l.wip_limit > 0) out.push(`WIP ${l.wip_limit}${l.wip_limit_hard ? ' (hard)' : ''}`)
  if (l.verify_mode && l.verify_mode !== 'inherit') out.push(`Verify: ${l.verify_mode}`)
  if (l.agent_ignore) out.push('Agent ignore')
  return out
}

export function agentSummary(s: LabelStaffing): { name: string; sub: string; extra: number; title: string } | null {
  if (s.kind !== 'agent') return null
  const active = s.agents.filter((a) => a.enabled)
  const first = active[0]
  if (!first) return null
  const prov = first.provider_config?.provider ?? ''
  const model = first.provider_config?.model ?? ''
  return {
    name: first.name,
    sub: prov + (model ? (prov ? ' / ' : '') + model : ''),
    extra: active.length - 1,
    title: active.map((a) => `${a.name} (${a.provider_config?.provider ?? '?'}${a.provider_config?.model ? ' / ' + a.provider_config.model : ''})`).join(', '),
  }
}
