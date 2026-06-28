import { useEffect, useRef, useState } from 'react'
import { api } from '../../api/client'
import type { Repo, Workflow } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'

type Props = {
  workflow: Workflow
  onClose: () => void
}

export default function NewTaskModal({ workflow, onClose }: Props) {
  const { upsert } = useTasksStore()
  const [repos, setRepos] = useState<Repo[]>([])
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [type, setType] = useState<'feature' | 'bug' | 'chore' | 'spike'>('feature')
  const [repoId, setRepoId] = useState('')
  const [attachments, setAttachments] = useState<File[]>([])
  const [attachmentPreviews, setAttachmentPreviews] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const titleRef = useRef<HTMLInputElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    api.repos.list().then((r) => {
      const workflowRepos = r.filter((repo) => repo.workflow_id === workflow.id)
      setRepos(workflowRepos)
      if (workflowRepos.length > 0) setRepoId(workflowRepos[0].id)
    })
    titleRef.current?.focus()
  }, [workflow.id])

  // Revoke object URLs when component unmounts to avoid memory leaks
  useEffect(() => {
    return () => {
      attachmentPreviews.forEach((url) => URL.revokeObjectURL(url))
    }
  }, [attachmentPreviews])

  function handleFilesSelected(files: FileList | null) {
    if (!files) return
    const newFiles: File[] = []
    const newPreviews: string[] = []
    for (let i = 0; i < files.length; i++) {
      const f = files[i]
      if (!f.type.startsWith('image/')) continue
      newFiles.push(f)
      newPreviews.push(URL.createObjectURL(f))
    }
    setAttachments((prev) => [...prev, ...newFiles])
    setAttachmentPreviews((prev) => [...prev, ...newPreviews])
  }

  function removeAttachment(index: number) {
    URL.revokeObjectURL(attachmentPreviews[index])
    setAttachments((prev) => prev.filter((_, i) => i !== index))
    setAttachmentPreviews((prev) => prev.filter((_, i) => i !== index))
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!title.trim() || !repoId) return
    setSubmitting(true)
    setError('')
    try {
      const fd = new FormData()
      fd.append('title', title.trim())
      fd.append('description', description.trim())
      fd.append('type', type)
      fd.append('repo_id', repoId)
      fd.append('workflow_id', workflow.id)
      attachments.forEach((f) => fd.append('attachments', f))

      const task = await api.tasks.create(fd)
      upsert(task)
      onClose()
    } catch (e) {
      setError(String(e))
      setSubmitting(false)
    }
  }

  function handleBackdrop(e: React.MouseEvent) {
    if (e.target === e.currentTarget) onClose()
  }

  function handleDrop(e: React.DragEvent) {
    e.preventDefault()
    handleFilesSelected(e.dataTransfer.files)
  }

  function handleDragOver(e: React.DragEvent) {
    e.preventDefault()
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60"
      onClick={handleBackdrop}
    >
      <div className="bg-slate-900 border border-slate-700 rounded-xl shadow-2xl w-full max-w-md mx-4 max-h-[90vh] flex flex-col">
        <div className="flex items-center justify-between px-5 py-4 border-b border-slate-700">
          <h2 className="text-sm font-semibold text-slate-100">New Task</h2>
          <button
            onClick={onClose}
            className="text-slate-500 hover:text-slate-300 transition-colors text-lg leading-none"
          >
            ×
          </button>
        </div>

        <form onSubmit={handleSubmit} className="p-5 flex flex-col gap-4 overflow-y-auto">
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Title</label>
            <input
              ref={titleRef}
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Short task description"
              required
              className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Description</label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Additional context for the agent (optional)"
              rows={3}
              className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 placeholder-slate-600 focus:outline-none focus:ring-1 focus:ring-indigo-500 resize-none"
            />
          </div>

          <div className="flex gap-3">
            <div className="flex flex-col gap-1.5 flex-1">
              <label className="text-xs font-medium text-slate-400">Type</label>
              <select
                value={type}
                onChange={(e) => setType(e.target.value as typeof type)}
                className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-1 focus:ring-indigo-500"
              >
                <option value="feature">Feature</option>
                <option value="bug">Bug</option>
                <option value="chore">Chore</option>
                <option value="spike">Spike</option>
              </select>
            </div>

            <div className="flex flex-col gap-1.5 flex-1">
              <label className="text-xs font-medium text-slate-400">Repo</label>
              {repos.length === 0 ? (
                <div className="text-xs text-slate-500 py-2">No repos in this workflow</div>
              ) : (
                <select
                  value={repoId}
                  onChange={(e) => setRepoId(e.target.value)}
                  className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-100 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                >
                  {repos.map((r) => (
                    <option key={r.id} value={r.id}>{r.name}</option>
                  ))}
                </select>
              )}
            </div>
          </div>

          {/* Image Attachments */}
          <div className="flex flex-col gap-1.5">
            <label className="text-xs font-medium text-slate-400">Attachments</label>
            <div
              className="border border-dashed border-slate-600 rounded-lg p-3 text-center cursor-pointer hover:border-slate-500 transition-colors"
              onClick={() => fileInputRef.current?.click()}
              onDrop={handleDrop}
              onDragOver={handleDragOver}
            >
              <p className="text-xs text-slate-500">
                Click or drag &amp; drop images here
              </p>
              <p className="text-xs text-slate-700 mt-0.5">PNG, JPG, GIF, WebP — max 10 MB each</p>
            </div>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/*"
              multiple
              className="hidden"
              onChange={(e) => handleFilesSelected(e.target.files)}
            />

            {attachmentPreviews.length > 0 && (
              <div className="flex flex-wrap gap-2 mt-1">
                {attachmentPreviews.map((src, i) => (
                  <div key={i} className="relative group">
                    <img
                      src={src}
                      alt={attachments[i]?.name}
                      className="w-16 h-16 object-cover rounded border border-slate-700"
                    />
                    <button
                      type="button"
                      onClick={() => removeAttachment(i)}
                      className="absolute -top-1 -right-1 w-4 h-4 bg-red-600 hover:bg-red-500 rounded-full text-white text-xs flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity"
                      title="Remove"
                    >
                      ×
                    </button>
                    <p className="text-xs text-slate-600 mt-0.5 truncate w-16 text-center">{attachments[i]?.name}</p>
                  </div>
                ))}
              </div>
            )}
          </div>

          {error && <p className="text-xs text-red-400">{error}</p>}

          <div className="flex justify-end gap-2 pt-1">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 text-sm text-slate-400 hover:text-slate-200 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={submitting || !title.trim() || !repoId}
              className="px-4 py-1.5 text-sm bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg transition-colors"
            >
              {submitting ? 'Creating…' : 'Create'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
