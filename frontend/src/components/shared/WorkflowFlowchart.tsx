import { useCallback, useMemo } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  MarkerType,
  Position,
  Handle,
  type NodeProps,
  type Node,
  type Edge,
  BackgroundVariant,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import dagre from 'dagre'
import type { Workflow, WorkflowTransition } from '../../api/client'
import { useThemeStore } from '../../stores/theme'
import type { LabelStaffing } from '../../lib/staffing'

// ── Constants ────────────────────────────────────────────────────────────────
const NODE_WIDTH = 180
const NODE_HEIGHT = 50
// Pipeline overview nodes carry extra rows (staffing, markers), so they are taller.
const DETAILED_NODE_HEIGHT = 92

// Handles must exist for edge routing but shouldn't render as visible dots.
const HANDLE_HIDDEN = { opacity: 0, width: 1, height: 1, border: 'none', background: 'transparent' } as const

// Edit mode: the center top (target) / bottom (source) handles are drag handles.
const HANDLE_VISIBLE = { width: 12, height: 12, background: '#6366f1', border: '2px solid #e2e8f0' } as const

// ── Helpers ──────────────────────────────────────────────────────────────────

function edgeColor(path: string | null | undefined): string {
  if (path === 'success') return '#22c55e' // green-500
  if (path === 'failure') return '#ef4444' // red-500
  return '#64748b' // slate-500 for either/null
}

/** Which side the failure edge uses (also the target handle it connects into) */
function failureSide(t: WorkflowTransition): 'left' | 'right' {
  return t.trigger_type === 'agent' ? 'right' : 'left'
}

/** Lane offset for success edges so human/agent arrows don't overlap on center */
function successLane(t: WorkflowTransition): '-l' | '-r' | '' {
  if (t.trigger_type === 'human') return '-l' // human → left of center
  if (t.trigger_type === 'agent') return '-r' // agent → right of center
  return '' // both/null → center
}

/** Which handle the edge exits from on the source node */
function sourceHandle(t: WorkflowTransition): string {
  if (t.path === 'failure') return failureSide(t)
  return `bottom${successLane(t)}`
}

/** Which handle the edge connects into on the target node */
function targetHandle(t: WorkflowTransition): string {
  // failures loop back to an earlier label; connect on the side so the arrow
  // approaches horizontally instead of crossing through other nodes to the top.
  if (t.path === 'failure') return `t-${failureSide(t)}`
  return `top${successLane(t)}`
}

// ── Dagre layout ─────────────────────────────────────────────────────────────

function layoutNodes(nodes: Node[], edges: Edge[], nodeHeight: number): Node[] {
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: 'TB', ranksep: 80, nodesep: 50 })
  g.setDefaultEdgeLabel(() => ({}))

  nodes.forEach((n) => g.setNode(n.id, { width: NODE_WIDTH, height: nodeHeight }))
  edges.forEach((e) => g.setEdge(e.source, e.target))

  dagre.layout(g)

  // Group node centers by rank (y) so ranks with a single node can be aligned
  // to a shared center column; multi-node ranks keep dagre's centering.
  const byRank = new Map<number, string[]>()
  nodes.forEach((n) => {
    const y = g.node(n.id).y
    const ids = byRank.get(y) ?? []
    ids.push(n.id)
    byRank.set(y, ids)
  })

  const allX = nodes.map((n) => g.node(n.id).x)
  const centerX = (Math.min(...allX) + Math.max(...allX)) / 2

  return nodes.map((n) => {
    const pos = g.node(n.id)
    const alone = (byRank.get(pos.y)?.length ?? 0) === 1
    const x = alone ? centerX : pos.x
    return {
      ...n,
      position: {
        x: x - NODE_WIDTH / 2,
        y: pos.y - nodeHeight / 2,
      },
    }
  })
}

// ── Custom Node ───────────────────────────────────────────────────────────────

interface LabelNodeData {
  label: string
  color: string
  isTerminal: boolean
  agentIgnore: boolean
  /** Present only in the detailed (Pipeline overview) rendering. */
  detail?: {
    staffing: LabelStaffing
    verifyMode: string
    createPr: boolean
    selected: boolean
    onSelect?: (name: string) => void
    /** Edit mode: the top/bottom handles become visible drag targets. */
    editable?: boolean
  }
}

function StaffingRow({ s }: { s: LabelStaffing }) {
  const chip = 'rounded-full border px-1.5 leading-4 text-[10px] font-medium'
  if (s.kind === 'unstaffed') {
    return <span className={`${chip} border-amber-600 bg-amber-900/40 text-amber-300`}>No agent assigned</span>
  }
  if (s.kind === 'human') {
    return <span className={`${chip} border-slate-600 bg-slate-800 text-slate-300`}>Human gate</span>
  }
  if (s.kind === 'terminal') {
    return <span className="text-[10px] text-slate-500">Done</span>
  }
  const active = s.agents.filter((a) => a.enabled)
  const first = active[0]
  return (
    <span className="max-w-full truncate text-[10px] text-slate-300" title={active.map((a) => `${a.name} (${a.provider_config?.provider ?? '?'})`).join(', ')}>
      {first?.name}
      {first?.provider_config?.provider ? ` · ${first.provider_config.provider}` : ''}
      {active.length > 1 ? ` +${active.length - 1}` : ''}
    </span>
  )
}

function WorkflowLabelNode({ data }: NodeProps) {
  const d = data as unknown as LabelNodeData
  const color = d.color || '#6366f1'
  const detail = d.detail
  const markers = detail
    ? [
        d.isTerminal ? 'terminal' : null,
        detail.createPr ? 'opens PR' : null,
        detail.verifyMode !== 'inherit' ? `verify: ${detail.verifyMode}` : null,
      ].filter(Boolean)
    : []

  return (
    <div
      role={detail?.onSelect ? 'button' : undefined}
      tabIndex={detail?.onSelect ? 0 : undefined}
      aria-label={detail ? `${d.label} stage` : undefined}
      aria-pressed={detail?.onSelect ? detail.selected : undefined}
      data-testid={detail ? `pipeline-node-${d.label}` : undefined}
      onKeyDown={
        detail?.onSelect
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                detail.onSelect?.(d.label)
              }
            }
          : undefined
      }
      className={detail?.onSelect ? 'cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400' : undefined}
      style={{
        width: NODE_WIDTH,
        height: detail ? DETAILED_NODE_HEIGHT : NODE_HEIGHT,
        border: `2px solid ${color}`,
        borderRadius: 8,
        background: `${color}22`,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        position: 'relative',
        flexDirection: detail ? 'column' : undefined,
        boxShadow: detail?.selected
          ? `0 0 0 3px ${color}`
          : d.isTerminal ? `0 0 0 3px ${color}66` : undefined,
      }}
    >
      {/* Handles — kept functional but visually hidden */}
      <Handle type="target" position={Position.Top} id="top" style={detail?.editable ? HANDLE_VISIBLE : HANDLE_HIDDEN} />
      <Handle type="target" position={Position.Top} id="top-l" style={{ ...HANDLE_HIDDEN, left: '30%' }} />
      <Handle type="target" position={Position.Top} id="top-r" style={{ ...HANDLE_HIDDEN, left: '70%' }} />
      <Handle type="source" position={Position.Bottom} id="bottom" style={detail?.editable ? HANDLE_VISIBLE : HANDLE_HIDDEN} />
      <Handle type="source" position={Position.Bottom} id="bottom-l" style={{ ...HANDLE_HIDDEN, left: '30%' }} />
      <Handle type="source" position={Position.Bottom} id="bottom-r" style={{ ...HANDLE_HIDDEN, left: '70%' }} />
      <Handle type="source" position={Position.Right} id="right" style={HANDLE_HIDDEN} />
      <Handle type="source" position={Position.Left} id="left" style={HANDLE_HIDDEN} />
      {/* Target handles for failure edges that loop back on a side */}
      <Handle type="target" position={Position.Right} id="t-right" style={{ ...HANDLE_HIDDEN, top: '65%' }} />
      <Handle type="target" position={Position.Left} id="t-left" style={{ ...HANDLE_HIDDEN, top: '65%' }} />

      {/* Label text */}
      <div style={{ textAlign: 'center', padding: '0 8px' }}>
        <span
          style={{
            fontSize: 12,
            fontWeight: 600,
            color: color,
            display: 'block',
            lineHeight: 1.3,
            wordBreak: 'break-word',
            fontStyle: d.agentIgnore ? 'italic' : 'normal',
          }}
        >
          {d.label}
        </span>
        {!detail && (
          <span style={{ fontSize: 9, color: '#94a3b8', lineHeight: 1 }}>
            {d.isTerminal && '⊠ terminal'}
            {!d.isTerminal && d.agentIgnore && 'agent ignore'}
          </span>
        )}
      </div>
      {detail && (
        <div className="mt-1 flex max-w-full flex-col items-center gap-0.5 px-2">
          <StaffingRow s={detail.staffing} />
          {markers.length > 0 && (
            <span className="text-[9px] text-slate-500">{markers.join(' · ')}</span>
          )}
        </div>
      )}
    </div>
  )
}

const nodeTypes = { workflowLabel: WorkflowLabelNode }

// ── Main Component ────────────────────────────────────────────────────────────

interface Props {
  workflow: Workflow | null
  /** When set, nodes render in detailed mode: who works each label, plus markers. */
  staffing?: Map<string, LabelStaffing>
  /** Detailed mode only: makes nodes clickable. */
  onSelectLabel?: (name: string) => void
  selectedLabel?: string | null
  /** Detailed mode only: nodes can be connected by dragging from the bottom handle to another node's top handle. */
  onConnectLabels?: (from: string, to: string) => void
}

export default function WorkflowFlowchart({ workflow, staffing, onSelectLabel, selectedLabel, onConnectLabels }: Props) {
  const editable = Boolean(onConnectLabels && staffing)
  const theme = useThemeStore((s) => s.theme)
  const isDark = theme === 'dark'
  const { nodes: layoutedNodes, edges } = useMemo(() => {
    if (!workflow?.labels?.length) return { nodes: [], edges: [] }

    // Rank each label by its position in sort order (0,1,2,…) so failure edges
    // can fan out by how far they span — wider jumps bow out farther.
    const sorted = [...(workflow.labels ?? [])].sort(
      (a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0),
    )
    const rankOf = new Map<string, number>()
    sorted.forEach((lbl, idx) => rankOf.set(lbl.name!, idx))

    // Build nodes sorted by sort_order
    const rawNodes: Node[] = sorted
      .map((lbl) => ({
        id: lbl.name!,
        type: 'workflowLabel',
        position: { x: 0, y: 0 }, // dagre will override
        data: {
          label: lbl.name,
          color: lbl.color || '#6366f1',
          isTerminal: Boolean(lbl.is_terminal),
          agentIgnore: Boolean(lbl.agent_ignore),
          detail: staffing
            ? {
                staffing: staffing.get(lbl.name!) ?? { kind: 'human', agents: [] },
                verifyMode: lbl.verify_mode ?? 'inherit',
                createPr: Boolean(lbl.create_pr),
                selected: selectedLabel === lbl.name,
                onSelect: onSelectLabel,
                editable,
              }
            : undefined,
        },
      }))

    // Build edges
    const rawEdges: Edge[] = (workflow.transitions ?? []).map((t, i) => {
      const color = edgeColor(t.path)
      const isAgent = t.trigger_type === 'agent'
      const isBoth = t.trigger_type === 'both'

      // Failure edges travel along the side; bow each one out by how many ranks
      // it spans so longer loop-backs sit in their own lane instead of stacking.
      let pathOptions: { offset: number } | undefined
      if (t.path === 'failure') {
        const span = Math.abs(
          (rankOf.get(t.to_label!) ?? 0) - (rankOf.get(t.from_label!) ?? 0),
        )
        pathOptions = { offset: 20 + span * 24 }
      }

      return {
        id: `e-${t.from_label}-${t.to_label}-${i}`,
        source: t.from_label!,
        target: t.to_label!,
        sourceHandle: sourceHandle(t),
        targetHandle: targetHandle(t),
        type: 'smoothstep',
        pathOptions,
        animated: false,
        style: {
          stroke: color,
          strokeWidth: 2,
          strokeDasharray: isAgent ? '6 3' : isBoth ? '2 2' : undefined,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color,
          width: 16,
          height: 16,
        },
        label: t.path ? t.path : undefined,
        labelStyle: { fontSize: 9, fill: color, fontWeight: 600 },
        labelBgStyle: { fill: isDark ? '#0f1117' : '#ffffff', fillOpacity: 0.8 },
      }
    })

    // Apply dagre layout
    const nodes = layoutNodes(rawNodes, rawEdges, staffing ? DETAILED_NODE_HEIGHT : NODE_HEIGHT)

    return { nodes, edges: rawEdges }
  }, [workflow, staffing, onSelectLabel, selectedLabel, isDark, editable])

  const onInit = useCallback(() => {}, [])

  if (!workflow) {
    return (
      <div className="flex items-center justify-center h-full text-slate-500 text-sm">
        Loading chart…
      </div>
    )
  }

  if (!workflow.labels?.length) {
    return (
      <div className="flex items-center justify-center h-full text-slate-500 text-sm">
        No labels defined yet.
      </div>
    )
  }

  return (
    <div style={{ width: '100%', height: '100%' }}>
      <ReactFlow
        nodes={layoutedNodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onInit={onInit}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        nodesDraggable={false}
        nodesConnectable={editable}
        onConnect={editable ? (c) => { if (c.source && c.target) onConnectLabels?.(c.source, c.target) } : undefined}
        // React Flow only lets pointer events through to a node when the
        // canvas has a node click handler, so selection goes through here.
        onNodeClick={onSelectLabel && staffing ? (_, n) => onSelectLabel(n.id) : undefined}
        elementsSelectable={false}
        panOnScroll
        zoomOnScroll={false}
        colorMode={isDark ? 'dark' : 'light'}
      >
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} color={isDark ? '#1e293b' : '#cbd5e1'} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  )
}
