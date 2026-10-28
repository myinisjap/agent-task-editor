import { Ban, Copy, Pause, Pencil, Play, RefreshCw, TriangleAlert } from 'lucide-react'
import { labelDisplayName, labelDisplayNameByName } from '../../lib/labelDisplay'
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, authedRawFetch, type Task, type Repo, type WorkflowLabel, type WorkflowTransition } from '../../api/client'
import GitStateBadge from '../board/GitStateBadge'
import SideEffectBadge from '../shared/SideEffectBadge'
import GitHubAuthWarning from '../shared/GitHubAuthWarning'
import { PRIORITY_LEVELS, priorityLabel } from '../../lib/priority'
import AgentNotesModal from './AgentNotesModal'
import { useIsMobile } from '../../lib/useIsMobile'
import { blockReasonLabel, isTransientBlockReason } from '../../lib/blockReason'
import { formatRelativeCountdown } from '../../lib/format'
import { canHumanMove } from '../../lib/humanMoves'
import { Badge, Button, Field, controlClasses, Icon } from '../ui'

export function Row({ label, children, wide = false }: { label: string; children: React.ReactNode; wide?: boolean }) {
  return (
    <div className={`flex items-center gap-2 min-w-0 ${wide ? 'lg:col-span-2' : ''}`}>
      <span className="text-xs text-fg-subtle w-16 shrink-0">{label}</span>
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
  transitions,
  onMoveLabel,
  onDuplicate,
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
  /** Workflow transitions; "Move to…" lists only labels a human can legally move to. */
  transitions?: WorkflowTransition[]
  onMoveLabel: (toLabel: string) => void
  /** When set, renders a "Duplicate" button that opens a pre-filled New Task modal for this task. */
  onDuplicate?: () => void
}) {
  // Cumulative cost across every run this task has had (all statuses,
  // matching how the dispatcher's cost-budget guard counts spend; see
  // docs/agents.md). Computed server-side (SumTaskCost) and returned on the
  // task itself, since GET /tasks/{id}/runs is now paginated — a client-side
  // SUM over one page of runs would silently undercount once a task has more
  // runs than fit on a page.
  const cumulativeCost = task.cumulative_cost_usd ?? 0

  // Labels a human can legally move this task to (mirrors the backend's rule;
  // see lib/humanMoves.ts), in board order.
  const moveTargets = [...labels]
    .filter((l) => canHumanMove(transitions ?? [], task.label, l.name))
    .sort((a, b) => a.sort_order - b.sort_order)

  // Attachments are fetched through the authed client and rendered as blob
  // URLs — a bare <img src="/api/v1/uploads/..."> can't carry the
  // Authorization header (see #138) and ignores the BASE_URL prefix used in
  // prod deployments served under a sub-path (e.g. nginx `/tasks/`).
  const blobUrlMapRef = useRef<Map<string, string>>(new Map())
  const [blobUrlVersion, setBlobUrlVersion] = useState(0)
  const attachments = task.attachments

  const [notesModalOpen, setNotesModalOpen] = useState(false)
  const [descModalOpen, setDescModalOpen] = useState(false)
  const isMobile = useIsMobile()

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
    // Depend on a stable key derived from the attachment paths rather than the
    // array itself — `task.attachments` gets a fresh identity on every
    // refetch (JSON.parse), which would otherwise revoke and re-download all
    // attachments on every unrelated WS-driven task refresh.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task.id, attachments?.join('|')])

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
          className="text-xs text-fg-subtle hover:text-fg text-left"
        >
          ← Board
        </button>
        <div className="flex items-center gap-3">
          <button
            onClick={onTogglePause}
            disabled={actionPending}
            className={`inline-flex items-center gap-1 text-xs disabled:opacity-50 ${task.paused ? 'text-success' : 'text-warning'}`}
            title={task.paused ? 'Resume task' : 'Pause task'}
          >
            <Icon icon={task.paused ? Play : Pause} size="sm" />
            {task.paused ? 'Resume' : 'Pause'}
          </button>
          {isStartingColumn && !editingTask && (
            <button
              onClick={onStartEdit}
              className="inline-flex items-center gap-1 text-xs text-accent-fg"
              title="Edit task"
            >
              <Icon icon={Pencil} size="sm" />Edit
            </button>
          )}
          {onDuplicate && (
            <button
              onClick={onDuplicate}
              className="inline-flex items-center gap-1 text-xs text-accent-fg"
              title="Duplicate task — opens a pre-filled New Task form"
            >
              <Icon icon={Copy} size="sm" />Duplicate
            </button>
          )}
          <button
            onClick={onDelete}
            className="text-xs text-danger hover:opacity-80"
          >
            Delete
          </button>
        </div>
      </div>

      {editingTask ? (
        <div className="flex flex-col gap-3">
          <Field label="Title">
            <input
              autoFocus
              value={editTitle}
              onChange={(e) => setEditTitle(e.target.value)}
              className={controlClasses}
              placeholder="Task title"
            />
          </Field>
          <Field label="Description">
            <textarea
              value={editDesc}
              onChange={(e) => setEditDesc(e.target.value)}
              rows={4}
              className={`${controlClasses} resize-none`}
              placeholder="Description (optional)"
            />
          </Field>
          <Field label="Type">
            <select value={editType} onChange={(e) => setEditType(e.target.value)} className={controlClasses}>
              {['feature', 'bug', 'chore', 'spike'].map((t) => (
                <option key={t} value={t}>{t}</option>
              ))}
            </select>
          </Field>
          {repos.length > 0 && (
            <Field label="Repo">
              <select value={editRepoId} onChange={(e) => setEditRepoId(e.target.value)} className={controlClasses}>
                {repos.map((r) => (
                  <option key={r.id} value={r.id}>{r.name}</option>
                ))}
              </select>
            </Field>
          )}
          <Field
            label="Max cost (USD)"
            hint="Advisory budget cap checked by the dispatcher before each dispatch. Empty/0 = unlimited."
          >
            <input
              type="number"
              step="0.01"
              min={0}
              value={editMaxCostUsd}
              onChange={(e) => setEditMaxCostUsd(e.target.value)}
              placeholder="Unlimited"
              className={controlClasses}
            />
          </Field>
          <Field label="Priority" hint="Dispatch order only — never preempts an already-running task.">
            <select value={editPriority} onChange={(e) => setEditPriority(Number(e.target.value))} className={controlClasses}>
              {PRIORITY_LEVELS.map((p) => (
                <option key={p.value} value={p.value}>{p.label}</option>
              ))}
            </select>
          </Field>
          {taskSaveError && (
            <p className="text-xs text-danger">{taskSaveError}</p>
          )}
          <div className="flex gap-2 justify-end">
            <Button size="sm" variant="secondary" onClick={onCancelEdit} disabled={taskSaving}>
              Cancel
            </Button>
            <Button size="sm" variant="primary" onClick={onTaskSave} disabled={taskSaving || !editTitle.trim()}>
              {taskSaving ? 'Saving…' : 'Save changes'}
            </Button>
          </div>
        </div>
      ) : (
        <div>
          <h1 className="text-lg font-semibold text-fg leading-snug">{task.title}</h1>
          {task.block_reason ? (
            <Badge tone="danger" className="gap-1.5 px-2 py-1 mt-2">
              <span className="inline-flex items-center gap-1"><Icon icon={Ban} size="sm" />Not dispatching — {blockReasonLabel(task.block_reason)}:</span>
              <span className="text-fg">{task.block_reason.message}</span>
              {task.block_reason.clears_at && isTransientBlockReason(task.block_reason) && (
                <span className="text-danger/80">
                  (clears {formatRelativeCountdown(task.block_reason.clears_at)})
                </span>
              )}
            </Badge>
          ) : (
            task.paused && (
              <Badge tone="warning" className="mt-2">
                <Icon icon={Pause} size="sm" />Paused — agents will not pick up this task
              </Badge>
            )
          )}
          {task.description && (
            isMobile ? (
              <div className="mt-2">
                <button
                  type="button"
                  onClick={() => setDescModalOpen(true)}
                  className="text-left w-full cursor-pointer hover:bg-surface-sunken transition-colors rounded"
                  title="Click to expand"
                >
                  <p className="text-sm text-fg-muted bg-surface-sunken rounded p-2 whitespace-pre-wrap max-h-24 overflow-hidden">
                    {task.description}
                  </p>
                </button>
              </div>
            ) : (
              <p className="text-sm text-fg-muted mt-2">{task.description}</p>
            )
          )}
          {descModalOpen && task.description && (
            <AgentNotesModal title="Description" notes={task.description} onClose={() => setDescModalOpen(false)} />
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
                    className="max-h-48 rounded border border-border cursor-pointer hover:border-fg-subtle transition-colors"
                    onClick={() => window.open(blobUrl, '_blank')}
                    title="Click to open full size"
                  />
                )
              })}
            </div>
          )}
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-x-6 gap-y-2 min-w-0">
        <Row label="Label" wide>
          <Badge tone="neutral">
            {labelDisplayNameByName(task.label, labels)}
          </Badge>
          {moveTargets.length > 0 && (
            <select
              defaultValue=""
              disabled={actionPending}
              onChange={(e) => {
                if (e.target.value) {
                  onMoveLabel(e.target.value)
                  e.target.value = ''
                }
              }}
              className={`${controlClasses} text-xs px-2 py-1 w-auto cursor-pointer`}
            >
              <option value="" disabled>Move to…</option>
              {moveTargets.map((l) => (
                <option key={l.id} value={l.name}>{labelDisplayName(l)}</option>
              ))}
            </select>
          )}
        </Row>
        <Row label="Type"><span className="text-xs text-fg">{task.type}</span></Row>
        <Row label="Priority">
          <span className="text-xs text-fg">
            {priorityLabel(task.priority)}
            {!task.block_reason && task.queue_position != null
              ? ` — #${task.queue_position + 1} in dispatch queue`
              : ''}
          </span>
        </Row>
        {task.block_reason?.code === 'dependency' && (
          <Row label="Blocked" wide>
            <span className="text-xs text-danger">See Dependencies under Details below for the blocking task(s).</span>
          </Row>
        )}
        <Row label="Cost">
          <span className="text-xs text-fg">
            ${cumulativeCost.toFixed(2)}
            {task.max_cost_usd ? ` / $${task.max_cost_usd.toFixed(2)} budget` : ''}
          </span>
        </Row>
        {task.side_effects && task.side_effects.length > 0 && (
          <Row label="Pending work" wide>
            <SideEffectBadge
              effects={task.side_effects}
              onRetry={() => { void api.tasks.retrySideEffects(task.id).catch(() => {}) }}
            />
          </Row>
        )}
        {task.branch && (
          <>
            <Row label="Branch">
              <span className="text-xs font-mono text-fg truncate min-w-0" title={task.branch}>{task.branch}</span>
            </Row>
            <Row label="Git">
              <div className="flex items-center gap-2">
                <GitStateBadge branch={task.branch} gitState={task.git_state} prMergeable={task.pr_mergeable} />
                <span className="text-xs text-fg-muted">{task.git_state || 'branched'}</span>
                {task.git_state === 'pr_open' && task.pr_mergeable === 'conflicting' && (
                  <Badge
                    tone="danger"
                    size="xs"
                    title="GitHub reports this PR no longer merges cleanly into its base branch"
                  >
                    merge conflict
                  </Badge>
                )}
                <button
                  onClick={onSyncGitState}
                  className="inline-flex items-center gap-1 text-xs text-fg-subtle hover:text-fg transition-colors"
                  title="Sync PR state from GitHub"
                >
                  <Icon icon={RefreshCw} size="sm" />Sync
                </button>
              </div>
            </Row>
            <Row label="PR">
              {task.pr_url ? (
                <a
                  href={task.pr_url}
                  target="_blank"
                  rel="noreferrer"
                  className="text-xs text-accent-fg transition-colors truncate"
                >
                  {task.pr_url.replace('https://github.com/', '')} ↗
                </a>
              ) : (
                <button
                  onClick={onCreatePR}
                  disabled={creatingPR}
                  className="text-xs text-accent-fg transition-colors disabled:opacity-50"
                  title="Push the branch and open a GitHub pull request"
                >
                  {creatingPR ? 'Creating PR…' : '+ Create PR'}
                </button>
              )}
            </Row>
            <div className="lg:col-span-2"><GitHubAuthWarning /></div>
          </>
        )}
        {task.agent_notes && (
          <div className="lg:col-span-2">
            <p className="text-xs text-fg-subtle mb-1" style={{ minHeight: '1.5em' }}>Agent Notes</p>
            <button
              type="button"
              onClick={() => setNotesModalOpen(true)}
              className="text-left w-full cursor-pointer hover:bg-surface-sunken transition-colors rounded"
              title="Click to expand"
            >
              <pre className="text-xs text-fg bg-surface-sunken rounded p-2 whitespace-pre-wrap max-h-60 overflow-hidden font-sans">
                {task.agent_notes}
              </pre>
            </button>
          </div>
        )}
        {notesModalOpen && task.agent_notes && (
          <AgentNotesModal notes={task.agent_notes} onClose={() => setNotesModalOpen(false)} />
        )}
        {task.source === 'github' && task.source_ref && (
          <Row label="Source">
            <a
              href={`https://github.com/${task.source_ref.replace('#', '/issues/')}`}
              target="_blank"
              rel="noreferrer"
              className="text-xs text-accent-fg transition-colors"
            >
              {task.source_ref}
            </a>
            {task.source_state === 'gone' && (
              <Badge
                tone="warning"
                title="The source issue closed or no longer matches the repo's issue sync filter. The task was left as-is (or archived/moved, per the repo's setting) rather than silently orphaned."
              >
                <Icon icon={TriangleAlert} size="sm" className="inline -mt-0.5 mr-1" />source issue closed or unlabeled
              </Badge>
            )}
          </Row>
        )}
        {task.matched_rule_id && (
          <Row label="Intake rule">
            <Link
              to="/settings/intake-rules"
              className="text-xs text-accent-fg transition-colors"
              title="This task's label/priority/template/workflow were shaped by this intake rule at creation — see Settings → Intake Rules."
            >
              {task.matched_rule_name || task.matched_rule_id}
            </Link>
          </Row>
        )}
        <Row label="Created">
          <span className="text-xs text-fg-muted">{new Date(task.created_at).toLocaleDateString()}</span>
        </Row>
      </div>
    </>
  )
}
