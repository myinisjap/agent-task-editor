import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import PreviewStrip from './PreviewStrip'
import { api, type TaskPreviewCapture } from '../../api/client'

vi.mock('../../api/client', async (orig) => {
  const mod = await orig<typeof import('../../api/client')>()
  return {
    ...mod,
    authedRawFetch: vi.fn().mockResolvedValue({ ok: true, blob: async () => new Blob(['x'], { type: 'image/png' }) }),
    api: { ...mod.api, tasks: { ...mod.api.tasks, previews: vi.fn() } },
  }
})

const cap = (over: Partial<TaskPreviewCapture>): TaskPreviewCapture => ({
  id: 'c1', status: 'ok', error: '', created_at: '2026-01-01T00:00:00Z', finished_at: '2026-01-01T00:01:00Z', images: [], ...over,
})
const img = (id: string, route: string, width: number) => ({
  id, route, variant: 'after' as const, width, height: 800, url: `/api/v1/uploads/t1/preview-${id}.png`,
})

describe('PreviewStrip', () => {
  beforeEach(() => {
    vi.mocked(api.tasks.previews).mockReset()
    URL.createObjectURL = vi.fn(() => 'blob:x')
    URL.revokeObjectURL = vi.fn()
  })

  it('renders nothing without a capture', async () => {
    vi.mocked(api.tasks.previews).mockResolvedValue({ capture: null })
    const { container } = render(<PreviewStrip taskId="t1" />)
    await waitFor(() => expect(api.tasks.previews).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })

  it('shows a running note', async () => {
    vi.mocked(api.tasks.previews).mockResolvedValue({ capture: cap({ status: 'running' }) })
    render(<PreviewStrip taskId="t1" />)
    expect(await screen.findByTestId('preview-running')).toHaveTextContent('Capturing preview')
  })

  it('shows the failure reason', async () => {
    vi.mocked(api.tasks.previews).mockResolvedValue({ capture: cap({ status: 'failed', error: 'port 1 not listening after 120s' }) })
    render(<PreviewStrip taskId="t1" />)
    expect(await screen.findByTestId('preview-failed')).toHaveTextContent('Preview failed: port 1 not listening after 120s')
  })

  it('renders thumbnails and opens a full-size viewer', async () => {
    vi.mocked(api.tasks.previews).mockResolvedValue({
      capture: cap({ images: [img('a', '/inbox', 1280), img('b', '/inbox', 390)] }),
    })
    render(<PreviewStrip taskId="t1" label="Preview" />)
    const thumbs = await screen.findAllByTestId('preview-thumb')
    expect(thumbs).toHaveLength(2)
    expect(thumbs[0]).toHaveTextContent('desktop')
    expect(thumbs[1]).toHaveTextContent('phone')

    await userEvent.click(thumbs[1])
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(await screen.findByTestId('preview-full')).toBeInTheDocument()
    await userEvent.click(screen.getByLabelText('Close'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })
})
