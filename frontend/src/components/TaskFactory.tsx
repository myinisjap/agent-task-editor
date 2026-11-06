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
// pauses while the tab is hidden; prefers-reduced-motion draws one static frame
// (no particles). The scene adds pixel shadows, art-pixel-resolution lighting
// layers and a small pooled particle system (sparks, paper, steam, confetti);
// all of that lives in officeScene.ts / officeParticles.ts.
// Sim in officeScene.ts; station derivation in officeStations.ts; sprite art in
// pixelSprites.ts; this file measures the container and hosts the canvas.
import { useEffect, useMemo, useRef } from 'react'
import type { Task, Workflow } from '../api/client'
import { wsClient } from '../api/ws'
import { OfficeScene } from './officeScene'
import { officeStationsFor, sceneTaskStatus } from './officeStations'
import { loadOfficeSheets } from './officeSheets'

export default function TaskFactory({
  workflow,
  labelCounts,
  robots = false,
  zoom = 3,
  tasks,
  rateLimitedUntil,
  now,
  onOpenTask,
}: {
  workflow: Workflow
  labelCounts: Record<string, number>
  robots?: boolean
  zoom?: number | 'auto'
  // Real tasks (task mode: one worker per task). Absent = count-only scene.
  tasks?: Task[]
  rateLimitedUntil?: Record<string, string>
  now?: number
  onOpenTask?: (id: string) => void
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
  const stations = useMemo(
    () => officeStationsFor(workflow, labelCounts, tasks, { rateLimitedUntil, now }),
    [workflow, labelCounts, tasks, rateLimitedUntil, now],
  )
  // layoutSig drives the loop/resize effect; taskSig adds the per-task fields
  // and only re-syncs the scene (never restarts the RAF loop).
  const layoutSig = stations
    .map((s) => `${s.key}:${s.count}:${s.action}:${s.color}:${s.gate ? 1 : 0}:${s.terminal ? 1 : 0}`)
    .join('|')
  const taskSig =
    layoutSig +
    '#' +
    stations
      .map((s) =>
        (s.tasks ?? [])
          .map((t) => `${t.id}:${t.order}:${t.runStatus ?? ''}:${t.rateLimited ? 1 : 0}:${t.clearsAt ?? ''}:${t.type}:${t.title}`)
          .join(','),
      )
      .join('|')
  const stationsRef = useRef(stations)
  stationsRef.current = stations
  const redrawRef = useRef<(() => void) | null>(null)
  const tipRef = useRef<HTMLDivElement>(null)
  const hoverRef = useRef<string | null>(null)

  useEffect(() => {
    sceneRef.current!.setStations(stationsRef.current)
    redrawRef.current?.() // reduced motion has no loop: repaint the static frame
  }, [taskSig])

  // Run outcomes go straight into the scene (no re-render): a failed run makes
  // its worker frustrated, a finished one primes the celebrate on the label move.
  const taskMode = tasks !== undefined
  useEffect(() => {
    if (!taskMode) return
    return wsClient.on((event) => {
      if (event.type === 'task.agent_done') sceneRef.current!.noteRunDone(event.payload.task_id, event.payload.status)
    })
  }, [taskMode])

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
    // Swap in the PNG sprite sheets once they decode (grid sprites until then).
    let alive = true
    loadOfficeSheets().then((sheets) => {
      if (!alive || !sheets) return
      scene.setSheets(sheets)
      if (reduce) scene.draw(ctx, cssW, cssH)
    })
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
      redrawRef.current = () => {
        scene.settle()
        scene.draw(ctx, cssW, cssH)
      }
      redrawRef.current() // finish tweens/fades so the single frame is the end state; no loop
      return () => {
        alive = false
        redrawRef.current = null
        ro.disconnect()
      }
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
      alive = false
      stop()
      document.removeEventListener('visibilitychange', onVisibility)
      ro.disconnect()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layoutSig, zoom])

  // Hover tooltip + click-to-open. Pointer events only (never per frame), and
  // the tooltip is written straight to the DOM so moves don't re-render.
  const onPointerMove = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current
    const wrap = wrapRef.current
    const tip = tipRef.current
    if (!canvas || !wrap || !tip) return
    const rect = canvas.getBoundingClientRect()
    const hit = sceneRef.current!.pickAt(e.clientX - rect.left, e.clientY - rect.top)
    canvas.style.cursor = hit ? 'pointer' : ''
    if (!hit) {
      hoverRef.current = null
      tip.style.display = 'none'
      return
    }
    if (hoverRef.current !== hit.taskId) {
      hoverRef.current = hit.taskId
      tip.replaceChildren()
      const title = document.createElement('div')
      title.className = 'truncate font-medium'
      title.textContent = hit.task.title
      const status = document.createElement('div')
      status.className = 'text-slate-400'
      status.textContent = sceneTaskStatus(hit.task, hit.gate)
      tip.append(title, status)
    }
    tip.style.display = 'block'
    const wr = wrap.getBoundingClientRect()
    const x = e.clientX - wr.left + 12
    const y = e.clientY - wr.top + 14
    tip.style.left = `${Math.max(0, Math.min(x, wr.width - tip.offsetWidth - 4))}px`
    tip.style.top = `${Math.max(0, Math.min(y, wr.height - tip.offsetHeight - 4))}px`
  }
  const onPointerLeave = () => {
    hoverRef.current = null
    if (tipRef.current) tipRef.current.style.display = 'none'
    if (canvasRef.current) canvasRef.current.style.cursor = ''
  }
  const onClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current
    if (!canvas || !onOpenTask) return
    const rect = canvas.getBoundingClientRect()
    const hit = sceneRef.current!.pickAt(e.clientX - rect.left, e.clientY - rect.top)
    if (hit) onOpenTask(hit.taskId)
  }

  return (
    <div ref={wrapRef} className="relative rounded-lg border border-slate-800 bg-slate-900 p-3">
      <canvas
        ref={canvasRef}
        className="block max-w-full"
        onPointerMove={onPointerMove}
        onPointerLeave={onPointerLeave}
        onClick={onClick}
      />
      <div
        ref={tipRef}
        className="pointer-events-none absolute z-10 max-w-[16rem] rounded bg-slate-950/95 px-2 py-1 text-xs text-slate-100 shadow"
        style={{ display: 'none' }}
      />
    </div>
  )
}
