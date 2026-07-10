import { useEffect, useState, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Task, type AgentRun, type TaskLabelHistoryEntry, type Workflow, type Repo } from '../api/client'
import { wsClient } from '../api/ws'
import { useAgentsStore } from '../stores/agents'
import DependenciesPanel from '../components/DependenciesPanel'
import SubtasksPanel from '../components/SubtasksPanel'
import TaskHeader from '../components/task-detail/TaskHeader'
import TaskActions from '../components/task-detail/TaskActions'
import RunHistoryList from '../components/task-detail/RunHistoryList'
import LabelHistoryList from '../components/task-detail/LabelHistoryList'
import RunLogPane from '../components/task-detail/RunLogPane'
import DiffReviewPane from '../components/task-detail/DiffReviewPane'
import { useDiffComments } from '../components/task-detail/useDiffComments'

type Tab = 'overview' | 'logs' | 'diff'

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [task, setTask] = useState<Task | null>(null)
  const [runs, setRuns] = useState<AgentRun[]>([])
  const [labelHistory, setLabelHistory] = useState<TaskLabelHistoryEntry[]>([])
  const [selectedRun, setSelectedRun] = useState<string | null>(null)
  const [rejectNote, setRejectNote] = useState('')
  const [replyText, setReplyText] = useState('')
  const [actionPending, setActionPending] = useState(false)
  const [creatingPR, setCreatingPR] = useState(false)
  const [activeTab, setActiveTab] = useState<Tab>('overview')
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [editingTask, setEditingTask] = useState(false)
  const [editTitle, setEditTitle] = useState('')
  const [editDesc, setEditDesc] = useState('')
  const [editType, setEditType] = useState('')
  const [editRepoId, setEditRepoId] = useState('')
  const [editMaxCostUsd, setEditMaxCostUsd] = useState('')
  const [editPriority, setEditPriority] = useState(0)
  const [repos, setRepos] = useState<Repo[]>([])
  const [taskSaving, setTaskSaving] = useState(false)
  const [taskSaveError, setTaskSaveError] = useState('')
  const { configs: agentConfigs, fetch: fetchAgents } = useAgentsStore()
  const { diffComments, openComments, refreshComments, handleAddComment, handleRemoveComment, handleReopenComment } = useDiffComments(id)

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

  const refreshLabelHistory = useCallback(() => {
    if (!id) return
    api.tasks.listLabelHistory(id).then((h) => setLabelHistory(h ?? [])).catch(() => {})
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
    Promise.all([api.tasks.get(id), api.tasks.runs(id), api.tasks.listLabelHistory(id)])
      .then(([t, r, h]) => {
        setTask(t)
        setRuns(r ?? [])
        setLabelHistory(h ?? [])
        if (r && r.length > 0) setSelectedRun(r[0].id)
      })
  }, [id])

  // Load workflow when task is available
  useEffect(() => {
    if (!task?.workflow_id) return
    api.workflows.get(task.workflow_id).then(setWorkflow).catch(() => {})
  }, [task?.workflow_id])

  // WS subscription — non-log, non-comment events. RunLogPane owns its own
  // wsClient.on() handler for agent.log/agent.log_replay (see useRunLogs);
  // it relies on this effect to keep the task's WS subscription alive via
  // subscribeTask/unsubscribeTask.
  useEffect(() => {
    if (!id) return
    wsClient.subscribeTask(id)

    const off = wsClient.on((event) => {
      if (event.type === 'task.label_changed' && event.payload.task_id === id) {
        setEditingTask(false)
        refreshTask()
        refreshLabelHistory()
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
  }, [id, refreshTask, refreshRuns, refreshComments, refreshLabelHistory])

  const activeRun = runs.find((r) => r.id === selectedRun)
  const needsHuman = activeRun?.status === 'waiting_human'
  const isRunning = activeRun?.status === 'running'
  const latestRun = runs[0]
  const canRerun = !!(latestRun && (latestRun.status === 'failed' || latestRun.status === 'completed' || latestRun.status === 'cancelled'))
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
    setEditMaxCostUsd(task.max_cost_usd ? String(task.max_cost_usd) : '')
    setEditPriority(task.priority ?? 0)
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
        max_cost_usd: editMaxCostUsd.trim() === '' ? 0 : Number(editMaxCostUsd),
        priority: editPriority,
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

  // "Move to…" control on the Overview tab — same call the board's drag/bulk
  // "Move to…" actions use. Gives a touch-friendly path to change a task's
  // label from any device (see issue #147).
  const handleMoveLabel = async (toLabel: string) => {
    if (!id || !toLabel || toLabel === task?.label) return
    setActionPending(true)
    try {
      const updated = await api.tasks.moveLabel(id, toLabel)
      setTask(updated)
      refreshRuns()
      refreshLabelHistory()
    } catch (e: any) {
      alert(e.message ?? String(e))
    } finally {
      setActionPending(false)
    }
  }

  const handleSyncGitState = () => {
    if (!id) return
    api.tasks.githubStatus(id)
      .then((s) => setTask((t) => t ? { ...t, git_state: s.git_state, pr_url: s.pr_url || t.pr_url } : t))
      .catch(() => {})
  }

  const handleDeleteTask = async () => {
    if (!id || !window.confirm('Delete this task?')) return
    await api.tasks.delete(id)
    navigate('/board')
  }

  const handleSelectRun = (runId: string) => {
    setSelectedRun(runId)
    setActiveTab('logs')
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
            <TaskHeader
              task={task}
              repos={repos}
              isStartingColumn={!!isStartingColumn}
              editingTask={editingTask}
              editTitle={editTitle}
              setEditTitle={setEditTitle}
              editDesc={editDesc}
              setEditDesc={setEditDesc}
              editType={editType}
              setEditType={setEditType}
              editRepoId={editRepoId}
              setEditRepoId={setEditRepoId}
              editMaxCostUsd={editMaxCostUsd}
              setEditMaxCostUsd={setEditMaxCostUsd}
              editPriority={editPriority}
              setEditPriority={setEditPriority}
              runs={runs}
              taskSaving={taskSaving}
              taskSaveError={taskSaveError}
              onStartEdit={handleStartEdit}
              onCancelEdit={handleCancelEdit}
              onTaskSave={handleTaskSave}
              onDelete={handleDeleteTask}
              onTogglePause={handleTogglePause}
              actionPending={actionPending}
              onCreatePR={handleCreatePR}
              creatingPR={creatingPR}
              onSyncGitState={handleSyncGitState}
              onBack={() => navigate('/board')}
              labels={workflow?.labels ?? []}
              onMoveLabel={handleMoveLabel}
            />

            <SubtasksPanel
              task={task}
              labels={workflow?.labels ?? []}
              onChanged={() => { if (id) api.tasks.get(id).then(setTask).catch(() => {}) }}
            />

            <DependenciesPanel
              task={task}
              onChanged={() => { if (id) api.tasks.get(id).then(setTask).catch(() => {}) }}
            />

            <RunHistoryList
              runs={runs}
              agentConfigs={agentConfigs}
              selectedRun={selectedRun}
              onSelectRun={handleSelectRun}
              isRunning={!!isRunning}
              canRerun={canRerun}
              onStop={handleStop}
              onRerun={handleRerun}
              actionPending={actionPending}
            />

            <LabelHistoryList history={labelHistory} />
          </div>
        )}

        {/* Logs tab */}
        {activeTab === 'logs' && (
          <RunLogPane taskId={id} runId={selectedRun} isRunning={!!isRunning} />
        )}

        {/* Diff tab */}
        {activeTab === 'diff' && (
          <DiffReviewPane
            taskId={task?.id}
            prUrl={task?.pr_url}
            onCreatePR={handleCreatePR}
            creatingPR={creatingPR}
            diffComments={diffComments}
            onAddComment={handleAddComment}
            onRemoveComment={handleRemoveComment}
            onReopenComment={handleReopenComment}
          />
        )}
      </div>

      {/* Approval panel — shown when agent needs human or task is at a human-gate label */}
      {(needsHuman || isHumanGateLabel) && (
        <TaskActions
          activeRun={activeRun}
          needsHuman={!!needsHuman}
          openCommentsCount={openComments.length}
          replyText={replyText}
          setReplyText={setReplyText}
          onReply={handleReply}
          rejectNote={rejectNote}
          setRejectNote={setRejectNote}
          onReject={handleReject}
          onApprove={handleApprove}
          actionPending={actionPending}
          onJumpToDiffTab={() => setActiveTab('diff')}
        />
      )}
    </div>
  )
}
