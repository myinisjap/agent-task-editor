// Fun, non-essential dashboard visualization: an animated top-down "office
// floor" where each workflow label (or 3 buckets for custom workflows) is a
// station on the floor, and each task at that label is a little pixel-art
// character milling around it. Characters walk point-to-point across their
// station, pause, and perform that station's action (drawing, hammering,
// stamping, a scanning robot, …). Rendered on a Canvas 2D game loop with
// integer-zoom pixel blitting — zero images, zero deps. The sim lives in
// officeScene.ts; sprite art in pixelSprites.ts; this file is just the host.
import { useEffect, useRef } from 'react'
import type { Workflow } from '../api/client'
import { bucketize } from './taskBuckets'
import { DEFAULT_ACTIONS, BUCKET_ACTIONS } from './pixelSprites'
import { OfficeScene, SCENE_H, type Station } from './officeScene'

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
}: {
  workflow: Workflow
  labelCounts: Record<string, number>
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const sceneRef = useRef<OfficeScene | undefined>(undefined)
  if (!sceneRef.current) sceneRef.current = new OfficeScene()

  // Rebuild station layout whenever the workflow or counts change. Signature
  // string keeps this cheap and avoids re-running on unrelated renders.
  const stations = stationsFor(workflow, labelCounts)
  const sig = stations.map((s) => `${s.key}:${s.count}:${s.action}:${s.color}`).join('|')

  useEffect(() => {
    sceneRef.current!.setStations(stationsFor(workflow, labelCounts))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig])

  useEffect(() => {
    const canvas = canvasRef.current
    const scene = sceneRef.current!
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const cssW = scene.width()
    const cssH = SCENE_H
    const dpr = Math.max(1, Math.floor(window.devicePixelRatio || 1))
    canvas.width = cssW * dpr
    canvas.height = cssH * dpr
    canvas.style.width = `${cssW}px`
    canvas.style.height = `${cssH}px`
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)

    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    if (reduce) {
      scene.draw(ctx, cssW, cssH) // one static frame, no loop
      return
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
    return () => cancelAnimationFrame(raf)
  }, [sig])

  return (
    <div className="overflow-x-auto pb-2">
      <div className="rounded-lg border border-slate-800 bg-slate-900 p-3 inline-block">
        <canvas ref={canvasRef} className="block" />
      </div>
    </div>
  )
}
