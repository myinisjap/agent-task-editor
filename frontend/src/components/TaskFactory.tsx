// Fun, non-essential dashboard visualization: an animated top-down "office
// floor" where each workflow label (or 3 buckets for custom workflows) is a
// station with its own workstation (desk / anvil / lab bench / charging rack
// / podium), and each task at that label is a little pixel-art worker that
// queues up, walks to the workstation, and performs that station's action
// facing it, then steps aside for the next. Rendered on a Canvas 2D game loop
// with integer-zoom pixel blitting — zero images, zero deps. Stations wrap
// into a responsive grid sized to the container, so it stacks on mobile
// instead of h-scrolling.
//
// `zoom` is the integer pixel scale (default 3, max 6) or 'auto' (the largest
// zoom, up to 6, that fits every station in one row). When a task moves
// between labels its worker walks from the old station to the new one; new /
// removed tasks fade in / out. Workers at human-gate stations wave. The loop
// pauses while the tab is hidden; prefers-reduced-motion draws one static frame.
// Sim in officeScene.ts; station derivation in officeStations.ts; sprite art in
// pixelSprites.ts; this file measures the container and hosts the canvas.
import { useEffect, useRef } from 'react'
import type { Workflow } from '../api/client'
import { OfficeScene } from './officeScene'
import { officeStationsFor } from './officeStations'

export default function TaskFactory({
  workflow,
  labelCounts,
  robots = false,
  zoom = 3,
}: {
  workflow: Workflow
  labelCounts: Record<string, number>
  robots?: boolean
  zoom?: number | 'auto'
}) {
  const wrapRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const sceneRef = useRef<OfficeScene | undefined>(undefined)
  if (!sceneRef.current) sceneRef.current = new OfficeScene()
  // Robot mode is read live by the draw loop each frame, so just keep the
  // scene's flag in sync — no re-render or re-layout needed. Zoom likewise;
  // the loop effect re-runs on zoom change to re-measure the canvas.
  sceneRef.current.setRobots(robots)
  sceneRef.current.setZoom(zoom)

  // Rebuild station layout whenever the workflow or counts change. Signature
  // string keeps this cheap and avoids re-running on unrelated renders.
  const stations = officeStationsFor(workflow, labelCounts)
  const sig = stations.map((s) => `${s.key}:${s.count}:${s.action}:${s.color}:${s.gate ? 1 : 0}`).join('|')

  useEffect(() => {
    sceneRef.current!.setStations(officeStationsFor(workflow, labelCounts))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig])

  useEffect(() => {
    const wrap = wrapRef.current
    const canvas = canvasRef.current
    const scene = sceneRef.current!
    if (!wrap || !canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

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
    const ro = new ResizeObserver(() => {
      ;({ cssW, cssH } = resize())
      // Resizing clears the canvas; with no loop running (reduced motion)
      // redraw a static frame so it doesn't stay blank.
      if (reduce) {
        scene.settle()
        scene.draw(ctx, cssW, cssH)
      }
    })
    ro.observe(wrap)

    if (reduce) {
      scene.settle() // finish tweens/fades so the single frame is the end state
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
    const start = () => {
      if (raf) return
      last = performance.now()
      raf = requestAnimationFrame(tick)
    }
    const stop = () => {
      cancelAnimationFrame(raf)
      raf = 0
    }
    // Don't burn CPU in a background tab.
    const onVisibility = () => (document.hidden ? stop() : start())
    document.addEventListener('visibilitychange', onVisibility)
    if (!document.hidden) start()
    return () => {
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
      ro.disconnect()
    }
  }, [sig, zoom])

  return (
    <div ref={wrapRef} className="rounded-lg border border-slate-800 bg-slate-900 p-3">
      <canvas ref={canvasRef} className="block max-w-full" />
    </div>
  )
}
