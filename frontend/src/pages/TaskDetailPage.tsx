import { useEffect, useState, useCallback, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Task, type AgentRun, type TaskLabelHistoryEntry, type TaskSourceComment, type Workflow, type Repo } from '../api/client'
import { wsClient } from '../api/ws'
import { useAgentsStore } from '../stores/agents'
import DependenciesPanel from '../components/DependenciesPanel'
import SubtasksPanel from '../components/SubtasksPanel'
import TaskHeader from '../components/task-detail/TaskHeader'
import TaskActions from '../components/task-detail/TaskActions'
import RunHistoryList from '../components/task-detail/RunHistoryList'
import LabelHistoryList from '../components/task-detail/LabelHistoryList'
import SourceCommentsList from '../components/task-detail/SourceCommentsList'
import RunLogPane from '../components/task-detail/RunLogPane'
import DiffReviewPane from '../components/task-detail/DiffReviewPane'
import { useDiffComments } from '../components/task-detail/useDiffComments'
import NewTaskModal from '../components/board/NewTaskModal'
import HelpModal from '../components/shared/HelpModal'
import HelpButton from '../components/shared/HelpButton'
import { TaskDetailHelp } from '../components/shared/pageHelp'

type Tab = 'overview' | 'logs' | 'diff'

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [task, setTask] = useState<Task | null>(null)
  const [runs, setRuns] = useState<AgentRun[]>([])
  const [labelHistory, setLabelHistory] = useState<TaskLabelHistoryEntry[]>([])
  const [sourceComments, setSourceComments] = useState<TaskSourceComment[]>([])
  const [selectedRun, setSelectedRun] = useState<string | null>(null)
  const [rejectNote, setRejectNote] = useState('')
  const [replyText, setReplyText] = useState('')
  const [actionPending, setActionPending] = useState(false)
  const [creatingPR, setCreatingPR] = useState(false)
  const [activeTab, setActiveTab] = useState<Tab>('overview')
  const [showHelp, setShowHelp] = useState(false)
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
  const [duplicating, setDuplicating] = useState(false)
  // Set once a task.cost_warning event arrives for this task (crossed the
  // early-warning threshold — see GET/PUT /settings/cost-warning). Cleared
  // on label change (new lifecycle stage) so a stale warning from a prior
  // stage/run doesn't linger indefinitely.
  const [costWarning, setCostWarning] = useState<{ spentUsd: number; budgetUsd: number } | null>(null)
  const { configs: agentConfigs, fetch: fetchAgents } = useAgentsStore()
  const { diffComments, openComments, refreshComments, handleAddComment, handleRemoveComment, handleReopenComment } = useDiffComments(id)

  // Tracks the task id this page instance currently "owns". React Router
  // reuses this component instance across `/tasks/:id` navigations, so any
  // refresh callback captured for task A must not apply its response after
  // the user has navigated to task B. Assigned synchronously during render
  // (not in an effect) so there is no window where an in-flight response
  // could land and read a stale value before an effect has run.
  const currentIdRef = useRef(id)
  currentIdRef.current = id

  const refreshTask = useCallback(() => {
    if (!id) return
    const taskId = id
    api.tasks.get(taskId).then((t) => {
      if (currentIdRef.current !== taskId) return
      setTask(t)
    }).catch(() => {})
  }, [id])

  const refreshRuns = useCallback(() => {
    if (!id) return
    const taskId = id
    api.tasks.runs(taskId).then(({ items: r }) => {
      if (currentIdRef.current !== taskId) return
      setRuns(r ?? [])
      if (r && r.length > 0) {
        setSelectedRun((prev) => prev ?? r[0].id)
      }
    }).catch(() => {})
  }, [id])

  const refreshLabelHistory = useCallback(() => {
    if (!id) return
    const taskId = id
    api.tasks.listLabelHistory(taskId).then((h) => {
      if (currentIdRef.current !== taskId) return
      setLabelHistory(h ?? [])
    }).catch(() => {})
  }, [id])

  const refreshSourceComments = useCallback(() => {
    if (!id) return
    const taskId = id
    api.tasks.sourceComments(taskId).then((c) => {
      if (currentIdRef.current !== taskId) return
      setSourceComments(c ?? [])
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

  // Reset per-task state when the task id changes, so a navigation never
  // shows the previous task's header/runs/history while the new task's
  // fetches are in flight. Must run before the initial-load effect below on
  // an id change, which source order guarantees (effects run in declaration
  // order). `activeTab` is intentionally left alone: the panes it can show
  // (RunLogPane/DiffReviewPane) are driven by `id`/`selectedRun`, both reset
  // here, so keeping the tab selection is safe and less surprising than
  // bouncing the user back to "overview" on every navigation.
  useEffect(() => {
    setTask(null)
    setRuns([])
    setSelectedRun(null)
    setLabelHistory([])
    setSourceComments([])
    setCostWarning(null)
    setEditingTask(false)
    setTaskSaveError('')
    setWorkflow(null)
  }, [id])

  // Initial load
  useEffect(() => {
    if (!id) return
    const taskId = id
    let cancelled = false
    Promise.all([api.tasks.get(taskId), api.tasks.runs(taskId), api.tasks.listLabelHistory(taskId), api.tasks.sourceComments(taskId)])
      .then(([t, runsPage, h, c]) => {
        if (cancelled || currentIdRef.current !== taskId) return
        const r = runsPage.items
        setTask(t)
        setRuns(r ?? [])
        setLabelHistory(h ?? [])
        setSourceComments(c ?? [])
        if (r && r.length > 0) setSelectedRun(r[0].id)
      })
      .catch(() => {})
    return () => { cancelled = true }
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
        setCostWarning(null)
        refreshTask()
        refreshLabelHistory()
      } else if (event.type === 'task.cost_warning' && event.payload.task_id === id) {
        setCostWarning({ spentUsd: event.payload.spent_usd, budgetUsd: event.payload.budget_usd })
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
      } else if (event.type === 'task.pr_mergeable_changed' && event.payload.task_id === id) {
        setTask((t) => t ? { ...t, pr_mergeable: event.payload.pr_mergeable } : t)
      } else if (event.type === 'task.updated' && event.payload.id === id) {
        // Covers the importer's reconciliation sweep (source_state flips to
        // 'gone'/back, or a field-drift update) — refetch for full data since
        // the payload here only carries {id}.
        refreshTask()
      } else if (event.type === 'task.source_comment_added' && event.payload.task_id === id) {
        refreshSourceComments()
      }
    })

    return () => {
      off()
      wsClient.unsubscribeTask(id)
    }
  }, [id, refreshTask, refreshRuns, refreshComments, refreshLabelHistory, refreshSourceComments])

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
      <div className="shrink-0 flex items-center gap-2 border-b border-slate-800 px-4 pt-3 w-full overflow-x-hidden">
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
        <HelpButton onClick={() => setShowHelp(true)} title="About this task" />
      </div>

      {showHelp && (
        <HelpModal title="About Task Detail" onClose={() => setShowHelp(false)}>
          <TaskDetailHelp />
        </HelpModal>
      )}

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
              onDuplicate={() => setDuplicating(true)}
            />

            {duplicating && task && (
              <NewTaskModal source={task} onClose={() => setDuplicating(false)} />
            )}

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

            <SourceCommentsList comments={sourceComments} />
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

      {/* Cost early-warning banner — crossed the configurable warn_ratio threshold
          (default 80%) of the effective cost budget, but not yet exhausted. See
          GET/PUT /settings/cost-warning and docs/agents.md#cost-budgets. */}
      {costWarning && (
        <div className="mb-4 px-4 py-3 rounded-lg bg-amber-900/40 border border-amber-700 text-amber-300 text-sm">
          💰 Cost warning: this task has spent ${costWarning.spentUsd.toFixed(2)} of its ${costWarning.budgetUsd.toFixed(2)} budget.
        </div>
      )}

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
