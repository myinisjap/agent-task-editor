// Fun, non-essential dashboard visualization: an animated top-down "office
// floor" where each workflow label (or 3 buckets for custom workflows) is a
// station with its own workstation (desk / anvil / lab bench / charging rack
// / podium), and each task at that label is a little pixel-art worker that
// queues up, walks to the workstation, and performs that station's action
// facing it, then steps aside for the next. Rendered on a Canvas 2D game loop
// with integer-zoom pixel blitting — zero images, zero deps. Stations wrap
// into a responsive grid sized to the container, so it stacks on mobile
// instead of h-scrolling. Sim in officeScene.ts; sprite art in
// pixelSprites.ts; this file measures the container and hosts the canvas.
import { useEffect, useRef } from 'react'
import type { Workflow } from '../api/client'
import { bucketize } from './taskBuckets'
import { DEFAULT_ACTIONS, BUCKET_ACTIONS } from './pixelSprites'
import { OfficeScene, type Station } from './officeScene'

function stationsFor(workflow: Workflow, labelCounts: Record<string, number>): Station[] {
  const buckets = bucketize(workflow)
  if (buckets) {
    return [
      { key: 'notReady', name: 'Not ready', color: '#94a3b8', action: BUCKET_ACTIONS.notReady, count: sum(buckets.notReady, labelCounts) },
      { key: 'agentWorking', name: 'Agent working', color: '#6366F1', action: BUCKET_ACTIONS.agentWorking, count: sum(buckets.agentWorking, labelCounts) },
      { key: 'waitingHuman', name: 'Waiting on human', color: '#EC4899', action: BUCKET_ACTIONS.waitingHuman, count: sum(buckets.waitingHuman, labelCounts) },
    ]
  }
  return [...workflow.labels]
    .sort((a, b) => a.sort_order - b.sort_order)
    .map((label) => ({
      key: label.id,
      name: label.name,
      color: label.color,
      action: DEFAULT_ACTIONS[label.name] ?? 'idle',
      count: labelCounts[label.name] ?? 0,
    }))
}

const sum = (names: string[], counts: Record<string, number>) => names.reduce((s, n) => s + (counts[n] ?? 0), 0)

export default function TaskFactory({
  workflow,
  labelCounts,
  robots = false,
}: {
  workflow: Workflow
  labelCounts: Record<string, number>
  robots?: boolean
}) {
  const wrapRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const sceneRef = useRef<OfficeScene | undefined>(undefined)
  if (!sceneRef.current) sceneRef.current = new OfficeScene()
  // Robot mode is read live by the draw loop each frame, so just keep the
  // scene's flag in sync — no re-render or re-layout needed.
  sceneRef.current.setRobots(robots)

  // Rebuild station layout whenever the workflow or counts change. Signature
  // string keeps this cheap and avoids re-running on unrelated renders.
  const stations = stationsFor(workflow, labelCounts)
  const sig = stations.map((s) => `${s.key}:${s.count}:${s.action}:${s.color}`).join('|')

  useEffect(() => {
    sceneRef.current!.setStations(stationsFor(workflow, labelCounts))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig])

  useEffect(() => {
    const wrap = wrapRef.current
    const canvas = canvasRef.current
    const scene = sceneRef.current!
    if (!wrap || !canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    // Size the canvas to the scene's responsive grid for the current wrapper
    // width. Re-run on container resize so the grid reflows (phone ↔ desktop).
    const resize = () => {
      // clientWidth includes the wrapper's px-3 padding (12px each side);
      // subtract it so the scene lays out within the actual canvas area.
      scene.layout(wrap.clientWidth - 24)
      const cssW = scene.width()
      const cssH = scene.height()
      const dpr = Math.max(1, Math.floor(window.devicePixelRatio || 1))
      canvas.width = cssW * dpr
      canvas.height = cssH * dpr
      canvas.style.width = `${cssW}px`
      canvas.style.height = `${cssH}px`
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      return { cssW, cssH }
    }

    let { cssW, cssH } = resize()
    const ro = new ResizeObserver(() => { ({ cssW, cssH } = resize()) })
    ro.observe(wrap)

    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    if (reduce) {
      scene.draw(ctx, cssW, cssH) // one static frame, no loop
      return () => ro.disconnect()
    }

    let raf = 0
    let last = performance.now()
    const tick = (now: number) => {
      const dt = Math.min(0.05, (now - last) / 1000) // clamp to avoid jumps after tab-hide
      last = now
      scene.step(dt)
      scene.draw(ctx, cssW, cssH)
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => { cancelAnimationFrame(raf); ro.disconnect() }
  }, [sig])

  return (
    <div ref={wrapRef} className="rounded-lg border border-slate-800 bg-slate-900 p-3">
      <canvas ref={canvasRef} className="block max-w-full" />
    </div>
  )
}
