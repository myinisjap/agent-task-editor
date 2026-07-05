import { useEffect, useRef, useState, useCallback, Fragment } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Task, type AgentRun, type AgentLog, type Workflow, type Repo } from '../api/client'
import { wsClient } from '../api/ws'
import { parseDiff, type FileDiff } from '../lib/parseDiff'
import { fromApiComment, type DiffComment } from '../lib/diffComments'
import FileDiffViewer from '../components/diff/FileDiffViewer'
import AgentLogEntry from '../components/board/AgentLogEntry'
import { useAgentsStore } from '../stores/agents'
import GitStateBadge from '../components/board/GitStateBadge'
import GitHubAuthWarning from '../components/shared/GitHubAuthWarning'
import DependenciesPanel from '../components/DependenciesPanel'
import SubtasksPanel from '../components/SubtasksPanel'

type Tab = 'overview' | 'logs' | 'diff'

// How many log entries to fetch per page (initial tail + each "load earlier").
const LOG_PAGE_SIZE = 200

// toLog normalises a log-ish payload (from a REST page, the batched replay, or
// a live agent.log event) into an AgentLog. Live events carry the timestamp as
// `at` and may omit the id, so fill both in.
function toLog(e: any): AgentLog {
  return {
    id: e.id ?? crypto.randomUUID(),
    agent_run_id: e.agent_run_id ?? '',
    timestamp: e.timestamp ?? e.at ?? '',
    type: e.type,
    content: e.content,
  }
}

// mergeLogs unions two log lists by id (deduping) and returns them in
// chronological order. Used when combining the initial page with the batched
// replay or with an older "load earlier" page. Ordering is by timestamp, with
// id as a stable tiebreaker for entries that share a timestamp.
function mergeLogs(prev: AgentLog[], incoming: AgentLog[]): AgentLog[] {
  if (incoming.length === 0) return prev
  const byId = new Map<string, AgentLog>()
  for (const l of prev) byId.set(l.id, l)
  for (const l of incoming) byId.set(l.id, l)
  return Array.from(byId.values()).sort((a, b) => {
    const ta = Date.parse(a.timestamp) || 0
    const tb = Date.parse(b.timestamp) || 0
    if (ta !== tb) return ta - tb
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
  })
}

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [task, setTask] = useState<Task | null>(null)
  const [runs, setRuns] = useState<AgentRun[]>([])
  const [selectedRun, setSelectedRun] = useState<string | null>(null)
  const [debug, setDebug] = useState(false)
  const [logs, setLogs] = useState<AgentLog[]>([])
  const [logsHasEarlier, setLogsHasEarlier] = useState(false)
  const [loadingEarlier, setLoadingEarlier] = useState(false)
  const [rejectNote, setRejectNote] = useState('')
  const [replyText, setReplyText] = useState('')
  const [actionPending, setActionPending] = useState(false)
  const [diffFiles, setDiffFiles] = useState<FileDiff[]>([])
  const [diffLoading, setDiffLoading] = useState(false)
  const [diffComments, setDiffComments] = useState<DiffComment[]>([])
  const [creatingPR, setCreatingPR] = useState(false)
  const [activeTab, setActiveTab] = useState<Tab>('overview')
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [editingTask, setEditingTask] = useState(false)
  const [editTitle, setEditTitle] = useState('')
  const [editDesc, setEditDesc] = useState('')
  const [editType, setEditType] = useState('')
  const [editRepoId, setEditRepoId] = useState('')
  const [repos, setRepos] = useState<Repo[]>([])
  const [taskSaving, setTaskSaving] = useState(false)
  const [taskSaveError, setTaskSaveError] = useState('')
  const logScrollRef = useRef<HTMLDivElement>(null)
  const autoScrollRef = useRef(true)
  // When "load earlier" prepends N entries, this holds N so the post-render
  // effect can re-anchor the viewport to the entry that was previously on top
  // (otherwise the virtualized list would jump).
  const anchorIndexRef = useRef<number | null>(null)
  const { configs: agentConfigs, fetch: fetchAgents } = useAgentsStore()

  // Virtualize the log list: only entries near the viewport are mounted, so a
  // run with thousands of entries stays smooth. Rows are variable-height
  // (markdown, expandable tool results), so heights are measured dynamically
  // via measureElement rather than estimated up front.
  const logVirtualizer = useVirtualizer({
    count: logs.length,
    getScrollElement: () => logScrollRef.current,
    estimateSize: () => 44,
    overscan: 12,
  })

  const refreshTask = useCallback(() => {
    if (!id) return
    api.tasks.get(id).then(setTask).catch(() => {})
  }, [id])

  const refreshRuns = useCallback(() => {
    if (!id) return
    api.tasks.runs(id).then((r) => {
      setRuns(r ?? [])
      if (r && r.length > 0) {
        setSelectedRun((prev) => prev ?? r[0].id)
      }
    }).catch(() => {})
  }, [id])

  const refreshComments = useCallback(() => {
    if (!id) return
    api.tasks.reviewComments(id)
      .then((cs) => setDiffComments((cs ?? []).map(fromApiComment)))
      .catch(() => {})
  }, [id])

  // Fetch agent configs for name lookup
  useEffect(() => {
    fetchAgents()
  }, [fetchAgents])

  // Load repos list for the edit form
  useEffect(() => {
    api.repos.list().then(setRepos).catch(() => {})
  }, [])

  // Initial load
  useEffect(() => {
    if (!id) return
    Promise.all([api.tasks.get(id), api.tasks.runs(id)])
      .then(([t, r]) => {
        setTask(t)
        setRuns(r ?? [])
        if (r && r.length > 0) setSelectedRun(r[0].id)
      })
  }, [id])

  // Load the newest page of logs when the selected run changes. Older entries
  // are fetched on demand via "Load earlier".
  useEffect(() => {
    if (!id || !selectedRun) return
    let cancelled = false
    api.tasks.runLogs(id, selectedRun, { limit: LOG_PAGE_SIZE }).then((res) => {
      if (cancelled) return
      setLogs(res.items)
      setLogsHasEarlier(res.hasMore)
      autoScrollRef.current = true
    }).catch(() => {})
    return () => { cancelled = true }
  }, [id, selectedRun])

  // Fetch the page of log entries immediately older than the ones we hold,
  // using the oldest currently-loaded entry's id as the cursor.
  const handleLoadEarlier = useCallback(async () => {
    if (!id || !selectedRun || loadingEarlier) return
    const oldest = logs[0]?.id
    if (!oldest) return
    setLoadingEarlier(true)
    try {
      const res = await api.tasks.runLogs(id, selectedRun, { before: oldest, limit: LOG_PAGE_SIZE })
      autoScrollRef.current = false
      setLogs((prev) => {
        const merged = mergeLogs(prev, res.items)
        // Number of entries added at the top — used to re-anchor the viewport.
        anchorIndexRef.current = merged.length - prev.length
        return merged
      })
      setLogsHasEarlier(res.hasMore)
    } catch {
      // best-effort; leave the button so the user can retry
    } finally {
      setLoadingEarlier(false)
    }
  }, [id, selectedRun, logs, loadingEarlier])

  // Load workflow when task is available
  useEffect(() => {
    if (!task?.workflow_id) return
    api.workflows.get(task.workflow_id).then(setWorkflow).catch(() => {})
  }, [task?.workflow_id])

  // Load diff when task is available
  useEffect(() => {
    if (!task?.id) return
    setDiffLoading(true)
    api.tasks.diff(task.id)
      .then((d) => setDiffFiles(parseDiff(d.diff)))
      .catch(() => setDiffFiles([]))
      .finally(() => setDiffLoading(false))
  }, [task?.id])

  // Load persisted review comments (open + resolved) when the task changes.
  useEffect(() => {
    refreshComments()
  }, [refreshComments])

  // WS subscription
  useEffect(() => {
    if (!id) return
    wsClient.subscribeTask(id)

    const off = wsClient.on((event) => {
      if (event.type === 'agent.log' && event.payload.task_id === id) {
        const entry = event.payload.entry as AgentLog
        if (entry && event.payload.run_id === selectedRun) {
          const l = toLog(entry)
          setLogs((prev) => (prev.some((x) => x.id === l.id) ? prev : [...prev, l]))
        }
      } else if (event.type === 'agent.log_replay' && event.payload.task_id === id) {
        // Batched tail sent on subscribe. Merge (dedupe) with whatever the REST
        // page already loaded, and surface "load earlier" if more history exists.
        if (event.payload.run_id === selectedRun) {
          const entries = (event.payload.entries ?? []).map(toLog)
          setLogs((prev) => mergeLogs(prev, entries))
          if (event.payload.has_more) setLogsHasEarlier(true)
        }
      } else if (event.type === 'task.label_changed' && event.payload.task_id === id) {
        setEditingTask(false)
        refreshTask()
      } else if (event.type === 'task.agent_started' && event.payload.task_id === id) {
        refreshRuns()
        refreshTask()
      } else if (event.type === 'task.agent_done' && event.payload.task_id === id) {
        setRuns((prev) =>
          prev.map((r) =>
            r.id === event.payload.run_id ? { ...r, status: event.payload.status } : r
          )
        )
        refreshTask()
        refreshComments()
      } else if (event.type === 'task.review_comments_changed' && event.payload.task_id === id) {
        refreshComments()
      } else if (event.type === 'task.needs_human' && event.payload.task_id === id) {
        refreshRuns()
        refreshTask()
      } else if (event.type === 'task.git_state_changed' && event.payload.task_id === id) {
        setTask((t) => t ? { ...t, git_state: event.payload.git_state, pr_url: event.payload.pr_url || t.pr_url } : t)
      }
    })

    return () => {
      off()
      wsClient.unsubscribeTask(id)
    }
  }, [id, selectedRun, refreshTask, refreshRuns, refreshComments])

  // Keep the log viewport anchored as entries change. After "load earlier"
  // prepends entries, re-anchor to the entry that was previously on top so the
  // view doesn't jump. Otherwise, when following the tail, scroll to the newest.
  useEffect(() => {
    if (anchorIndexRef.current != null) {
      const idx = anchorIndexRef.current
      anchorIndexRef.current = null
      if (idx > 0) logVirtualizer.scrollToIndex(idx, { align: 'start' })
      return
    }
    if (autoScrollRef.current && logs.length > 0) {
      logVirtualizer.scrollToIndex(logs.length - 1, { align: 'end' })
    }
  }, [logs, logVirtualizer])

  const activeRun = runs.find((r) => r.id === selectedRun)
  const needsHuman = activeRun?.status === 'waiting_human'
  const isRunning = activeRun?.status === 'running'
  const latestRun = runs[0]
  const canRerun = latestRun && (latestRun.status === 'failed' || latestRun.status === 'completed' || latestRun.status === 'cancelled')
  const isHumanGateLabel = task
    ? workflow?.transitions?.some((t) => t.from_label === task.label && t.trigger_type === 'human') ?? false
    : false

  const isStartingColumn = task && workflow
    ? [...(workflow.labels ?? [])].sort((a, b) => a.sort_order - b.sort_order)[0]?.name === task.label
    : false

  const handleStartEdit = () => {
    if (!task) return
    setEditTitle(task.title)
    setEditDesc(task.description ?? '')
    setEditType(task.type)
    setEditRepoId(task.repo_id)
    setTaskSaveError('')
    setEditingTask(true)
  }

  const handleCancelEdit = () => {
    setEditingTask(false)
    setTaskSaveError('')
  }

  const handleTaskSave = async () => {
    if (!id || !editTitle.trim()) return
    setTaskSaving(true)
    setTaskSaveError('')
    try {
      const updated = await api.tasks.update(id, {
        title: editTitle.trim(),
        description: editDesc.trim(),
        type: editType,
        repo_id: editRepoId,
      })
      setTask(updated)
      setEditingTask(false)
    } catch (e: any) {
      setTaskSaveError(e.message ?? String(e))
    } finally {
      setTaskSaving(false)
    }
  }

  const handleRerun = async () => {
    if (!id) return
    setActionPending(true)
    try {
      await api.tasks.rerun(id)
      refreshRuns()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setActionPending(false)
    }
  }

  // handleStop requests cancellation of the running agent run. The pool marks
  // the run "cancelled" and pauses the task; the resulting task.agent_done WS
  // event refreshes the run list and task, so we just fire the request.
  const handleStop = async () => {
    if (!id || !selectedRun) return
    if (!confirm('Stop this agent run? The task will be paused so it is not immediately re-dispatched.')) return
    setActionPending(true)
    try {
      await api.tasks.cancelRun(id, selectedRun)
      refreshRuns()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setActionPending(false)
    }
  }

  const openComments = diffComments.filter((c) => c.status !== 'resolved')

  // Reply to a waiting_human run: answers the agent's question with text and
  // starts a continuation run (resuming the provider session where supported).
  // The task stays on its label — this is a conversation, not a transition.
  const handleReply = async () => {
    if (!id || !selectedRun || !replyText.trim()) return
    setActionPending(true)
    try {
      const res = await api.tasks.replyRun(id, selectedRun, replyText.trim())
      setReplyText('')
      // Follow the continuation run so the user watches the agent pick the reply up.
      setSelectedRun(res.run_id)
      refreshRuns()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setActionPending(false)
    }
  }

  const handleApprove = async () => {
    if (!id) return
    setActionPending(true)
    try {
      const updated = await api.tasks.approve(id)
      setTask(updated)
      refreshRuns()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setActionPending(false)
    }
  }

  const handleReject = async () => {
    // Open review comments are persisted server-side and injected into the
    // next run's prompt directly — only the free-text note travels here.
    if (!id || (!rejectNote.trim() && openComments.length === 0)) return
    setActionPending(true)
    try {
      const updated = await api.tasks.reject(id, rejectNote.trim())
      setTask(updated)
      setRejectNote('')
      refreshRuns()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setActionPending(false)
    }
  }

  const handleAddComment = async (draft: DiffComment) => {
    if (!id) return
    // Optimistic insert with the draft's temporary id, replaced (or rolled
    // back) once the API responds.
    setDiffComments((prev) => [...prev, draft])
    try {
      const created = await api.tasks.addReviewComment(id, {
        file_path: draft.filePath,
        side: draft.side,
        start_line: draft.startLine,
        end_line: draft.endLine,
        quoted_text: draft.quotedText,
        body: draft.comment,
      })
      setDiffComments((prev) => prev.map((c) => (c.id === draft.id ? fromApiComment(created) : c)))
    } catch (e: any) {
      setDiffComments((prev) => prev.filter((c) => c.id !== draft.id))
      alert(`Failed to save comment: ${e.message ?? e}`)
    }
  }

  const handleRemoveComment = async (commentId: string) => {
    if (!id) return
    try {
      await api.tasks.deleteReviewComment(id, commentId)
      setDiffComments((prev) => prev.filter((c) => c.id !== commentId))
    } catch (e: any) {
      alert(`Failed to delete comment: ${e.message ?? e}`)
    }
  }

  const handleReopenComment = async (commentId: string) => {
    if (!id) return
    try {
      const updated = await api.tasks.updateReviewComment(id, commentId, { status: 'open' })
      setDiffComments((prev) => prev.map((c) => (c.id === commentId ? fromApiComment(updated) : c)))
    } catch (e: any) {
      alert(`Failed to reopen comment: ${e.message ?? e}`)
    }
  }

  // Pushes the branch and opens a GitHub PR in one click (idempotent — returns
  // the existing PR if one already exists), then opens it in a new tab.
  const handleCreatePR = async () => {
    if (!id) return
    setCreatingPR(true)
    try {
      const res = await api.tasks.createPR(id)
      setTask((t) => t ? { ...t, pr_url: res.pr_url, git_state: res.git_state } : t)
      if (res.pr_url) window.open(res.pr_url, '_blank', 'noopener')
    } catch (e: any) {
      alert(`Cannot create PR: ${e.message ?? e}`)
    } finally {
      setCreatingPR(false)
    }
  }

  const handleTogglePause = async () => {
    if (!id || !task) return
    setActionPending(true)
    try {
      const updated = await api.tasks.setPaused(id, !task.paused)
      setTask(updated)
    } catch (e: any) {
      alert(e.message ?? String(e))
    } finally {
      setActionPending(false)
    }
  }

  if (!task) return <div className="p-6 text-slate-400">Loading…</div>

  const tabs: { id: Tab; label: string }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'logs', label: 'Logs' },
    { id: 'diff', label: 'Diff' },
  ]

  return (
    <div className="flex h-full overflow-hidden flex-col">
      {/* Tab bar */}
      <div className="shrink-0 flex items-center border-b border-slate-800 px-4 pt-3 w-full overflow-x-hidden">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setActiveTab(t.id)}
            className={`flex-grow min-w-[100px] px-3 py-1.5 text-xs font-medium rounded-t transition-colors ${
              activeTab === t.id
                ? 'bg-slate-800 text-slate-100 border-b-2 border-slate-400'
                : 'text-slate-500 hover:text-slate-300'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div className="flex-1 overflow-hidden">
        {/* Overview tab */}
        {activeTab === 'overview' && (
          <div className="h-full overflow-y-auto p-5 flex flex-col gap-4">
            <div className="flex items-center justify-between">
              <button
                onClick={() => navigate('/board')}
                className="text-xs text-slate-500 hover:text-slate-300 text-left"
              >
                ← Board
              </button>
              <div className="flex items-center gap-3">
                <button
                  onClick={handleTogglePause}
                  disabled={actionPending}
                  className={`text-xs disabled:opacity-50 ${task.paused ? 'text-emerald-400 hover:text-emerald-300' : 'text-amber-400 hover:text-amber-300'}`}
                  title={task.paused ? 'Resume task' : 'Pause task'}
                >
                  {task.paused ? '▶ Resume' : '⏸ Pause'}
                </button>
                {isStartingColumn && !editingTask && (
                  <button
                    onClick={handleStartEdit}
                    className="text-xs text-indigo-400 hover:text-indigo-300"
                    title="Edit task"
                  >
                    ✎ Edit
                  </button>
                )}
                <button
                  onClick={async () => {
                    if (!id || !window.confirm('Delete this task?')) return
                    await api.tasks.delete(id)
                    navigate('/board')
                  }}
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
                {taskSaveError && (
                  <p className="text-xs text-red-400">{taskSaveError}</p>
                )}
                <div className="flex gap-2 justify-end">
                  <button
                    onClick={handleCancelEdit}
                    disabled={taskSaving}
                    className="px-3 py-1.5 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-300 disabled:opacity-50 transition-colors"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={handleTaskSave}
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
                  <div className="flex flex-wrap gap-2 mt-3">
                    {task.attachments.map((rel) => (
                      <img
                        key={rel}
                        src={`/api/v1/uploads/${rel}`}
                        alt="attachment"
                        className="max-h-48 rounded border border-slate-700 cursor-pointer hover:border-slate-500 transition-colors"
                        onClick={() => window.open(`/api/v1/uploads/${rel}`, '_blank')}
                        title="Click to open full size"
                      />
                    ))}
                  </div>
                )}
              </div>
            )}

            <div className="flex flex-col gap-2">
              <Row label="Label">
                <span className="text-xs px-2 py-0.5 rounded-full font-medium text-white bg-slate-600">
                  {task.label}
                </span>
              </Row>
              <Row label="Type"><span className="text-xs text-slate-300">{task.type}</span></Row>
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
                        onClick={() => {
                          if (!id) return
                          api.tasks.githubStatus(id)
                            .then((s) => setTask((t) => t ? { ...t, git_state: s.git_state, pr_url: s.pr_url || t.pr_url } : t))
                            .catch(() => {})
                        }}
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
                        onClick={handleCreatePR}
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

            <SubtasksPanel
              task={task}
              labels={workflow?.labels ?? []}
              onChanged={() => { if (id) api.tasks.get(id).then(setTask).catch(() => {}) }}
            />

            <DependenciesPanel
              task={task}
              onChanged={() => { if (id) api.tasks.get(id).then(setTask).catch(() => {}) }}
            />

            {runs.length > 0 && (
              <div>
                <p className="text-xs text-slate-500 mb-2">Agent runs</p>
                <div className="flex flex-col gap-1">
                  {runs.map((run) => (
                    <Fragment key={run.id}>
                      <button
                        onClick={() => { setSelectedRun(run.id); autoScrollRef.current = false; setActiveTab('logs') }}
                        className={`text-left text-xs px-2 py-1.5 rounded ${
                          selectedRun === run.id
                            ? 'bg-slate-700 text-slate-100'
                            : 'text-slate-400 hover:bg-slate-800'
                        }`}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-mono truncate">{run.id.slice(0, 8)}</span>
                          {agentConfigs.find((a) => a.id === run.agent_config_id)?.name && (
                            <span className="text-slate-400 truncate text-xs">
                              {agentConfigs.find((a) => a.id === run.agent_config_id)?.name}
                            </span>
                          )}
                          <span className={`shrink-0 ${
                            run.status === 'completed'     ? 'text-emerald-400' :
                            run.status === 'running'       ? 'text-yellow-400 animate-pulse' :
                            run.status === 'failed'        ? 'text-red-400' :
                            run.status === 'waiting_human' ? 'text-pink-400' :
                            run.status === 'cancelled'     ? 'text-orange-400' :
                            'text-slate-500'
                          }`}>{run.status}</span>
                        </div>
                        {((run.cost_usd ?? 0) > 0 || (run.input_tokens ?? 0) > 0 || (run.output_tokens ?? 0) > 0) && (
                          <div className="text-slate-500 text-[11px] mt-0.5">
                            ${(run.cost_usd ?? 0).toFixed(4)} · {formatTokenCount(run.input_tokens ?? 0)}/{formatTokenCount(run.output_tokens ?? 0)} tok
                          </div>
                        )}
                      </button>
                      {run.stored_info && (
                        <StoredInfoPanel runId={run.id} info={run.stored_info} />
                      )}
                      {run.notes && (
                        <NotesPanel notes={run.notes} />
                      )}
                    </Fragment>
                  ))}
                </div>
              </div>
            )}

            {isRunning && (
              <button
                onClick={handleStop}
                disabled={actionPending}
                className="w-full px-3 py-1.5 text-xs font-medium rounded bg-red-900/60 hover:bg-red-800 text-red-200 disabled:opacity-50"
                title="Stop the running agent and pause the task"
              >
                ■ Stop run
              </button>
            )}

            {canRerun && (
              <button
                onClick={handleRerun}
                disabled={actionPending}
                className="w-full px-3 py-1.5 text-xs font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-200 disabled:opacity-50"
              >
                ↻ Re-run
              </button>
            )}
          </div>
        )}

        {/* Logs tab */}
        {activeTab === 'logs' && (
          <div className="h-full flex flex-col">
            <p className="text-slate-500 text-xs py-3 px-3 font-sans flex items-center gap-2 shrink-0">
              {isRunning && <span className="inline-block w-2 h-2 rounded-full bg-yellow-400 animate-pulse" />}
              {selectedRun ? `Run ${selectedRun.slice(0, 8)}` : 'No agent runs yet'}
              {logs.length > 0 && <span className="text-slate-700">· {logs.length} events</span>}
              <label className="ml-2 flex items-center gap-1 cursor-pointer">
                <input
                  type="checkbox"
                  className="rounded border-slate-700 bg-slate-800 text-indigo-600 focus:ring-indigo-500"
                  checked={debug}
                  onChange={(e) => setDebug(e.target.checked)}
                />
                <span className="text-slate-400">Debug</span>
              </label>
            </p>
            {logs.length === 0 && selectedRun && (
              <p className="text-slate-600 text-xs px-3">No log entries</p>
            )}
            {logsHasEarlier && (
              <div className="flex justify-center pb-2 shrink-0">
                <button
                  onClick={handleLoadEarlier}
                  disabled={loadingEarlier}
                  className="text-xs px-3 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 disabled:opacity-50"
                >
                  {loadingEarlier ? 'Loading…' : '↑ Load earlier'}
                </button>
              </div>
            )}
            {/* Virtualized log list — only rows near the viewport are mounted. */}
            <div
              ref={logScrollRef}
              className="flex-1 overflow-y-auto px-2"
              onScroll={(e) => {
                const el = e.currentTarget
                autoScrollRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
              }}
            >
              <div style={{ height: logVirtualizer.getTotalSize(), position: 'relative', width: '100%' }}>
                {logVirtualizer.getVirtualItems().map((vi) => (
                  <div
                    key={logs[vi.index].id ?? vi.index}
                    data-index={vi.index}
                    ref={logVirtualizer.measureElement}
                    style={{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${vi.start}px)` }}
                  >
                    <AgentLogEntry log={logs[vi.index]} debug={debug} />
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}

        {/* Diff tab */}
        {activeTab === 'diff' && (
          <div className="h-full overflow-y-auto p-4">
            <div className="flex items-center justify-between mb-3">
              <p className="text-xs text-slate-500">Changes on this task's branch</p>
              <div className="flex items-center gap-3">
                {task?.pr_url ? (
                  <a
                    href={task.pr_url}
                    target="_blank"
                    rel="noreferrer"
                    className="px-3 py-1.5 text-xs font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white"
                  >
                    View PR ↗
                  </a>
                ) : (
                  <button
                    onClick={handleCreatePR}
                    disabled={creatingPR}
                    className="px-3 py-1.5 text-xs font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white disabled:opacity-50"
                    title="Push the branch and open a GitHub pull request"
                  >
                    {creatingPR ? 'Creating PR…' : 'Create PR'}
                  </button>
                )}
                <button
                  onClick={() => {
                    if (!task?.id) return
                    setDiffLoading(true)
                    api.tasks.diff(task.id)
                      .then((d) => setDiffFiles(parseDiff(d.diff)))
                      .catch(() => setDiffFiles([]))
                      .finally(() => setDiffLoading(false))
                  }}
                  className="px-3 py-1.5 text-xs font-medium rounded bg-slate-700 hover:bg-slate-600 text-slate-200"
                >
                  ↻ Refresh
                </button>
              </div>
            </div>
            <FileDiffViewer
              files={diffFiles}
              loading={diffLoading}
              comments={diffComments}
              onAddComment={handleAddComment}
              onRemoveComment={handleRemoveComment}
              onReopenComment={handleReopenComment}
            />
          </div>
        )}
      </div>

      {/* Approval panel — shown when agent needs human or task is at a human-gate label */}
      {(needsHuman || isHumanGateLabel) && (
        <div className="shrink-0 border-t border-slate-700 bg-slate-900 p-4">
          <p className="text-sm font-medium text-slate-200 mb-3">
            {needsHuman ? 'Agent is waiting for your input' : 'Human review required'}
          </p>
          {activeRun?.feedback && (
            <p className="text-xs text-slate-400 mb-3 bg-slate-800 rounded p-2">
              {activeRun.feedback}
            </p>
          )}
          {openComments.length > 0 && (
            <p className="text-xs text-amber-400 mb-2">
              💬 {openComments.length} open diff comment{openComments.length !== 1 ? 's' : ''} — the next agent run will see and address them
              {' '}
              <button
                onClick={() => setActiveTab('diff')}
                className="underline hover:text-amber-300"
              >
                review in Diff tab
              </button>
            </p>
          )}
          {needsHuman && (
            <div className="flex gap-3 items-start mb-3">
              <textarea
                value={replyText}
                onChange={(e) => setReplyText(e.target.value)}
                placeholder="Reply to the agent — answer its question and it continues in the same session, without moving the task…"
                rows={2}
                className="flex-1 text-xs bg-slate-800 border border-slate-700 rounded px-3 py-2 text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-slate-500"
              />
              <button
                onClick={handleReply}
                disabled={actionPending || !replyText.trim()}
                className="px-4 py-1.5 text-xs font-medium rounded bg-sky-600 hover:bg-sky-500 text-white disabled:opacity-50"
              >
                Reply & Continue
              </button>
            </div>
          )}
          <div className="flex gap-3 items-start">
            <textarea
              value={rejectNote}
              onChange={(e) => setRejectNote(e.target.value)}
              placeholder={
                openComments.length > 0
                  ? 'Additional rejection note (optional — open inline comments reach the agent automatically)…'
                  : 'Rejection note (required to reject)…'
              }
              rows={2}
              className="flex-1 text-xs bg-slate-800 border border-slate-700 rounded px-3 py-2 text-slate-200 placeholder-slate-500 resize-none focus:outline-none focus:border-slate-500"
            />
            <div className="flex flex-col gap-2">
              <button
                onClick={handleApprove}
                disabled={actionPending}
                className="px-4 py-1.5 text-xs font-medium rounded bg-emerald-600 hover:bg-emerald-500 text-white disabled:opacity-50"
              >
                Approve
              </button>
              <button
                onClick={handleReject}
                disabled={actionPending || (!rejectNote.trim() && openComments.length === 0)}
                className="px-4 py-1.5 text-xs font-medium rounded bg-red-700 hover:bg-red-600 text-white disabled:opacity-50"
              >
                Reject
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

// formatTokenCount renders a token count compactly (e.g. "1.2k") for the
// per-run cost/usage badge, falling back to the plain number for small counts.
function formatTokenCount(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`
  return `${n}`
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2">
      <span className="text-xs text-slate-500 w-16">{label}</span>
      {children}
    </div>
  )
}

function NotesPanel({ notes }: { notes: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="ml-2 border-l border-slate-700 pl-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-xs text-slate-500 hover:text-slate-300 w-full text-left py-0.5"
      >
        <span>{open ? '▾' : '▸'}</span>
        <span>agent notes</span>
      </button>
      {open && (
        <pre className="text-xs text-slate-300 bg-slate-800 rounded p-2 mt-1 whitespace-pre-wrap max-h-48 overflow-y-auto font-sans">
          {notes}
        </pre>
      )}
    </div>
  )
}

function StoredInfoPanel({ runId, info }: { runId: string; info: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="ml-2 border-l border-slate-700 pl-2">
      <button
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-1 text-xs text-slate-500 hover:text-slate-300 w-full text-left py-0.5"
      >
        <span>{open ? '▾' : '▸'}</span>
        <span className="font-mono text-slate-600">{runId.slice(0, 8)}</span>
        <span>stored info</span>
      </button>
      {open && (
        <pre className="text-xs text-slate-300 bg-slate-800 rounded p-2 mt-1 whitespace-pre-wrap max-h-48 overflow-y-auto font-sans">
          {info}
        </pre>
      )}
    </div>
  )
}
