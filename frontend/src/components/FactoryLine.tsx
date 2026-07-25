// Fun, non-essential dashboard visualization: a clean-vector "assembly line"
// where each workflow label is a STATION (its machine performs an action
// representative of that label) and each task is a PART being assembled that
// rides CONVEYOR BELTS from one station to the next, gaining structure as it
// goes. The default workflow gets a bespoke machine per label; custom workflows
// collapse to the 3 buckets the office scene uses (see factoryStations.ts).
//
// The clever bit: the belts are routed from the *real* flex layout. We measure
// each card's getBoundingClientRect() against its neighbor's and pick one of
// three cases — a straight belt (same row), a vertical drop (card directly
// below, i.e. single-column phones), or an L-shaped "return hook" (the line
// wrapped) — so the conveyors re-attach correctly as the line reflows. A
// ResizeObserver re-routes on every container resize.
import { useEffect, useMemo, useRef } from 'react'
import type { Workflow } from '../api/client'
import { stationsFor } from './factoryStations'
import { machineSvg, itemSvg, FACTORY_DEFS } from './factoryMachines'
import './factoryLine.css'

const CAP = 9 // max count shown in a station's badge before a "+N" suffix

type Pt = { x: number; y: number }

// Rounded orthogonal path through ordered points (used for the return hook).
function orthoPath(pts: Pt[], r: number): string {
  if (pts.length === 2) return `M${pts[0].x} ${pts[0].y} L${pts[1].x} ${pts[1].y}`
  let d = `M${pts[0].x} ${pts[0].y}`
  for (let i = 1; i < pts.length - 1; i++) {
    const p = pts[i], a = pts[i - 1], b = pts[i + 1]
    const inLen = Math.hypot(p.x - a.x, p.y - a.y) || 1
    const outLen = Math.hypot(b.x - p.x, b.y - p.y) || 1
    const ri = Math.min(r, inLen / 2, outLen / 2)
    const inU = { x: (p.x - a.x) / inLen, y: (p.y - a.y) / inLen }
    const outU = { x: (b.x - p.x) / outLen, y: (b.y - p.y) / outLen }
    d += ` L${(p.x - inU.x * ri).toFixed(1)} ${(p.y - inU.y * ri).toFixed(1)}`
    d += ` Q${p.x} ${p.y} ${(p.x + outU.x * ri).toFixed(1)} ${(p.y + outU.y * ri).toFixed(1)}`
  }
  const last = pts[pts.length - 1]
  d += ` L${last.x} ${last.y}`
  return d
}

const SVGNS = 'http://www.w3.org/2000/svg'
function beltPath(d: string, w: number, stroke: string, cls?: string): SVGPathElement {
  const p = document.createElementNS(SVGNS, 'path')
  p.setAttribute('d', d)
  p.setAttribute('fill', 'none')
  p.setAttribute('stroke', stroke)
  p.setAttribute('stroke-width', String(w))
  p.setAttribute('stroke-linejoin', 'round')
  p.setAttribute('stroke-linecap', 'round')
  if (cls) p.setAttribute('class', cls)
  return p
}

export default function FactoryLine({
  workflow,
  labelCounts,
}: {
  workflow: Workflow
  labelCounts: Record<string, number>
}) {
  const stations = useMemo(() => stationsFor(workflow, labelCounts), [workflow, labelCounts])
  // Signature keeps the routing effect from re-running on unrelated renders.
  const sig = stations.map((s) => `${s.key}:${s.count}:${s.action}:${s.color}`).join('|')

  const hostRef = useRef<HTMLDivElement>(null)
  const lineRef = useRef<HTMLDivElement>(null)
  const beltsRef = useRef<SVGSVGElement>(null)
  const productsRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const host = hostRef.current
    const line = lineRef.current
    const belts = beltsRef.current
    const products = productsRef.current
    if (!host || !line || !belts || !products) return

    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
    const anims: Animation[] = []
    let measure: SVGPathElement | null = null

    const pathLength = (d: string): number => {
      if (!measure) {
        measure = document.createElementNS(SVGNS, 'path')
        belts.appendChild(measure)
      }
      measure.setAttribute('d', d)
      return measure.getTotalLength()
    }

    const layout = () => {
      const cards = Array.from(line.children) as HTMLElement[]
      const hostRect = host.getBoundingClientRect()
      belts.setAttribute('viewBox', `0 0 ${hostRect.width} ${hostRect.height}`)
      belts.setAttribute('width', String(hostRect.width))
      belts.setAttribute('height', String(hostRect.height))
      belts.innerHTML = ''
      products.innerHTML = ''
      anims.forEach((a) => a.cancel())
      anims.length = 0
      measure = null

      const BW = 14, PAD = 6
      const rects = cards.map((c) => {
        const r = c.getBoundingClientRect()
        return {
          left: r.left - hostRect.left,
          right: r.right - hostRect.left,
          top: r.top - hostRect.top,
          bottom: r.bottom - hostRect.top,
          midX: (r.left + r.right) / 2 - hostRect.left,
          midY: (r.top + r.bottom) / 2 - hostRect.top,
          h: r.height,
          w: r.width,
        }
      })

      for (let i = 0; i < rects.length - 1; i++) {
        const a = rects[i], b = rects[i + 1]
        const sameRow = Math.abs(a.midY - b.midY) < a.h * 0.5
        const stacked = !sameRow && Math.abs(a.midX - b.midX) < a.w * 0.6
        let pts: Pt[]
        if (sameRow) {
          pts = [{ x: a.right - 1, y: a.midY }, { x: b.left + 1, y: b.midY }]
        } else if (stacked) {
          pts = [{ x: a.midX, y: a.bottom - 1 }, { x: b.midX, y: b.top + 1 }]
        } else {
          // Return hook: exit right, drop into the inter-row gap, run left, rise
          // into the next row's start.
          const gapY = (a.bottom + b.top) / 2
          const outX = Math.min(hostRect.width - PAD, a.right + 26)
          const inX = Math.max(PAD, b.left - 26)
          pts = [
            { x: a.right - 1, y: a.midY },
            { x: outX, y: a.midY },
            { x: outX, y: gapY },
            { x: inX, y: gapY },
            { x: inX, y: b.midY },
            { x: b.left + 1, y: b.midY },
          ]
        }
        const d = orthoPath(pts, 14)
        belts.appendChild(beltPath(d, BW + 5, '#1b2536'))
        belts.appendChild(beltPath(d, BW, '#3b4657'))
        const tread = beltPath(d, BW - 5, '#5a6b85', 'fl-belt-tread')
        tread.setAttribute('stroke-dasharray', '3 13')
        tread.setAttribute('stroke-linecap', 'butt')
        if (!reduce) tread.style.animation = 'fl-treadMove .7s linear infinite'
        belts.appendChild(tread)

        // The evolving part, in the state it LEAVES this station in, riding the
        // belt via CSS offset-path.
        const prod = document.createElement('div')
        prod.className = 'fl-product'
        const stage = Math.min(i, 6)
        const partColor = (stations[i + 1] ?? stations[i]).color
        prod.innerHTML = `<svg viewBox="49 44 34 38">${itemSvg(stage, partColor)}</svg>`
        prod.style.setProperty('offset-path', `path('${d}')`)
        products.appendChild(prod)
        if (!reduce) {
          const len = pathLength(d)
          const dur = Math.max(2200, (len / 55) * 1000)
          const anim = prod.animate(
            [{ offsetDistance: '0%' }, { offsetDistance: '100%' }] as unknown as Keyframe[],
            { duration: dur, iterations: Infinity, easing: 'linear', delay: -Math.random() * dur },
          )
          anims.push(anim)
        } else {
          prod.style.setProperty('offset-distance', '50%')
        }
      }
    }

    // Belts depend on final flex layout; run after paint, then keep in sync.
    const raf = requestAnimationFrame(() => requestAnimationFrame(layout))
    const ro = new ResizeObserver(() => layout())
    ro.observe(host)
    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
      anims.forEach((a) => a.cancel())
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sig])

  return (
    <div className="rounded-lg border border-slate-800 bg-slate-900 p-3">
      <div ref={hostRef} className="fl-host">
        <div ref={lineRef} className="fl-line">
          {stations.map((st) => {
            const over = st.count - CAP
            return (
              <div key={st.key} className="fl-station" style={{ ['--sc' as string]: st.color } as React.CSSProperties}>
                <div className="fl-top" />
                <div className="fl-head">
                  <span className="fl-name">{st.name}</span>
                  <span className="fl-count">
                    {Math.min(st.count, CAP)}
                    {over > 0 && <span className="fl-over">+{over}</span>}
                  </span>
                </div>
                <div className="fl-machine" dangerouslySetInnerHTML={{ __html: machineSvg(st.action, st.color) }} />
                <div className="fl-base" />
              </div>
            )
          })}
        </div>
        <svg ref={beltsRef} className="fl-belts" aria-hidden="true" />
        <div ref={productsRef} className="fl-products" aria-hidden="true" />
        <svg className="fl-defs" aria-hidden="true" dangerouslySetInnerHTML={{ __html: `<defs>${FACTORY_DEFS}</defs>` }} />
      </div>
    </div>
  )
}
