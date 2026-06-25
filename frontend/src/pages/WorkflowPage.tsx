import { useEffect, useMemo, useCallback, useState, useRef } from 'react'
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
import type { Node, Edge, NodeProps, Connection, EdgeMouseHandler } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { api } from '../api/client'
import type { Workflow, WorkflowLabel } from '../api/client'

// ─── Constants ───────────────────────────────────────────────────────────────

const TRIGGER_STYLE: Record<string, { strokeDasharray?: string; stroke: string }> = {
  agent: { stroke: '#6366F1' },
  human: { stroke: '#EC4899', strokeDasharray: '5 3' },
  both:  { stroke: '#F59E0B' },
}

const TRIGGER_CYCLE: Record<string, 'agent' | 'human' | 'both'> = {
  agent: 'human',
  human: 'both',
  both: 'agent',
}

// path cycles only for agent/both transitions
const PATH_CYCLE: Record<string, 'success' | 'failure' | 'either' | null> = {
  success: 'failure',
  failure: 'either',
  either: null,
}

const PATH_LABEL: Record<string, string> = {
  success: '✓',
  failure: '✗',
  either: '~',
}

const COL_W = 200
const ROW_H = 120
const COLS = 4

// ─── Types ───────────────────────────────────────────────────────────────────

type LabelNodeData = WorkflowLabel & {
  onSelect?: (name: string) => void
  isSelected?: boolean
}

// ─── Custom Node ─────────────────────────────────────────────────────────────

function LabelNode({ data, selected }: NodeProps) {
  const label = data as unknown as LabelNodeData
  const isIgnored = label.agent_ignore === 1
  const isTerminal = label.is_terminal === 1

  return (
    <div
      onClick={() => label.onSelect?.(label.name)}
      className={`rounded-lg px-4 py-2.5 min-w-28 text-center border-2 shadow-md cursor-pointer transition-all ${
        isIgnored ? 'opacity-60' : ''
      } ${selected ? 'ring-2 ring-white ring-offset-1 ring-offset-slate-950' : ''}`}
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

// ─── Helpers ─────────────────────────────────────────────────────────────────

function labelsToNodes(
  labels: WorkflowLabel[],
  onSelect: (name: string) => void,
  selectedName: string | null,
): Node[] {
  const sorted = [...labels].sort((a, b) => a.sort_order - b.sort_order)
  return sorted.map((lbl, i) => ({
    id: lbl.name,
    type: 'label',
    position: { x: (i % COLS) * COL_W, y: Math.floor(i / COLS) * ROW_H },
    data: { ...lbl, onSelect, isSelected: lbl.name === selectedName } as unknown as Record<string, unknown>,
    selected: lbl.name === selectedName,
  }))
}

function edgeLabel(trigger: string, path?: string | null): string {
  const pathSuffix = path ? ` ${PATH_LABEL[path] ?? path}` : ''
  return `${trigger}${pathSuffix}`
}

function transitionsToEdges(transitions: Array<{ from_label: string; to_label: string; trigger_type: string; agent_config_id?: string; path?: string | null }>, idPrefix = ''): Edge[] {
  return transitions.map((t, i) => {
    const tt = t.trigger_type as 'agent' | 'human' | 'both'
    return {
      id: idPrefix ? `${idPrefix}-${i}` : `${t.from_label}->${t.to_label}-${i}`,
      source: t.from_label,
      target: t.to_label,
      label: edgeLabel(tt, t.path),
      data: { trigger: tt, path: t.path ?? null },
      markerEnd: { type: MarkerType.ArrowClosed },
      style: TRIGGER_STYLE[tt] ?? { stroke: '#6B7280' },
      labelStyle: { fill: '#94A3B8', fontSize: 10 },
      labelBgStyle: { fill: '#0F172A' },
    }
  })
}

function workflowToFlow(wf: Workflow, onSelect: (name: string) => void, selectedName: string | null): { nodes: Node[]; edges: Edge[] } {
  const nodes = labelsToNodes(wf.labels, onSelect, selectedName)
  const edges = transitionsToEdges(wf.transitions, wf.id)
  return { nodes, edges }
}

// ─── Side Panel ──────────────────────────────────────────────────────────────

interface LabelPanelProps {
  label: WorkflowLabel
  allLabelNames: string[]
  onUpdate: (updated: WorkflowLabel) => void
  onDelete: () => void
  onClose: () => void
}

function LabelPanel({ label, allLabelNames, onUpdate, onDelete, onClose }: LabelPanelProps) {
  const [form, setForm] = useState<WorkflowLabel>({ ...label })
  const [nameError, setNameError] = useState<string | null>(null)
  const nameInputRef = useRef<HTMLInputElement>(null)

  // Sync when a different label is selected
  useEffect(() => {
    setForm({ ...label })
    setNameError(null)
  }, [label.id, label.name])

  const handleNameChange = (v: string) => {
    setForm((f) => ({ ...f, name: v }))
    if (v.trim() === '') {
      setNameError('Name is required')
    } else if (v !== label.name && allLabelNames.includes(v.trim())) {
      setNameError('Name must be unique')
    } else {
      setNameError(null)
    }
  }

  const handleApply = () => {
    if (nameError || !form.name.trim()) return
    onUpdate({ ...form, name: form.name.trim() })
  }

  const isDirty =
    form.name !== label.name ||
    form.color !== label.color ||
    form.agent_ignore !== label.agent_ignore ||
    form.is_terminal !== label.is_terminal ||
    form.sort_order !== label.sort_order

  return (
    <div className="w-72 flex-shrink-0 bg-slate-900 border-l border-slate-800 flex flex-col">
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-slate-800">
        <span className="text-sm font-semibold text-slate-200">Edit Label</span>
        <button
          onClick={onClose}
          className="text-slate-500 hover:text-slate-300 transition-colors text-lg leading-none"
          aria-label="Close panel"
        >
          ×
        </button>
      </div>

      {/* Fields */}
      <div className="flex-1 overflow-y-auto px-4 py-4 space-y-5">

        {/* Name */}
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Name</label>
          <input
            ref={nameInputRef}
            type="text"
            value={form.name}
            onChange={(e) => handleNameChange(e.target.value)}
            className={`w-full bg-slate-800 border rounded px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-1 ${
              nameError
                ? 'border-red-500 focus:ring-red-500'
                : 'border-slate-700 focus:ring-indigo-500'
            }`}
          />
          {nameError && (
            <p className="text-xs text-red-400 mt-1">{nameError}</p>
          )}
        </div>

        {/* Color */}
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Color</label>
          <div className="flex items-center gap-2">
            <input
              type="color"
              value={form.color}
              onChange={(e) => setForm((f) => ({ ...f, color: e.target.value }))}
              className="w-9 h-9 rounded cursor-pointer bg-transparent border-0 p-0.5"
              aria-label="Color picker"
            />
            <input
              type="text"
              value={form.color}
              onChange={(e) => {
                const v = e.target.value
                if (/^#[0-9A-Fa-f]{0,6}$/.test(v)) setForm((f) => ({ ...f, color: v }))
              }}
              className="flex-1 bg-slate-800 border border-slate-700 rounded px-3 py-1.5 text-sm text-slate-100 font-mono focus:outline-none focus:ring-1 focus:ring-indigo-500"
              maxLength={7}
            />
          </div>
        </div>

        {/* Sort Order */}
        <div>
          <label className="block text-xs font-medium text-slate-400 mb-1.5">Sort Order</label>
          <input
            type="number"
            value={form.sort_order}
            onChange={(e) => setForm((f) => ({ ...f, sort_order: parseInt(e.target.value, 10) || 0 }))}
            className="w-full bg-slate-800 border border-slate-700 rounded px-3 py-1.5 text-sm text-slate-100 focus:outline-none focus:ring-1 focus:ring-indigo-500"
          />
          <p className="text-xs text-slate-500 mt-1">Controls position in the grid layout</p>
        </div>

        {/* Toggles */}
        <div className="space-y-3">
          <Toggle
            id="agent_ignore"
            label="Agent Ignore"
            description="Agents will skip tasks in this label"
            checked={form.agent_ignore === 1}
            onChange={(v) => setForm((f) => ({ ...f, agent_ignore: v ? 1 : 0 }))}
            badge="∅"
            badgeClass="text-slate-400"
          />
          <Toggle
            id="is_terminal"
            label="Terminal"
            description="No further transitions expected from this label"
            checked={form.is_terminal === 1}
            onChange={(v) => setForm((f) => ({ ...f, is_terminal: v ? 1 : 0 }))}
            badge="✓"
            badgeClass="text-emerald-400"
          />
        </div>

        {/* Color preview */}
        <div
          className="rounded-lg px-4 py-2.5 text-center border-2 shadow-md select-none"
          style={{ borderColor: form.color, backgroundColor: `${form.color}22`, opacity: form.agent_ignore ? 0.6 : 1 }}
        >
          <div className="text-xs font-semibold text-white">{form.name || 'preview'}</div>
          <div className="flex justify-center gap-1 mt-1">
            {form.agent_ignore === 1 && <span className="text-slate-400 text-xs">∅</span>}
            {form.is_terminal === 1 && <span className="text-emerald-400 text-xs">✓</span>}
          </div>
        </div>
      </div>

      {/* Actions */}
      <div className="px-4 py-3 border-t border-slate-800 space-y-2">
        <button
          onClick={handleApply}
          disabled={!!nameError || !isDirty}
          className="w-full px-4 py-1.5 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
        >
          Apply Changes
        </button>
        <button
          onClick={onDelete}
          className="w-full px-4 py-1.5 text-sm font-medium rounded bg-slate-800 hover:bg-red-900 hover:text-red-300 text-slate-400 transition-colors border border-slate-700 hover:border-red-800"
        >
          Delete Label
        </button>
      </div>
    </div>
  )
}

interface ToggleProps {
  id: string
  label: string
  description: string
  checked: boolean
  onChange: (v: boolean) => void
  badge: string
  badgeClass: string
}

function Toggle({ id, label, description, checked, onChange, badge, badgeClass }: ToggleProps) {
  return (
    <label htmlFor={id} className="flex items-start gap-3 cursor-pointer group">
      <div className="relative mt-0.5 flex-shrink-0">
        <input
          type="checkbox"
          id={id}
          checked={checked}
          onChange={(e) => onChange(e.target.checked)}
          className="sr-only"
        />
        <div
          className={`w-9 h-5 rounded-full transition-colors ${
            checked ? 'bg-indigo-600' : 'bg-slate-700'
          }`}
        />
        <div
          className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white shadow transition-transform ${
            checked ? 'translate-x-4' : 'translate-x-0'
          }`}
        />
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-1.5">
          <span className="text-sm text-slate-200 font-medium">{label}</span>
          <span className={`text-xs ${badgeClass}`}>{badge}</span>
        </div>
        <p className="text-xs text-slate-500 mt-0.5">{description}</p>
      </div>
    </label>
  )
}

// ─── Inline Editable Text ─────────────────────────────────────────────────────

interface InlineEditProps {
  value: string
  onChange: (v: string) => void
  className?: string
  placeholder?: string
  as?: 'h1' | 'p'
}

function InlineEdit({ value, onChange, className = '', placeholder = 'Click to edit', as: Tag = 'p' }: InlineEditProps) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    setDraft(value)
  }, [value])

  const commit = () => {
    setEditing(false)
    if (draft.trim() !== value) onChange(draft.trim())
  }

  if (editing) {
    return (
      <input
        ref={inputRef}
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === 'Enter') commit()
          if (e.key === 'Escape') { setDraft(value); setEditing(false) }
        }}
        className={`bg-transparent border-b border-indigo-500 focus:outline-none ${className}`}
      />
    )
  }

  return (
    <Tag
      onClick={() => setEditing(true)}
      title="Click to edit"
      className={`cursor-text hover:text-indigo-300 transition-colors ${className} ${!value ? 'text-slate-600' : ''}`}
    >
      {value || placeholder}
    </Tag>
  )
}

// ─── Main Page ────────────────────────────────────────────────────────────────

export default function WorkflowPage() {
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [wfName, setWfName] = useState('')
  const [wfDesc, setWfDesc] = useState('')
  const [labels, setLabels] = useState<WorkflowLabel[]>([])
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [selectedLabel, setSelectedLabel] = useState<string | null>(null)

  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([])
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([])

  // Keep a ref to onSelect so nodes don't need to re-render when it changes
  const onSelectRef = useRef<(name: string) => void>(() => {})

  const onSelect = useCallback((name: string) => {
    setSelectedLabel((prev) => (prev === name ? null : name))
  }, [])
  onSelectRef.current = onSelect

  // Stable callback for nodes so we don't trigger re-flow on every render
  const stableOnSelect = useCallback((name: string) => onSelectRef.current(name), [])

  // Rebuild nodes whenever labels or selection changes
  const rebuildNodes = useCallback(
    (lbls: WorkflowLabel[], sel: string | null) => {
      setNodes((prev) => {
        const posMap = new Map(prev.map((n) => [n.id, n.position]))
        const sorted = [...lbls].sort((a, b) => a.sort_order - b.sort_order)
        return sorted.map((lbl, i) => ({
          id: lbl.name,
          type: 'label',
          position: posMap.get(lbl.name) ?? { x: (i % COLS) * COL_W, y: Math.floor(i / COLS) * ROW_H },
          data: { ...lbl, onSelect: stableOnSelect, isSelected: lbl.name === sel } as unknown as Record<string, unknown>,
          selected: lbl.name === sel,
        }))
      })
    },
    [setNodes, stableOnSelect],
  )

  // Load workflow on mount
  useEffect(() => {
    api.workflows.list().then((wfs) => {
      if (wfs && wfs.length > 0) {
        const wf = wfs[0]
        setWorkflow(wf)
        setWfName(wf.name)
        setWfDesc(wf.description ?? '')
        setLabels(wf.labels)
        const { nodes: n, edges: e } = workflowToFlow(wf, stableOnSelect, null)
        setNodes(n)
        setEdges(e)
      }
    })
  }, [setNodes, setEdges, stableOnSelect])

  // Sync nodes when labels or selection changes (after initial load)
  const isFirstRender = useRef(true)
  useEffect(() => {
    if (isFirstRender.current) { isFirstRender.current = false; return }
    rebuildNodes(labels, selectedLabel)
  }, [labels, selectedLabel, rebuildNodes])

  // ── Connections ──
  const onConnect = useCallback(
    (connection: Connection) => {
      const newEdge: Edge = {
        ...connection,
        id: `${connection.source}->${connection.target}-${Date.now()}`,
        label: edgeLabel('agent', null),
        data: { trigger: 'agent', path: null },
        markerEnd: { type: MarkerType.ArrowClosed },
        style: TRIGGER_STYLE.agent,
        labelStyle: { fill: '#94A3B8', fontSize: 10 },
        labelBgStyle: { fill: '#0F172A' },
      } as Edge
      setEdges((eds) => addEdge(newEdge, eds))
    },
    [setEdges],
  )

  // ── Edge click → cycle trigger_type; shift+click → cycle path (agent/both only) ──
  const onEdgeClick: EdgeMouseHandler = useCallback(
    (evt, edge) => {
      const d = (edge.data ?? {}) as { trigger?: string; path?: string | null }
      const trigger = d.trigger ?? 'agent'
      const path = d.path ?? null

      if (evt.shiftKey && trigger !== 'human') {
        // Cycle path: null → success → failure → either → null
        const nextPath = path === null ? 'success' : (PATH_CYCLE[path] ?? null)
        setEdges((eds) =>
          eds.map((e) =>
            e.id === edge.id
              ? { ...e, label: edgeLabel(trigger, nextPath), data: { trigger, path: nextPath } }
              : e,
          ),
        )
      } else {
        // Cycle trigger
        const nextTrigger = TRIGGER_CYCLE[trigger] ?? 'agent'
        // Reset path if switching to human (no path on human transitions)
        const nextPath = nextTrigger === 'human' ? null : path
        setEdges((eds) =>
          eds.map((e) =>
            e.id === edge.id
              ? { ...e, label: edgeLabel(nextTrigger, nextPath), style: TRIGGER_STYLE[nextTrigger], data: { trigger: nextTrigger, path: nextPath } }
              : e,
          ),
        )
      }
    },
    [setEdges],
  )

  // ── Label CRUD ──
  const handleLabelUpdate = useCallback(
    (updated: WorkflowLabel) => {
      const oldName = selectedLabel!
      const newName = updated.name

      setLabels((prev) => {
        const next = prev.map((l) => (l.name === oldName ? { ...updated } : l))
        return next
      })

      // If name changed, update edges that reference this label
      if (newName !== oldName) {
        setEdges((prev) =>
          prev.map((e) => ({
            ...e,
            source: e.source === oldName ? newName : e.source,
            target: e.target === oldName ? newName : e.target,
          })),
        )
        setSelectedLabel(newName)
      }
    },
    [selectedLabel, setEdges],
  )

  const handleLabelDelete = useCallback(() => {
    if (!selectedLabel) return
    const name = selectedLabel
    if (!window.confirm(`Delete label "${name}"? Any transitions referencing it will also be removed.`)) return
    setLabels((prev) => prev.filter((l) => l.name !== name))
    setEdges((prev) => prev.filter((e) => e.source !== name && e.target !== name))
    setSelectedLabel(null)
  }, [selectedLabel, setEdges])

  const handleAddLabel = useCallback(() => {
    const maxOrder = labels.reduce((m, l) => Math.max(m, l.sort_order), -1)
    const newLabel: WorkflowLabel = {
      id: `new-${Date.now()}`,
      workflow_id: workflow?.id ?? '',
      name: `new_label_${maxOrder + 1}`,
      color: '#6B7280',
      sort_order: maxOrder + 1,
      agent_ignore: 0,
      is_terminal: 0,
    }
    setLabels((prev) => [...prev, newLabel])
    setSelectedLabel(newLabel.name)
  }, [labels, workflow])

  // ── Save ──
  const handleSave = async () => {
    if (!workflow) return
    setSaving(true)
    setSaveError(null)
    try {
      const updatedLabels = labels.map((lbl) => ({
        name: lbl.name,
        color: lbl.color,
        sort_order: lbl.sort_order,
        agent_ignore: lbl.agent_ignore === 1,
        is_terminal: lbl.is_terminal === 1,
        is_rejection_target: false,
      }))
      const updatedTransitions = edges.map((e) => {
        const d = (e.data ?? {}) as { trigger?: string; path?: string | null }
        return {
          from_label: e.source,
          to_label: e.target,
          trigger_type: d.trigger ?? 'agent',
          path: d.path ?? null,
          agent_config_id: undefined as string | undefined,
        }
      })
      const saved = await api.workflows.update(workflow.id, {
        name: wfName,
        description: wfDesc,
        labels: updatedLabels,
        transitions: updatedTransitions,
      })
      setWorkflow(saved)
    } catch (err: unknown) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  // ── Derived ──
  const selectedLabelObj = useMemo(
    () => labels.find((l) => l.name === selectedLabel) ?? null,
    [labels, selectedLabel],
  )

  const allLabelNames = useMemo(() => labels.map((l) => l.name), [labels])

  const legend = useMemo(
    () => [
      { color: '#6366F1', label: 'agent trigger', dashed: false },
      { color: '#EC4899', label: 'human gate', dashed: true },
      { color: '#F59E0B', label: 'both', dashed: false },
    ],
    [],
  )

  return (
    <div className="flex flex-col h-full">
      {/* ── Top bar ── */}
      <div className="flex items-center justify-between px-6 py-4 border-b border-slate-800">
        <div className="flex-1 min-w-0 mr-6">
          <InlineEdit
            value={wfName}
            onChange={setWfName}
            as="h1"
            placeholder="Workflow name"
            className="text-xl font-semibold text-slate-100 w-full"
          />
          <InlineEdit
            value={wfDesc}
            onChange={setWfDesc}
            placeholder="Add a description…"
            className="text-xs text-slate-500 mt-0.5 w-full"
          />
        </div>

        <div className="flex items-center gap-4 flex-shrink-0">
          {/* Legend */}
          <div className="flex gap-4 mr-2">
            {legend.map((l) => (
              <div key={l.label} className="flex items-center gap-1.5 text-xs text-slate-400">
                <div
                  className="w-6"
                  style={{
                    borderTop: `2px ${l.dashed ? 'dashed' : 'solid'} ${l.color}`,
                  }}
                />
                {l.label}
              </div>
            ))}
          </div>

          {/* Add Label */}
          <button
            onClick={handleAddLabel}
            disabled={!workflow}
            className="px-3 py-1.5 text-sm font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-200 disabled:opacity-50 transition-colors"
          >
            + Add Label
          </button>

          {/* Export YAML */}
          {workflow && (
            <a
              href={`/api/v1/workflows/${workflow.id}/export.yaml`}
              download="workflow.yaml"
              className="px-3 py-1.5 text-sm font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-200 transition-colors"
            >
              Export YAML
            </a>
          )}

          {/* Save */}
          <button
            onClick={handleSave}
            disabled={saving || !workflow}
            className="px-4 py-1.5 text-sm font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50 transition-colors"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
        </div>
      </div>

      {/* ── Save error ── */}
      {saveError && (
        <div className="px-6 py-2 bg-red-900/30 border-b border-red-800 text-red-300 text-sm flex items-center justify-between">
          <span>Save failed: {saveError}</span>
          <button onClick={() => setSaveError(null)} className="text-red-400 hover:text-red-200 ml-4">×</button>
        </div>
      )}

      {/* ── Edge hint banner ── */}
      <div className="px-6 py-1.5 border-b border-slate-800 bg-slate-950 text-xs text-slate-500 flex items-center gap-4">
        <span>Click a node to edit its properties</span>
        <span className="text-slate-700">·</span>
        <span>Click an edge to cycle trigger type · Shift+click to cycle path (success ✓ / failure ✗ / either ~) · Select + Delete/Backspace to remove</span>
        <span className="text-slate-700">·</span>
        <span>Drag from a node handle to create a new transition</span>
      </div>

      {/* ── Canvas + side panel ── */}
      <div className="flex flex-1 min-h-0">
        <div className="flex-1 relative">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onEdgeClick={onEdgeClick}
            fitView
            proOptions={{ hideAttribution: true }}
            className="bg-slate-950"
          >
            <Background color="#334155" gap={20} />
            <Controls className="!bg-slate-800 !border-slate-700" />
          </ReactFlow>
        </div>

        {/* ── Label edit panel (slide in) ── */}
        {selectedLabelObj && (
          <LabelPanel
            key={selectedLabelObj.name}
            label={selectedLabelObj}
            allLabelNames={allLabelNames}
            onUpdate={handleLabelUpdate}
            onDelete={handleLabelDelete}
            onClose={() => setSelectedLabel(null)}
          />
        )}
      </div>
    </div>
  )
}
