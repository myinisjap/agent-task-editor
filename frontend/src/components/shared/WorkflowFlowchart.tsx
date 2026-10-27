import { labelDisplayName } from '../../lib/labelDisplay'
import { useEffect, useMemo } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  MarkerType,
  Position,
  Handle,
  Panel,
  useReactFlow,
  useStore,
  type NodeProps,
  type Node,
  type Edge,
  BackgroundVariant,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { Workflow } from '../../api/client'
import { useThemeStore } from '../../stores/theme'
import { useIsMobile } from '../../lib/useIsMobile'
import type { LabelStaffing } from '../../lib/staffing'
import {
  FONT,
  FIT_MAX_ZOOM,
  MAX_ZOOM,
  MIN_ZOOM,
  NODE_WIDTH,
  SIMPLE_NODE_HEIGHT,
  DETAILED_NODE_HEIGHT,
  agentSummary,
  edgeHandles,
  graphOverflows,
  initialViewport,
  isLoopBack,
  layoutWorkflow,
  loopOffset,
  nodeBadges,
  rankBySortOrder,
  type Bounds,
  type FlowDirection,
} from '../../lib/flowchartLayout'

// Handles must exist for edge routing but shouldn't render as visible dots.
const HANDLE_HIDDEN = { opacity: 0, width: 1, height: 1, border: 'none', background: 'transparent' } as const

// Edit mode: the main in/out handles are drag handles.
const HANDLE_VISIBLE = { width: 12, height: 12, background: 'var(--color-indigo-500)', border: '2px solid var(--color-slate-200)' } as const

function edgeColor(path: string | null | undefined): string {
  if (path === 'success') return '#22c55e' // green-500
  if (path === 'failure') return '#ef4444' // red-500
  return '#64748b' // slate-500 for either/null
}

// ── Custom Node ───────────────────────────────────────────────────────────────

interface LabelNodeData {
  label: string
  /** Raw label identifier (the node id); `label` is the visible display text. */
  name: string
  color: string
  isTerminal: boolean
  agentIgnore: boolean
  badges: string[]
  dir: FlowDirection
  /** Present only in the detailed (Pipeline overview) rendering. */
  detail?: {
    staffing: LabelStaffing
    selected: boolean
    onSelect?: (name: string) => void
    /** Edit mode: the main in/out handles become visible drag targets. */
    editable?: boolean
  }
}

const chip = 'rounded-full border px-2 leading-5 font-medium whitespace-nowrap'
const chipStyle = { fontSize: FONT.meta } as const

function Badge({ text }: { text: string }) {
  const tone = text === 'Opens PR' ? 'border-indigo-500 text-indigo-300' : 'border-slate-600 text-slate-300'
  return <span className={`${chip} ${tone}`} style={chipStyle}>{text}</span>
}

function StaffingRow({ name, s, detail }: { name: string; s: LabelStaffing; detail: NonNullable<LabelNodeData['detail']> }) {
  if (s.kind === 'terminal') return null
  if (s.kind === 'human') {
    return <span className={`${chip} self-start border-slate-600 bg-slate-800 text-slate-300`} style={chipStyle}>Human gate</span>
  }
  if (s.kind === 'unstaffed') {
    return (
      <div className="flex items-center gap-2">
        <span className={`${chip} border-amber-600 bg-amber-900/40 text-amber-300`} style={chipStyle}>No agent</span>
        {detail.onSelect && !detail.editable && (
          <button
            type="button"
            className="nodrag nopan rounded border border-indigo-500 px-2 leading-5 text-indigo-300 hover:bg-indigo-500/20"
            style={chipStyle}
            onClick={(e) => { e.stopPropagation(); detail.onSelect?.(name) }}
          >
            Assign
          </button>
        )}
      </div>
    )
  }
  const a = agentSummary(s)
  if (!a) return null
  return (
    <div className="min-w-0 leading-tight" title={a.title}>
      <div className="truncate text-slate-200" style={{ fontSize: FONT.body }}>
        {a.name}{a.extra > 0 ? ` +${a.extra}` : ''}
      </div>
      {a.sub && <div className="truncate text-slate-400" style={{ fontSize: FONT.meta }}>{a.sub}</div>}
    </div>
  )
}

function WorkflowLabelNode({ data }: NodeProps) {
  const d = data as unknown as LabelNodeData
  const color = d.color || '#6366f1'
  const detail = d.detail
  const lr = d.dir === 'LR'
  const inPos = lr ? Position.Left : Position.Top
  const outPos = lr ? Position.Right : Position.Bottom
  const laneA = lr ? { top: '30%' } : { left: '30%' }
  const laneB = lr ? { top: '70%' } : { left: '70%' }
  const loopA = lr ? Position.Top : Position.Left
  const loopB = lr ? Position.Bottom : Position.Right
  const loopSrc = lr ? { left: '65%' } : { top: '35%' }
  const loopTgt = lr ? { left: '35%' } : { top: '65%' }
  const main = detail?.editable ? HANDLE_VISIBLE : HANDLE_HIDDEN

  return (
    <div
      role={detail?.onSelect ? 'button' : undefined}
      tabIndex={detail?.onSelect ? 0 : undefined}
      aria-label={detail ? `${d.label} stage` : undefined}
      aria-pressed={detail?.onSelect ? detail.selected : undefined}
      data-testid={detail ? `pipeline-node-${d.name}` : undefined}
      onKeyDown={
        detail?.onSelect
          ? (e) => {
              if (e.target !== e.currentTarget) return
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault()
                detail.onSelect?.(d.name)
              }
            }
          : undefined
      }
      className={detail?.onSelect ? 'cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-400' : undefined}
      style={{
        width: NODE_WIDTH,
        height: detail ? DETAILED_NODE_HEIGHT : SIMPLE_NODE_HEIGHT,
        border: `2px solid ${color}`,
        borderRadius: 8,
        background: `${color}1a`,
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        gap: 6,
        padding: 10,
        overflow: 'hidden',
        position: 'relative',
        textAlign: 'left',
        boxShadow: detail?.selected
          ? `0 0 0 3px ${color}`
          : d.isTerminal ? `0 0 0 3px ${color}66` : undefined,
      }}
    >
      <Handle type="target" position={inPos} id="in" style={main} />
      <Handle type="target" position={inPos} id="in-a" style={{ ...HANDLE_HIDDEN, ...laneA }} />
      <Handle type="target" position={inPos} id="in-b" style={{ ...HANDLE_HIDDEN, ...laneB }} />
      <Handle type="source" position={outPos} id="out" style={main} />
      <Handle type="source" position={outPos} id="out-a" style={{ ...HANDLE_HIDDEN, ...laneA }} />
      <Handle type="source" position={outPos} id="out-b" style={{ ...HANDLE_HIDDEN, ...laneB }} />
      <Handle type="source" position={loopA} id="loop-a" style={{ ...HANDLE_HIDDEN, ...loopSrc }} />
      <Handle type="source" position={loopB} id="loop-b" style={{ ...HANDLE_HIDDEN, ...loopSrc }} />
      <Handle type="target" position={loopA} id="t-loop-a" style={{ ...HANDLE_HIDDEN, ...loopTgt }} />
      <Handle type="target" position={loopB} id="t-loop-b" style={{ ...HANDLE_HIDDEN, ...loopTgt }} />

      <div
        className="truncate font-semibold text-slate-100"
        title={d.label}
        style={{ fontSize: FONT.title, lineHeight: 1.25, fontStyle: d.agentIgnore ? 'italic' : 'normal' }}
      >
        {d.label}
      </div>
      {detail && <StaffingRow name={d.name} s={detail.staffing} detail={detail} />}
      {d.badges.length > 0 && (
        <div className="flex flex-wrap gap-1" style={{ maxHeight: 52, overflow: 'hidden' }}>
          {d.badges.map((b) => <Badge key={b} text={b} />)}
        </div>
      )}
    </div>
  )
}

const nodeTypes = { workflowLabel: WorkflowLabelNode }

/** Applies the initial viewport (clamped zoom, start-aligned when overflowing) and shows a pan hint. */
function ViewportController({ bounds, layoutKey }: { bounds: Bounds; layoutKey: string }) {
  const { setViewport } = useReactFlow()
  const width = useStore((s) => s.width)
  const height = useStore((s) => s.height)
  const zoom = useStore((s) => s.transform[2])

  useEffect(() => {
    if (!width || !height) return
    void setViewport(initialViewport(bounds, { width, height }, { padding: 24 }))
    // Keyed on the layout, not on selection, so clicking a node keeps the user's pan.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layoutKey, width, height, setViewport])

  if (!graphOverflows(bounds, { width, height }, zoom)) return null
  return (
    <Panel position="bottom-center">
      <div
        role="status"
        data-testid="flowchart-pan-hint"
        className="rounded-full border border-slate-700 bg-slate-900 px-3 py-1 text-slate-300"
        style={{ fontSize: FONT.meta }}
      >
        Drag or scroll to see the whole pipeline
      </div>
    </Panel>
  )
}

// ── Main Component ────────────────────────────────────────────────────────────

interface Props {
  workflow: Workflow | null
  /** When set, nodes render in detailed mode: who works each label, plus markers. */
  staffing?: Map<string, LabelStaffing>
  /** Detailed mode only: makes nodes clickable. */
  onSelectLabel?: (name: string) => void
  selectedLabel?: string | null
  /** Detailed mode only: nodes can be connected by dragging from the right handle to another node's left handle (bottom → top on phones). */
  onConnectLabels?: (from: string, to: string) => void
}

export default function WorkflowFlowchart({ workflow, staffing, onSelectLabel, selectedLabel, onConnectLabels }: Props) {
  const editable = Boolean(onConnectLabels && staffing)
  const theme = useThemeStore((s) => s.theme)
  const isDark = theme === 'dark'
  const isMobile = useIsMobile()
  const dir: FlowDirection = isMobile ? 'TB' : 'LR'
  const { nodes: layoutedNodes, edges, bounds, layoutKey } = useMemo(() => {
    if (!workflow?.labels?.length) return { nodes: [], edges: [], bounds: null, layoutKey: '' }

    // Rank = board column order, so loop-back edges can fan out by span.
    const sorted = [...workflow.labels].sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0))
    const rankOf = rankBySortOrder(workflow.labels)

    const rawNodes: Node[] = sorted.map((lbl) => ({
      id: lbl.name!,
      type: 'workflowLabel',
      position: { x: 0, y: 0 }, // overridden by layout
      data: {
        label: labelDisplayName(lbl),
        name: lbl.name,
        color: lbl.color || '#6366f1',
        isTerminal: Boolean(lbl.is_terminal),
        agentIgnore: Boolean(lbl.agent_ignore),
        badges: nodeBadges(lbl),
        dir,
        detail: staffing
          ? {
              staffing: staffing.get(lbl.name!) ?? { kind: 'human', agents: [] },
              selected: selectedLabel === lbl.name,
              onSelect: onSelectLabel,
              editable,
            }
          : undefined,
      },
    }))

    const rawEdges: Edge[] = (workflow.transitions ?? []).map((t, i) => {
      const color = edgeColor(t.path)
      const isAgent = t.trigger_type === 'agent'
      const isBoth = t.trigger_type === 'both'
      const loopBack = isLoopBack(t, rankOf)
      const h = edgeHandles(t, loopBack)

      let pathOptions: { offset: number } | undefined
      if (loopBack) {
        const span = Math.abs((rankOf.get(t.to_label!) ?? 0) - (rankOf.get(t.from_label!) ?? 0))
        pathOptions = { offset: loopOffset(span) }
      }

      return {
        id: `e-${t.from_label}-${t.to_label}-${i}`,
        source: t.from_label!,
        target: t.to_label!,
        sourceHandle: h.source,
        targetHandle: h.target,
        type: 'smoothstep',
        pathOptions,
        animated: false,
        style: {
          stroke: color,
          strokeWidth: 2,
          strokeDasharray: isAgent ? '6 3' : isBoth ? '2 2' : undefined,
        },
        markerEnd: { type: MarkerType.ArrowClosed, color, width: 16, height: 16 },
        label: t.path ? t.path : undefined,
        labelStyle: { fontSize: FONT.meta, fill: color, fontWeight: 600 },
        labelBgStyle: { fill: 'var(--color-slate-950)', fillOpacity: 0.85 },
      }
    })

    const laid = layoutWorkflow(rawNodes, workflow.transitions ?? [], rankOf, dir, staffing ? DETAILED_NODE_HEIGHT : SIMPLE_NODE_HEIGHT)
    const key = `${workflow.id ?? ''}|${dir}|${sorted.map((l) => l.name).join(',')}|${Math.round(laid.bounds.width)}x${Math.round(laid.bounds.height)}|${staffing ? 'd' : 's'}`
    return { nodes: laid.nodes as Node[], edges: rawEdges, bounds: laid.bounds, layoutKey: key }
  }, [workflow, staffing, onSelectLabel, selectedLabel, editable, dir])

  if (!workflow) {
    return (
      <div className="flex items-center justify-center h-full text-slate-500 text-sm">
        Loading chart…
      </div>
    )
  }

  if (!workflow.labels?.length || !bounds) {
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
        minZoom={MIN_ZOOM}
        maxZoom={MAX_ZOOM}
        style={{ background: 'transparent' }}
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
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} color="var(--color-slate-800)" />
        <Controls showInteractive={false} fitViewOptions={{ minZoom: MIN_ZOOM, maxZoom: FIT_MAX_ZOOM, padding: 0.1 }} />
        <ViewportController bounds={bounds} layoutKey={layoutKey} />
      </ReactFlow>
    </div>
  )
}
