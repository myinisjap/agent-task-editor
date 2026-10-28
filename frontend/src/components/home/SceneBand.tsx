import { useState } from 'react'
import type { Workflow } from '../../api/client'
import TaskFactory from '../TaskFactory'
import FactoryLine from '../FactoryLine'
import { Button, cx } from '../ui'
import { useMediaQuery } from '../../lib/useMediaQuery'
import { homeStations } from '../../lib/homeInbox'
import PipelineStrip from './PipelineStrip'

const ROBOTS_KEY = 'dashboard.visualize.robots' // legacy; migrated into MODE_KEY
const MODE_KEY = 'dashboard.visualize.mode'

type VizMode = 'office' | 'robots' | 'factory'

// Read the persisted visualization mode, migrating the old boolean "Robots"
// toggle: an existing robots='1' becomes the 'robots' mode on first read.
function initialMode(): VizMode {
  try {
    const m = localStorage.getItem(MODE_KEY)
    if (m === 'office' || m === 'robots' || m === 'factory') return m
    if (localStorage.getItem(ROBOTS_KEY) === '1') return 'robots'
  } catch { /* ignore */ }
  return 'office'
}

const MODES: { value: VizMode; label: string }[] = [
  { value: 'office', label: 'Office' },
  { value: 'robots', label: 'Robots' },
  { value: 'factory', label: 'Factory' },
]

/** Short or narrow viewports start collapsed. */
export const COMPACT_QUERY = '(max-height: 819px), (max-width: 767px)'

function expandedKey(compact: boolean) {
  return compact ? 'home.scene.expanded.compact' : 'home.scene.expanded.wide'
}

function readExpanded(compact: boolean): boolean {
  try {
    const v = localStorage.getItem(expandedKey(compact))
    if (v === '1') return true
    if (v === '0') return false
  } catch { /* ignore */ }
  return !compact
}

type Props = {
  workflow: Workflow
  labelCounts: Record<string, number>
}

export default function SceneBand({ workflow, labelCounts }: Props) {
  const compact = useMediaQuery(COMPACT_QUERY)
  // Remember an explicit choice per viewport class; otherwise follow the default.
  const [override, setOverride] = useState<{ compact: boolean; value: boolean } | null>(null)
  const stored = readExpanded(compact)
  const expanded = override && override.compact === compact ? override.value : stored
  const [mode, setMode] = useState<VizMode>(initialMode)

  const toggle = () => {
    const next = !expanded
    setOverride({ compact, value: next })
    try { localStorage.setItem(expandedKey(compact), next ? '1' : '0') } catch { /* ignore */ }
  }
  const selectMode = (next: VizMode) => {
    setMode(next)
    try { localStorage.setItem(MODE_KEY, next) } catch { /* ignore */ }
  }
  const stations = homeStations(workflow, labelCounts)

  return (
    <section aria-label="Scene" className="mb-4 min-w-0">
      <div className="flex items-center justify-between gap-2 mb-2 min-w-0">
        {expanded ? (
          <div className="inline-flex p-0.5 rounded-full border border-border bg-surface-raised" role="group" aria-label="Visualization style">
            {MODES.map((m) => (
              <button
                key={m.value}
                type="button"
                onClick={() => selectMode(m.value)}
                aria-pressed={mode === m.value}
                className={cx(
                  'px-2.5 py-1 text-xs rounded-full transition-colors',
                  mode === m.value ? 'bg-surface-sunken text-fg shadow-sm' : 'text-fg-subtle hover:text-fg-muted',
                )}
                title={
                  m.value === 'factory'
                    ? 'Tasks as items on a factory assembly line'
                    : m.value === 'robots'
                      ? 'Office floor, crew rendered as robots'
                      : 'Office floor with a human crew'
                }
              >
                {m.label}
              </button>
            ))}
          </div>
        ) : <span />}
        <Button variant="ghost" size="sm" onClick={toggle} aria-expanded={expanded} data-testid="scene-toggle">
          {expanded ? 'Hide scene' : 'Show scene'}
        </Button>
      </div>
      {expanded ? (
        <div className="min-w-0 overflow-hidden">
          {mode === 'factory' ? (
            <FactoryLine workflow={workflow} labelCounts={labelCounts} />
          ) : (
            <TaskFactory workflow={workflow} labelCounts={labelCounts} robots={mode === 'robots'} />
          )}
          <ul data-testid="station-legend" className="flex flex-wrap gap-2 mt-3 min-w-0">
            {stations.map((s) => (
              <li
                key={s.key}
                data-gate={s.isGate && s.count > 0 ? 'true' : undefined}
                className={cx(
                  'flex items-center gap-2 min-w-0 rounded-lg border px-3 py-1.5',
                  s.isGate && s.count > 0 ? 'border-gate bg-gate-soft text-gate' : 'border-border bg-surface-raised text-fg',
                )}
              >
                <span aria-hidden className="inline-block w-2.5 h-2.5 rounded-full shrink-0" style={{ backgroundColor: s.color }} />
                <span className="text-sm truncate">{s.name}</span>
                <span className="text-2xl font-semibold font-mono">{s.count}</span>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <PipelineStrip workflow={workflow} labelCounts={labelCounts} />
      )}
    </section>
  )
}
