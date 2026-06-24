import { useEffect, useMemo, useCallback, useState } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  Handle,
  Position,
  MarkerType,
  useNodesState,
  useEdgesState,
  addEdge,
} from '@xyflow/react'
import type { Node, Edge, NodeProps, Connection } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { api } from '../api/client'
import type { Workflow, WorkflowLabel } from '../api/client'

// Custom node rendering a workflow label
function LabelNode({ data }: NodeProps) {
  const label = data as unknown as WorkflowLabel & { selected?: boolean }
  const isIgnored = label.agent_ignore === 1
  const isTerminal = label.is_terminal === 1

  return (
    <div
      className={`rounded-lg px-4 py-2.5 min-w-28 text-center border-2 shadow-md ${
        isIgnored ? 'opacity-60' : ''
      }`}
      style={{ borderColor: label.color, backgroundColor: `${label.color}22` }}
    >
      <Handle type="target" position={Position.Left} className="!bg-slate-600" />
      <div className="text-xs font-semibold text-white">{label.name}</div>
      <div className="flex justify-center gap-1 mt-1">
        {isIgnored && (
          <span className="text-slate-400 text-xs" title="agent ignored">∅</span>
        )}
        {isTerminal && (
          <span className="text-emerald-400 text-xs" title="terminal">✓</span>
        )}
      </div>
      <Handle type="source" position={Position.Right} className="!bg-slate-600" />
    </div>
  )
}

const nodeTypes = { label: LabelNode }

function workflowToFlow(wf: Workflow): { nodes: Node[]; edges: Edge[] } {
  const sorted = [...wf.labels].sort((a, b) => a.sort_order - b.sort_order)

  const COL_W = 200
  const ROW_H = 120
  const COLS = 4

  const nodes: Node[] = sorted.map((lbl, i) => ({
    id: lbl.name,
    type: 'label',
    position: { x: (i % COLS) * COL_W, y: Math.floor(i / COLS) * ROW_H },
    data: lbl as unknown as Record<string, unknown>,
  }))

  const TRIGGER_STYLE: Record<string, { strokeDasharray?: string; stroke: string }> = {
    agent: { stroke: '#6366F1' },
    human: { stroke: '#EC4899', strokeDasharray: '5 3' },
    both:  { stroke: '#F59E0B' },
  }

  const edges: Edge[] = wf.transitions.map((t) => ({
    id: t.id,
    source: t.from_label,
    target: t.to_label,
    label: t.trigger_type,
    markerEnd: { type: MarkerType.ArrowClosed },
    style: TRIGGER_STYLE[t.trigger_type] ?? { stroke: '#6B7280' },
    labelStyle: { fill: '#94A3B8', fontSize: 10 },
    labelBgStyle: { fill: '#0F172A' },
  }))

  return { nodes, edges }
}

export default function WorkflowPage() {
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [saving, setSaving] = useState(false)
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])

  useEffect(() => {
    api.workflows.list().then((wfs) => {
      if (wfs && wfs.length > 0) {
        setWorkflow(wfs[0])
        const { nodes: n, edges: e } = workflowToFlow(wfs[0])
        setNodes(n)
        setEdges(e)
      }
    })
  }, [setNodes, setEdges])

  const onConnect = useCallback(
    (connection: Connection) => setEdges((eds) => addEdge(connection, eds)),
    [setEdges],
  )

  const handleSave = async () => {
    if (!workflow) return
    setSaving(true)
    try {
      // Build updated labels and transitions from current nodes/edges
      const updatedLabels = workflow.labels.map((lbl) => ({
        ...lbl,
      }))
      const updatedTransitions = edges.map((e) => ({
        from_label: e.source,
        to_label: e.target,
        trigger_type: (e.label as string) ?? 'both',
        agent_config_id: undefined as string | undefined,
      }))
      await api.workflows.update(workflow.id, {
        name: workflow.name,
        description: workflow.description,
        labels: updatedLabels,
        transitions: updatedTransitions,
      })
    } catch (err: any) {
      alert(err.message)
    } finally {
      setSaving(false)
    }
  }

  const legend = useMemo(() => [
    { color: '#6366F1', label: 'agent trigger' },
    { color: '#EC4899', label: 'human gate', dashed: true },
    { color: '#F59E0B', label: 'both' },
  ], [])

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-6 py-4 border-b border-slate-800">
        <div>
          <h1 className="text-xl font-semibold text-slate-100">
            {workflow?.name ?? 'Workflow Editor'}
          </h1>
          {workflow?.description && (
            <p className="text-xs text-slate-500 mt-0.5">{workflow.description}</p>
          )}
        </div>
        <div className="flex items-center gap-4">
          <div className="flex gap-4">
            {legend.map((l) => (
              <div key={l.label} className="flex items-center gap-1.5 text-xs text-slate-400">
                <div
                  className="w-6 h-0.5"
                  style={{
                    backgroundColor: l.color,
                    borderTop: l.dashed ? `2px dashed ${l.color}` : undefined,
                    height: l.dashed ? 0 : undefined,
                  }}
                />
                {l.label}
              </div>
            ))}
          </div>
          {workflow && (
            <a
              href={`/api/v1/workflows/${workflow.id}/export.yaml`}
              download="workflow.yaml"
              className="px-3 py-1.5 text-sm font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-200"
            >
              Export YAML
            </a>
          )}
          <button
            onClick={handleSave}
            disabled={saving || !workflow}
            className="px-4 py-1.5 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>

      <div className="flex-1">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          fitView
          proOptions={{ hideAttribution: true }}
          className="bg-slate-950"
        >
          <Background color="#334155" gap={20} />
          <Controls className="!bg-slate-800 !border-slate-700" />
        </ReactFlow>
      </div>
    </div>
  )
}
