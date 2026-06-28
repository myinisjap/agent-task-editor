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

// ── Constants ────────────────────────────────────────────────────────────────
const NODE_WIDTH = 180
const NODE_HEIGHT = 50

// Handles must exist for edge routing but shouldn't render as visible dots.
const HANDLE_HIDDEN = { opacity: 0, width: 1, height: 1, border: 'none', background: 'transparent' } as const

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

function layoutNodes(nodes: Node[], edges: Edge[]): Node[] {
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: 'TB', ranksep: 80, nodesep: 50 })
  g.setDefaultEdgeLabel(() => ({}))

  nodes.forEach((n) => g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHT }))
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
        y: pos.y - NODE_HEIGHT / 2,
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
}

function WorkflowLabelNode({ data }: NodeProps) {
  const d = data as unknown as LabelNodeData
  const color = d.color || '#6366f1'

  return (
    <div
      style={{
        width: NODE_WIDTH,
        height: NODE_HEIGHT,
        border: `2px solid ${color}`,
        borderRadius: 8,
        background: `${color}22`,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        position: 'relative',
        boxShadow: d.isTerminal ? `0 0 0 3px ${color}66` : undefined,
      }}
    >
      {/* Handles — kept functional but visually hidden */}
      <Handle type="target" position={Position.Top} id="top" style={HANDLE_HIDDEN} />
      <Handle type="target" position={Position.Top} id="top-l" style={{ ...HANDLE_HIDDEN, left: '30%' }} />
      <Handle type="target" position={Position.Top} id="top-r" style={{ ...HANDLE_HIDDEN, left: '70%' }} />
      <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_HIDDEN} />
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
        <span style={{ fontSize: 9, color: '#94a3b8', lineHeight: 1 }}>
          {d.isTerminal && '⊠ terminal'}
          {!d.isTerminal && d.agentIgnore && 'agent ignore'}
        </span>
      </div>
    </div>
  )
}

const nodeTypes = { workflowLabel: WorkflowLabelNode }

// ── Main Component ────────────────────────────────────────────────────────────

interface Props {
  workflow: Workflow | null
}

export default function WorkflowFlowchart({ workflow }: Props) {
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
        labelBgStyle: { fill: '#0f1117', fillOpacity: 0.8 },
      }
    })

    // Apply dagre layout
    const nodes = layoutNodes(rawNodes, rawEdges)

    return { nodes, edges: rawEdges }
  }, [workflow])

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
        nodesConnectable={false}
        elementsSelectable={false}
        panOnScroll
        zoomOnScroll={false}
        colorMode="dark"
      >
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} color="#1e293b" />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  )
}
