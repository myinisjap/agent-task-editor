import { useCallback, useEffect, useRef, useState } from 'react'
import { api, authedRawFetch, type TaskPreviewCapture, type TaskPreviewImage } from '../../api/client'
import ModalShell from '../shared/ModalShell'

// Image URLs from the API are absolute server paths; rebuild them through the
// client so BASE_URL (sub-path deployments) is respected.
function imageUrl(img: TaskPreviewImage): string {
  return api.uploads.downloadUrl(img.url.replace(/^.*\/uploads\//, ''))
}

function sizeName(img: TaskPreviewImage): string {
  return img.width >= 1000 ? 'desktop' : 'phone'
}

// useBlobUrl fetches an authed image as a blob URL (a bare <img src> cannot
// send the Authorization header). It starts when `enabled` flips true.
function useBlobUrl(img: TaskPreviewImage, enabled: boolean): string | null {
  const [url, setUrl] = useState<string | null>(null)
  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    let made: string | null = null
    authedRawFetch(imageUrl(img))
      .then((res) => (res.ok ? res.blob() : null))
      .then((blob) => {
        if (cancelled || !blob) return
        made = URL.createObjectURL(blob)
        setUrl(made)
      })
      .catch(() => {})
    return () => {
      cancelled = true
      if (made) URL.revokeObjectURL(made)
    }
  }, [img.url, enabled]) // eslint-disable-line react-hooks/exhaustive-deps
  return url
}

function Thumb({ img, onOpen }: { img: TaskPreviewImage; onOpen: () => void }) {
  const ref = useRef<HTMLButtonElement>(null)
  const [visible, setVisible] = useState(typeof IntersectionObserver === 'undefined')
  useEffect(() => {
    if (visible || !ref.current) return
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        setVisible(true)
        io.disconnect()
      }
    })
    io.observe(ref.current)
    return () => io.disconnect()
  }, [visible])
  const src = useBlobUrl(img, visible)
  return (
    <button
      ref={ref}
      type="button"
      onClick={onOpen}
      className="snap-start shrink-0 w-[120px] min-h-[40px] text-left"
      data-testid="preview-thumb"
      aria-label={`Open ${img.route} ${sizeName(img)} screenshot`}
    >
      <div className="h-20 w-full overflow-hidden rounded border border-slate-700 bg-slate-950">
        {src && <img src={src} alt={`${img.route} (${sizeName(img)})`} loading="lazy" className="w-full h-full object-cover object-top" />}
      </div>
      <div className="mt-1 text-[10px] text-slate-400 truncate">
        <span className="font-mono">{img.route}</span> · {sizeName(img)}
      </div>
    </button>
  )
}

function Viewer({ images, index, onIndex, onClose }: { images: TaskPreviewImage[]; index: number; onIndex: (i: number) => void; onClose: () => void }) {
  const img = images[index]
  const src = useBlobUrl(img, true)
  const prev = useCallback(() => onIndex((index + images.length - 1) % images.length), [index, images.length, onIndex])
  const next = useCallback(() => onIndex((index + 1) % images.length), [index, images.length, onIndex])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'ArrowLeft') prev()
      else if (e.key === 'ArrowRight') next()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [prev, next])
  const btn = 'min-h-[44px] min-w-[44px] px-3 rounded border border-slate-600 text-sm text-slate-200 hover:bg-slate-800'
  return (
    <ModalShell onClose={onClose} ariaLabel="Preview screenshot" className="w-full max-w-[95vw] max-h-[95vh] flex flex-col bg-slate-900 p-2 rounded">
      <div className="flex items-center justify-between gap-2 pb-2">
        <div className="text-xs text-slate-300 truncate">
          <span className="font-mono">{img.route}</span> · {sizeName(img)} {img.width}×{img.height} · {index + 1}/{images.length}
        </div>
        <div className="flex gap-2 shrink-0">
          <button type="button" className={btn} onClick={prev} aria-label="Previous screenshot" disabled={images.length < 2}>‹</button>
          <button type="button" className={btn} onClick={next} aria-label="Next screenshot" disabled={images.length < 2}>›</button>
          <button type="button" className={btn} onClick={onClose} aria-label="Close">✕</button>
        </div>
      </div>
      <div className="overflow-auto flex-1 min-h-0">
        {src ? <img src={src} alt={`${img.route} (${sizeName(img)})`} className="max-w-full h-auto mx-auto" data-testid="preview-full" /> : <div className="text-xs text-slate-500 p-4">Loading…</div>}
      </div>
    </ModalShell>
  )
}

// PreviewStrip shows the latest preview screenshots for a task as a
// horizontally scrollable thumbnail strip that opens full size
// (docs/workflows.md#preview-screenshots). Renders nothing when no capture
// exists. `refreshKey` changes when the task changes or a task.preview event
// arrives.
export default function PreviewStrip({ taskId, refreshKey = '', label }: { taskId: string | undefined; refreshKey?: string | number; label?: string }) {
  const [capture, setCapture] = useState<TaskPreviewCapture | null>(null)
  const [open, setOpen] = useState<number | null>(null)

  useEffect(() => {
    if (!taskId) return
    let cancelled = false
    api.tasks
      .previews(taskId)
      .then((r) => {
        if (!cancelled) setCapture(r.capture)
      })
      .catch(() => {
        if (!cancelled) setCapture(null)
      })
    return () => {
      cancelled = true
    }
  }, [taskId, refreshKey])

  if (!capture) return null
  const wrap = (node: React.ReactNode) =>
    label ? (
      <div className="flex flex-col sm:flex-row sm:gap-3 min-w-0">
        <div className="sm:w-24 shrink-0 text-xs font-semibold uppercase tracking-wide text-slate-500">{label}</div>
        <div className="min-w-0 flex-1">{node}</div>
      </div>
    ) : (
      node
    )
  if (capture.status === 'running') {
    return wrap(<div className="text-xs text-slate-400" data-testid="preview-running">Capturing preview…</div>)
  }
  if (capture.status === 'failed') {
    return wrap(<div className="text-xs text-amber-400 break-words" data-testid="preview-failed">Preview failed: {capture.error || 'unknown error'}</div>)
  }
  const images = capture.images
  if (images.length === 0) return null
  return wrap(
    <div className="min-w-0" data-testid="preview-strip">
      <div className="flex gap-2 overflow-x-auto snap-x pb-1">
        {images.map((img, i) => (
          <Thumb key={img.id} img={img} onOpen={() => setOpen(i)} />
        ))}
      </div>
      {capture.error && <div className="mt-1 text-xs text-amber-400 break-words">Some previews failed: {capture.error}</div>}
      {open !== null && images[open] && <Viewer images={images} index={open} onIndex={setOpen} onClose={() => setOpen(null)} />}
    </div>
  )
}
