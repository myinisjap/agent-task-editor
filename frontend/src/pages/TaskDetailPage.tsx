import { useEffect, useRef, useState, useCallback, Fragment } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Task, type AgentRun, type AgentLog, type Workflow, type Repo } from '../api/client'
import { wsClient } from '../api/ws'
import { parseDiff, type FileDiff } from '../lib/parseDiff'
import FileDiffViewer from '../components/diff/FileDiffViewer'
import AgentLogEntry from '../components/board/AgentLogEntry'
import { useAgentsStore } from '../stores/agents'
import GitStateBadge from '../components/board/GitStateBadge'
import GitHubAuthWarning from '../components/shared/GitHubAuthWarning'

type Tab = 'overview' | 'logs' | 'diff'

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [task, setTask] = useState<Task | null>(null)
  const [runs, setRuns] = useState<AgentRun[]>([])
  const [selectedRun, setSelectedRun] = useState<string | null>(null)
  const [debug, setDebug] = useState(false)
  const [logs, setLogs] = useState<AgentLog[]>([])
  const [rejectNote, setRejectNote] = useState('')
  const [actionPending, setActionPending] = useState(false)
  const [diffFiles, setDiffFiles] = useState<FileDiff[]>([])
  const [diffLoading, setDiffLoading] = useState(false)
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
  const logBottomRef = useRef<HTMLDivElement>(null)
  const autoScrollRef = useRef(true)
  const { configs: agentConfigs, fetch: fetchAgents } = useAgentsStore()

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

  // Load logs when selected run changes
  useEffect(() => {
    if (!id || !selectedRun) return
    api.tasks.runLogs(id, selectedRun).then((l) => {
      setLogs(l ?? [])
      autoScrollRef.current = true
    }).catch(() => {})
  }, [id, selectedRun])

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

  // WS subscription
  useEffect(() => {
    if (!id) return
    wsClient.subscribeTask(id)

    const off = wsClient.on((event) => {
      if (event.type === 'agent.log' && event.payload.task_id === id) {
        const entry = event.payload.entry as AgentLog
        if (entry && event.payload.run_id === selectedRun) {
          setLogs((prev) => [...prev, { ...entry, id: entry.id ?? crypto.randomUUID() }])
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
      } else if (event.type === 'task.needs_human' && event.payload.task_id === id) {
        refreshRuns()
        refreshTask()
      }
    })

    return () => {
      off()
      wsClient.unsubscribeTask(id)
    }
  }, [id, selectedRun, refreshTask, refreshRuns])

  // Auto-scroll log pane
  useEffect(() => {
    if (autoScrollRef.current) {
      logBottomRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }, [logs])

  const activeRun = runs.find((r) => r.id === selectedRun)
  const needsHuman = activeRun?.status === 'waiting_human'
  const isRunning = activeRun?.status === 'running'
  const latestRun = runs[0]
  const canRerun = latestRun && (latestRun.status === 'failed' || latestRun.status === 'completed')
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
    if (!id || !rejectNote.trim()) return
    setActionPending(true)
    try {
      const updated = await api.tasks.reject(id, rejectNote)
      setTask(updated)
      setRejectNote('')
      refreshRuns()
    } catch (e: any) {
      alert(e.message)
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
                            .then((s) => setTask((t) => t ? { ...t, git_state: s.git_state } : t))
                            .catch(() => {})
                        }}
                        className="text-xs text-slate-500 hover:text-slate-300 transition-colors"
                        title="Sync PR state from GitHub"
                      >
                        ↻ Sync
                      </button>
                    </div>
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
              <Row label="Created">
                <span className="text-xs text-slate-400">{new Date(task.created_at).toLocaleDateString()}</span>
              </Row>
            </div>

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
                            'text-slate-500'
                          }`}>{run.status}</span>
                        </div>
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
          <div
            className="h-full overflow-y-auto py-3 px-2"
            onScroll={(e) => {
              const el = e.currentTarget
              autoScrollRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
            }}
          >
            <p className="text-slate-500 text-xs mb-3 px-3 font-sans flex items-center gap-2">
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
            {logs.map((log, i) => (
              <AgentLogEntry key={log.id ?? i} log={log} debug={debug} />
            ))}
            <div ref={logBottomRef} />
          </div>
        )}

        {/* Diff tab */}
        {activeTab === 'diff' && (
          <div className="h-full overflow-y-auto p-4">
            <div className="flex items-center justify-between mb-3">
              <p className="text-xs text-slate-500">Changes on this task's branch</p>
              <div className="flex items-center gap-3">
                <button
                  onClick={() => {
                    if (!task?.id) return
                    api.tasks.prUrl(task.id)
                      .then((d) => window.open(d.url, '_blank', 'noopener'))
                      .catch((e) => alert(`Cannot open PR: ${e.message ?? e}`))
                  }}
                  className="px-3 py-1.5 text-xs font-medium rounded bg-indigo-600 hover:bg-indigo-500 text-white"
                >
                  Open PR ↗
                </button>
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
            <FileDiffViewer files={diffFiles} loading={diffLoading} />
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
          <div className="flex gap-3 items-start">
            <textarea
              value={rejectNote}
              onChange={(e) => setRejectNote(e.target.value)}
              placeholder="Rejection note (required to reject)…"
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
                disabled={actionPending || !rejectNote.trim()}
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
