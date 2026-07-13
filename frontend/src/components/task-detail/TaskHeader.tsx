import { useEffect, useRef, useState } from 'react'
import { api, authedRawFetch, type Task, type Repo, type AgentRun, type WorkflowLabel } from '../../api/client'
import GitStateBadge from '../board/GitStateBadge'
import GitHubAuthWarning from '../shared/GitHubAuthWarning'
import { PRIORITY_LEVELS, priorityLabel } from '../../lib/priority'

export function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-xs text-slate-500 w-16">{label}</span>
      {children}
    </div>
  )
}

// TaskHeader renders the Overview tab's top bar (back/pause/edit/delete), the
// title/description display or edit form, attachments, and the metadata rows
// (label/type/branch/git/PR/agent notes/source/created).
export default function TaskHeader({
  task,
  repos,
  isStartingColumn,
  editingTask,
  editTitle,
  setEditTitle,
  editDesc,
  setEditDesc,
  editType,
  setEditType,
  editRepoId,
  setEditRepoId,
  editMaxCostUsd,
  setEditMaxCostUsd,
  editPriority,
  setEditPriority,
  runs,
  taskSaving,
  taskSaveError,
  onStartEdit,
  onCancelEdit,
  onTaskSave,
  onDelete,
  onTogglePause,
  actionPending,
  onCreatePR,
  creatingPR,
  onSyncGitState,
  onBack,
  labels,
  onMoveLabel,
}: {
  task: Task
  repos: Repo[]
  isStartingColumn: boolean
  editingTask: boolean
  editTitle: string
  setEditTitle: (v: string) => void
  editDesc: string
  setEditDesc: (v: string) => void
  editType: string
  setEditType: (v: string) => void
  editRepoId: string
  setEditRepoId: (v: string) => void
  editMaxCostUsd: string
  setEditMaxCostUsd: (v: string) => void
  editPriority: number
  setEditPriority: (v: number) => void
  runs?: AgentRun[]
  taskSaving: boolean
  taskSaveError: string
  onStartEdit: () => void
  onCancelEdit: () => void
  onTaskSave: () => void
  onDelete: () => void
  onTogglePause: () => void
  actionPending: boolean
  onCreatePR: () => void
  creatingPR: boolean
  onSyncGitState: () => void
  onBack: () => void
  labels: WorkflowLabel[]
  onMoveLabel: (toLabel: string) => void
}) {
  // Cumulative cost across every run this task has had — a simple client-side
  // SUM over the already-fetched runs list (all statuses, matching how the
  // dispatcher's cost-budget guard counts spend; see docs/agents.md).
  const cumulativeCost = (runs ?? []).reduce((sum, r) => sum + (r.cost_usd ?? 0), 0)

  // Attachments are fetched through the authed client and rendered as blob
  // URLs — a bare <img src="/api/v1/uploads/..."> can't carry the
  // Authorization header (see #138) and ignores the BASE_URL prefix used in
  // prod deployments served under a sub-path (e.g. nginx `/tasks/`).
  const blobUrlMapRef = useRef<Map<string, string>>(new Map())
  const [blobUrlVersion, setBlobUrlVersion] = useState(0)
  const attachments = task.attachments

  useEffect(() => {
    // Revoke any previously-created blob URLs before fetching the new set.
    for (const url of blobUrlMapRef.current.values()) {
      URL.revokeObjectURL(url)
    }
    blobUrlMapRef.current = new Map()
    setBlobUrlVersion((v) => v + 1)

    if (!attachments || attachments.length === 0) return

    let cancelled = false
    void (async () => {
      for (const rel of attachments) {
        try {
          const res = await authedRawFetch(api.uploads.downloadUrl(rel))
          if (cancelled || !res.ok) continue
          const blob = await res.blob()
          if (cancelled) return
          const blobUrl = URL.createObjectURL(blob)
          blobUrlMapRef.current.set(rel, blobUrl)
          setBlobUrlVersion((v) => v + 1)
        } catch {
          // Attachments are decorative — skip on failure, no error toast.
        }
      }
    })()

    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task.id, attachments])

  useEffect(() => {
    return () => {
      for (const url of blobUrlMapRef.current.values()) {
        URL.revokeObjectURL(url)
      }
    }
  }, [])

  return (
    <>
      <div className="flex items-center justify-between">
        <button
          onClick={onBack}
          className="text-xs text-slate-500 hover:text-slate-300 text-left"
        >
          ← Board
        </button>
        <div className="flex items-center gap-3">
          <button
            onClick={onTogglePause}
            disabled={actionPending}
            className={`text-xs disabled:opacity-50 ${task.paused ? 'text-emerald-400 hover:text-emerald-300' : 'text-amber-400 hover:text-amber-300'}`}
            title={task.paused ? 'Resume task' : 'Pause task'}
          >
            {task.paused ? '▶ Resume' : '⏸ Pause'}
          </button>
          {isStartingColumn && !editingTask && (
            <button
              onClick={onStartEdit}
              className="text-xs text-indigo-400 hover:text-indigo-300"
              title="Edit task"
            >
              ✎ Edit
            </button>
          )}
          <button
            onClick={onDelete}
            className="text-xs text-red-700 hover:text-red-400"
          >
            Delete
          </button>
        </div>
      </div>

      {editingTask ? (
        <div className="flex flex-col gap-3">
          <div>
            <label className="text-xs text-slate-500 mb-1 block">Title</label>
            <input
              autoFocus
              value={editTitle}
              onChange={(e) => setEditTitle(e.target.value)}
              className="w-full text-sm bg-slate-800 border border-slate-600 rounded px-3 py-2 text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-400"
              placeholder="Task title"
            />
          </div>
          <div>
            <label className="text-xs text-slate-500 mb-1 block">Description</label>
            <textarea
              value={editDesc}
              onChange={(e) => setEditDesc(e.target.value)}
              rows={4}
              className="w-full text-sm bg-slate-800 border border-slate-600 rounded px-3 py-2 text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-400 resize-none"
              placeholder="Description (optional)"
            />
          </div>
          <div>
            <label className="text-xs text-slate-500 mb-1 block">Type</label>
            <select
              value={editType}
              onChange={(e) => setEditType(e.target.value)}
              className="w-full text-sm bg-slate-800 border border-slate-600 rounded px-3 py-2 text-slate-100 focus:outline-none focus:border-indigo-400"
            >
              {['feature', 'bug', 'chore', 'spike'].map((t) => (
                <option key={t} value={t}>{t}</option>
              ))}
            </select>
          </div>
          {repos.length > 0 && (
            <div>
              <label className="text-xs text-slate-500 mb-1 block">Repo</label>
              <select
                value={editRepoId}
                onChange={(e) => setEditRepoId(e.target.value)}
                className="w-full text-sm bg-slate-800 border border-slate-600 rounded px-3 py-2 text-slate-100 focus:outline-none focus:border-indigo-400"
              >
                {repos.map((r) => (
                  <option key={r.id} value={r.id}>{r.name}</option>
                ))}
              </select>
            </div>
          )}
          <div>
            <label className="text-xs text-slate-500 mb-1 block">Max cost (USD)</label>
            <input
              type="number"
              step="0.01"
              min={0}
              value={editMaxCostUsd}
              onChange={(e) => setEditMaxCostUsd(e.target.value)}
              placeholder="Unlimited"
              className="w-full text-sm bg-slate-800 border border-slate-600 rounded px-3 py-2 text-slate-100 placeholder-slate-500 focus:outline-none focus:border-indigo-400"
            />
            <p className="mt-1 text-xs text-slate-500">Advisory budget cap checked by the dispatcher before each dispatch. Empty/0 = unlimited.</p>
          </div>
          <div>
            <label className="text-xs text-slate-500 mb-1 block">Priority</label>
            <select
              value={editPriority}
              onChange={(e) => setEditPriority(Number(e.target.value))}
              className="w-full text-sm bg-slate-800 border border-slate-600 rounded px-3 py-2 text-slate-100 focus:outline-none focus:border-indigo-400"
            >
              {PRIORITY_LEVELS.map((p) => (
                <option key={p.value} value={p.value}>{p.label}</option>
              ))}
            </select>
            <p className="mt-1 text-xs text-slate-500">Dispatch order only — never preempts an already-running task.</p>
          </div>
          {taskSaveError && (
            <p className="text-xs text-red-400">{taskSaveError}</p>
          )}
          <div className="flex gap-2 justify-end">
            <button
              onClick={onCancelEdit}
              disabled={taskSaving}
              className="px-3 py-1.5 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-300 disabled:opacity-50 transition-colors"
            >
              Cancel
            </button>
            <button
              onClick={onTaskSave}
              disabled={taskSaving || !editTitle.trim()}
              className="px-3 py-1.5 text-xs rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50 transition-colors"
            >
              {taskSaving ? 'Saving…' : 'Save changes'}
            </button>
          </div>
        </div>
      ) : (
        <div>
          <h1 className="text-lg font-semibold text-slate-100 leading-snug">{task.title}</h1>
          {task.paused && (
            <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full font-semibold bg-amber-900/70 text-amber-300 mt-2">
              ⏸ Paused — agents will not pick up this task
            </span>
          )}
          {task.description && (
            <p className="text-sm text-slate-400 mt-2">{task.description}</p>
          )}
          {task.attachments && task.attachments.length > 0 && (
            <div className="flex flex-wrap gap-2 mt-3" data-blob-url-version={blobUrlVersion}>
              {task.attachments.map((rel) => {
                const blobUrl = blobUrlMapRef.current.get(rel)
                if (!blobUrl) return null
                return (
                  <img
                    key={rel}
                    src={blobUrl}
                    alt="attachment"
                    className="max-h-48 rounded border border-slate-700 cursor-pointer hover:border-slate-500 transition-colors"
                    onClick={() => window.open(blobUrl, '_blank')}
                    title="Click to open full size"
                  />
                )
              })}
            </div>
          )}
        </div>
      )}

      <div className="flex flex-col gap-2">
        <Row label="Label">
          <span className="text-xs px-2 py-0.5 rounded-full font-medium text-white bg-slate-600">
            {task.label}
          </span>
          {labels.length > 1 && (
            <select
              defaultValue=""
              disabled={actionPending}
              onChange={(e) => {
                if (e.target.value) {
                  onMoveLabel(e.target.value)
                  e.target.value = ''
                }
              }}
              className="text-xs bg-slate-800 border border-slate-700 rounded px-2 py-1 text-slate-300 focus:outline-none focus:ring-1 focus:ring-indigo-500 cursor-pointer disabled:opacity-50"
            >
              <option value="" disabled>Move to…</option>
              {[...labels]
                .filter((l) => l.name !== task.label)
                .sort((a, b) => a.sort_order - b.sort_order)
                .map((l) => (
                  <option key={l.id} value={l.name}>{l.name}</option>
                ))}
            </select>
          )}
        </Row>
        <Row label="Type"><span className="text-xs text-slate-300">{task.type}</span></Row>
        <Row label="Priority">
          <span className="text-xs text-slate-300">
            {priorityLabel(task.priority)}
            {task.queue_position != null ? ` — #${task.queue_position + 1} in dispatch queue` : ''}
          </span>
        </Row>
        <Row label="Cost">
          <span className="text-xs text-slate-300">
            ${cumulativeCost.toFixed(2)}
            {task.max_cost_usd ? ` / $${task.max_cost_usd.toFixed(2)} budget` : ''}
          </span>
        </Row>
        {task.branch && (
          <>
            <Row label="Branch">
              <span className="text-xs font-mono text-slate-300">{task.branch}</span>
            </Row>
            <Row label="Git">
              <div className="flex items-center gap-2">
                <GitStateBadge branch={task.branch} gitState={task.git_state} />
                <span className="text-xs text-slate-400">{task.git_state || 'branched'}</span>
                <button
                  onClick={onSyncGitState}
                  className="text-xs text-slate-500 hover:text-slate-300 transition-colors"
                  title="Sync PR state from GitHub"
                >
                  ↻ Sync
                </button>
              </div>
            </Row>
            <Row label="PR">
              {task.pr_url ? (
                <a
                  href={task.pr_url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors truncate"
                >
                  {task.pr_url.replace('https://github.com/', '')} ↗
                </a>
              ) : (
                <button
                  onClick={onCreatePR}
                  disabled={creatingPR}
                  className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors disabled:opacity-50"
                  title="Push the branch and open a GitHub pull request"
                >
                  {creatingPR ? 'Creating PR…' : '+ Create PR'}
                </button>
              )}
            </Row>
            <GitHubAuthWarning />
          </>
        )}
        {task.agent_notes && (
          <div>
            <p className="text-xs text-slate-500 mb-1" style={{ minHeight: '1.5em' }}>Agent Notes</p>
            <pre className="text-xs text-slate-300 bg-slate-800 rounded p-2 whitespace-pre-wrap max-h-60 overflow-y-auto font-sans">
              {task.agent_notes}
            </pre>
          </div>
        )}
        {task.source === 'github' && task.source_ref && (
          <Row label="Source">
            <a
              href={`https://github.com/${task.source_ref.replace('#', '/issues/')}`}
              target="_blank"
              rel="noreferrer"
              className="text-xs text-indigo-400 hover:text-indigo-300 transition-colors"
            >
              {task.source_ref}
            </a>
          </Row>
        )}
        <Row label="Created">
          <span className="text-xs text-slate-400">{new Date(task.created_at).toLocaleDateString()}</span>
        </Row>
      </div>
    </>
  )
}
