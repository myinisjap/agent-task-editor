import { CircleDollarSign } from 'lucide-react'
import Icon from '../components/ui/Icon'
import { useEffect, useState, useCallback, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { api, type Task, type AgentRun, type TaskLabelHistoryEntry, type TaskSourceComment, type TaskHumanDecision, type Workflow, type Repo } from '../api/client'
import { wsClient } from '../api/ws'
import { useAgentsStore } from '../stores/agents'
import { useProviderConfigsStore } from '../stores/providerConfigs'
import DependenciesPanel from '../components/DependenciesPanel'
import SubtasksPanel from '../components/SubtasksPanel'
import TaskHeader from '../components/task-detail/TaskHeader'
import { humanActionsFor, isHumanGateLabel as isStrictHumanGate, humanPathTargetLabel } from '../lib/humanGate'
import { useIsPhone, useIsWideDesktop } from '../lib/useMediaQuery'
import DetailsDisclosure from '../components/task-detail/DetailsDisclosure'
import AlwaysAskSwitch from '../components/task-detail/AlwaysAskSwitch'
import ReviewLayout from '../components/task-detail/ReviewLayout'
import TaskActions from '../components/task-detail/TaskActions'
import RunHistoryList from '../components/task-detail/RunHistoryList'
import ForceMoveControl from '../components/task-detail/ForceMoveControl'
import HumanDecisionsList from '../components/task-detail/HumanDecisionsList'
import RememberLessonModal from '../components/lessons/RememberLessonModal'
import Toast from '../components/shared/Toast'
import ConfirmDialog from '../components/shared/ConfirmDialog'
import { showError, showToast, showUndoToast } from '../stores/toast'
import { useNeedsMeStore } from '../stores/needsMe'
import { errMessage } from '../lib/errMessage'
import { canHumanMove } from '../lib/humanMoves'
import ShouldHaveAskedDialog from '../components/shared/ShouldHaveAskedDialog'
import { lastGateExitByPolicy } from '../lib/shouldHaveAsked'
import LabelHistoryList from '../components/task-detail/LabelHistoryList'
import SourceUpdateNotice from '../components/task-detail/SourceUpdateNotice'
import SourceCommentsList from '../components/task-detail/SourceCommentsList'
import RunLogPane from '../components/task-detail/RunLogPane'
import DiffReviewPane from '../components/task-detail/DiffReviewPane'
import ReviewBriefCard from '../components/task-detail/ReviewBriefCard'
import { useDiffComments } from '../components/task-detail/useDiffComments'
import NewTaskModal from '../components/board/NewTaskModal'
import HelpModal from '../components/shared/HelpModal'
import HelpButton from '../components/shared/HelpButton'
import { TaskDetailHelp } from '../components/shared/pageHelp'
import { useCommandPaletteStore } from '../stores/commandPalette'
import { buildTaskCommands, type TaskCommandHandlers } from '../lib/taskCommands'

type Tab = 'overview' | 'logs' | 'diff'

export default function TaskDetailPage() {
  const { id } = useParams<{ id: string }>()
  const autoPending = useNeedsMeStore((s) => (id ? s.autoApprovingByTask[id] : undefined))
  const navigate = useNavigate()
  const [task, setTask] = useState<Task | null>(null)
  const [runs, setRuns] = useState<AgentRun[]>([])
  const [labelHistory, setLabelHistory] = useState<TaskLabelHistoryEntry[]>([])
  const [sourceComments, setSourceComments] = useState<TaskSourceComment[]>([])
  const [humanDecisions, setHumanDecisions] = useState<TaskHumanDecision[]>([])
  const [rememberText, setRememberText] = useState<string | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [selectedRun, setSelectedRun] = useState<string | null>(null)
  const [rejectNote, setRejectNote] = useState('')
  const [replyText, setReplyText] = useState('')
  const [actionPending, setActionPending] = useState(false)
  // Run whose escalation the user already retried; hides its banner until the
  // re-dispatched run shows up and is selected.
  const [retriedRunId, setRetriedRunId] = useState<string | null>(null)
  const [creatingPR, setCreatingPR] = useState(false)
  const [activeTab, setActiveTab] = useState<Tab>('overview')
  const [showHelp, setShowHelp] = useState(false)
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [alwaysAskPending, setAlwaysAskPending] = useState(false)
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
  const [actionError, setActionError] = useState('')
  const [stopConfirmOpen, setStopConfirmOpen] = useState(false)
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [duplicating, setDuplicating] = useState(false)
  // Set once a task.cost_warning event arrives for this task (crossed the
  // early-warning threshold — see GET/PUT /settings/cost-warning). Cleared
  // on label change (new lifecycle stage) so a stale warning from a prior
  // stage/run doesn't linger indefinitely.
  const [shaOpen, setShaOpen] = useState(false)
  const [costWarning, setCostWarning] = useState<{ spentUsd: number; budgetUsd: number } | null>(null)
  const [verificationKey, setVerificationKey] = useState(0)
  // Must stay above the `if (!task) return` early return (rules of hooks).
  const isWide = useIsWideDesktop()
  const isPhone = useIsPhone()
  const { configs: agentConfigs, fetch: fetchAgents } = useAgentsStore()
  const { configs: providerConfigs, fetch: fetchProviderConfigs } = useProviderConfigsStore()
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

  const refreshHumanDecisions = useCallback(() => {
    if (!id) return
    const taskId = id
    api.tasks.humanDecisions(taskId).then((d) => {
      if (currentIdRef.current !== taskId) return
      setHumanDecisions(d ?? [])
    }).catch(() => {})
  }, [id])

  // Human decisions load separately so a failure can't break the initial load.
  useEffect(() => {
    refreshHumanDecisions()
  }, [refreshHumanDecisions])

  // Fetch agent configs for name lookup
  useEffect(() => {
    fetchAgents()
    fetchProviderConfigs()
  }, [fetchAgents, fetchProviderConfigs])

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
    setHumanDecisions([])
    setRememberText(null)
    setToast(null)
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
      if (event.type === 'resync') {
        // Dropped events or a reconnect: refetch everything this page derives
        // from WS events.
        refreshTask()
        refreshRuns()
        refreshComments()
        refreshLabelHistory()
        refreshSourceComments()
        setVerificationKey((k) => k + 1)
      } else if (event.type === 'task.label_changed' && event.payload.task_id === id) {
        setEditingTask(false)
        setCostWarning(null)
        refreshTask()
        refreshLabelHistory()
      } else if ((event.type === 'task.autonomy_approved' || event.type === 'task.autonomy_undone' || event.type === 'task.autonomy_held') && event.payload.task_id === id) {
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
      } else if ((event.type === 'task.verification' || event.type === 'task.preview') && event.payload.task_id === id) {
        setVerificationKey((k) => k + 1)
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
  const needsHuman = activeRun?.status === 'waiting_human' && activeRun.id !== retriedRunId
  const isRunning = activeRun?.status === 'running'
  const latestRun = runs[0]
  const canRerun = !!(latestRun && (latestRun.status === 'failed' || latestRun.status === 'completed' || latestRun.status === 'cancelled' || latestRun.status === 'interrupted'))
  const isHumanGateLabel = task
    ? workflow?.transitions?.some((t) => t.from_label === task.label && t.trigger_type === 'human') ?? false
    : false

  const showBrief = isHumanGateLabel && !needsHuman
  const briefKey = `${task?.label ?? ''}:${task?.updated_at ?? ''}:${verificationKey}`

  const humanActions = humanActionsFor(workflow ?? undefined, task?.label)

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
    setActionError('')
    setActionPending(true)
    try {
      await api.tasks.rerun(id)
      refreshRuns()
    } catch (e: unknown) {
      setActionError(errMessage(e))
    } finally {
      setActionPending(false)
    }
  }

  const focusReply = () => {
    const el = document.getElementById('task-reply-input')
    el?.scrollIntoView({ block: 'center' })
    el?.focus()
  }

  // Retry from an escalation banner: same re-dispatch as Rerun, but errors
  // show inline in the banner.
  const handleRetryEscalation = async () => {
    if (!id || !selectedRun) return
    setActionError('')
    setActionPending(true)
    try {
      await api.tasks.rerun(id)
      setRetriedRunId(selectedRun)
      refreshRuns()
    } catch (e: any) {
      setActionError(e.message ?? String(e))
    } finally {
      setActionPending(false)
    }
  }

  // handleStop requests cancellation of the running agent run. The pool marks
  // the run "cancelled" and pauses the task; the resulting task.agent_done WS
  // event refreshes the run list and task, so we just fire the request.
  const handleStop = () => {
    if (!id || !selectedRun) return
    setStopConfirmOpen(true)
  }

  const performStop = async () => {
    if (!id || !selectedRun) return
    setStopConfirmOpen(false)
    setActionError('')
    setActionPending(true)
    try {
      await api.tasks.cancelRun(id, selectedRun)
      refreshRuns()
    } catch (e: unknown) {
      setActionError(errMessage(e))
    } finally {
      setActionPending(false)
    }
  }

  // Reply to a waiting_human run: answers the agent's question with text and
  // starts a continuation run (resuming the provider session where supported).
  // The task stays on its label — this is a conversation, not a transition.
  const handleReply = async () => {
    if (!id || !selectedRun || !replyText.trim()) return
    setActionError('')
    setActionPending(true)
    try {
      const res = await api.tasks.replyRun(id, selectedRun, replyText.trim())
      setReplyText('')
      // Follow the continuation run so the user watches the agent pick the reply up.
      setSelectedRun(res.run_id)
      refreshRuns()
      refreshHumanDecisions()
    } catch (e: any) {
      setActionError(e.message ?? String(e))
    } finally {
      setActionPending(false)
    }
  }

  const handleHold = async () => {
    if (!id) return
    try {
      await api.autonomy.hold(id)
      showToast("Held. It's waiting for your review.")
    } catch (e) {
      showError(`Couldn't hold: ${errMessage(e)}`)
    } finally {
      void useNeedsMeStore.getState().refresh()
    }
  }

  const handleApprove = async () => {
    if (!id) return
    setActionError('')
    setActionPending(true)
    try {
      const updated = await api.tasks.approve(id)
      setTask(updated)
      refreshRuns()
    } catch (e: any) {
      setActionError(e.message ?? String(e))
    } finally {
      setActionPending(false)
    }
  }

  const handleReject = async () => {
    // Open review comments are persisted server-side and injected into the
    // next run's prompt directly — only the free-text note travels here.
    if (!id || (!rejectNote.trim() && openComments.length === 0)) return
    setActionError('')
    setActionPending(true)
    try {
      const updated = await api.tasks.reject(id, rejectNote.trim())
      setTask(updated)
      setRejectNote('')
      refreshRuns()
    } catch (e: any) {
      setActionError(e.message ?? String(e))
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
      showError(`Cannot create PR: ${errMessage(e)}`)
    } finally {
      setCreatingPR(false)
    }
  }

  const handleTogglePause = async () => {
    if (!id || !task) return
    setActionPending(true)
    try {
      const prev = !!task.paused
      const updated = await api.tasks.setPaused(id, !prev)
      setTask(updated)
      showUndoToast(`${prev ? 'Resumed' : 'Paused'} "${task.title}"`, async () => {
        setTask(await api.tasks.setPaused(id, prev))
      })
    } catch (e: unknown) {
      showError(errMessage(e))
    } finally {
      setActionPending(false)
    }
  }

  const handleToggleAlwaysAsk = async () => {
    if (!id || !task) return
    setAlwaysAskPending(true)
    try {
      const prev = !!task.always_ask
      const updated = await api.tasks.setAlwaysAsk(id, !prev)
      setTask(updated)
      showUndoToast(`Always ask ${prev ? 'off' : 'on'} for "${task.title}"`, async () => {
        setTask(await api.tasks.setAlwaysAsk(id, prev))
      })
    } catch (e: unknown) {
      showError(errMessage(e))
    } finally {
      setAlwaysAskPending(false)
    }
  }

  // "Move to…" control on the Overview tab — same call the board's drag/bulk
  // "Move to…" actions use. Gives a touch-friendly path to change a task's
  // label from any device (see issue #147).
  const handleMoveLabel = async (toLabel: string) => {
    if (!id || !toLabel || toLabel === task?.label) return
    setActionPending(true)
    try {
      const fromLabel = task!.label
      const updated = await api.tasks.moveLabel(id, toLabel)
      setTask(updated)
      refreshRuns()
      refreshLabelHistory()
      // Only offer Undo when the workflow defines the reverse human move.
      if (canHumanMove(workflow?.transitions ?? [], toLabel, fromLabel)) {
        showUndoToast(`Moved "${task!.title}" to ${toLabel}`, async () => {
          setTask(await api.tasks.moveLabel(id, fromLabel, 'Undo move'))
          refreshRuns()
          refreshLabelHistory()
        })
      }
    } catch (e: unknown) {
      showError(errMessage(e))
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

  const handleDeleteTask = () => {
    if (!id) return
    setDeleteConfirmOpen(true)
  }

  const handleSelectRun = (runId: string) => {
    setSelectedRun(runId)
    setActiveTab('logs')
  }

  const handleToggleArchive = async () => {
    if (!id || !task) return
    setActionPending(true)
    try {
      const prev = !!task.archived
      const updated = await api.tasks.setArchived(id, !prev)
      setTask(updated)
      showUndoToast(`${prev ? 'Unarchived' : 'Archived'} "${task.title}"`, async () => {
        setTask(await api.tasks.setArchived(id, prev))
      })
    } catch (e: unknown) {
      showError(errMessage(e))
    } finally {
      setActionPending(false)
    }
  }

  // Expose this task's actions to the global command palette. Handlers are
  // recreated every render, so commands call through a ref and the effect
  // depends only on data.
  const commandHandlersRef = useRef<TaskCommandHandlers>(null as unknown as TaskCommandHandlers)
  commandHandlersRef.current = {
    moveLabel: handleMoveLabel,
    approve: handleApprove,
    requestChanges: () => {
      const el = document.querySelector<HTMLTextAreaElement>('textarea[data-reject-note]')
      if (el) { el.scrollIntoView?.({ block: 'center' }); el.focus() }
      else setToast('Open the Overview tab to write a change request')
    },
    rerun: handleRerun,
    createPR: handleCreatePR,
    openPR: (url) => { window.open(url, '_blank', 'noopener') },
    togglePause: handleTogglePause,
    toggleArchive: handleToggleArchive,
  }
  useEffect(() => {
    if (!id || !task) return
    const proxy: TaskCommandHandlers = {
      moveLabel: (l) => commandHandlersRef.current.moveLabel(l),
      approve: () => commandHandlersRef.current.approve(),
      requestChanges: () => commandHandlersRef.current.requestChanges(),
      rerun: () => commandHandlersRef.current.rerun(),
      createPR: () => commandHandlersRef.current.createPR(),
      openPR: (u) => commandHandlersRef.current.openPR(u),
      togglePause: () => commandHandlersRef.current.togglePause(),
      toggleArchive: () => commandHandlersRef.current.toggleArchive(),
    }
    useCommandPaletteStore.getState().setTaskContext({
      taskId: id,
      title: task.title,
      commands: buildTaskCommands({ task, workflow, canRerun, handlers: proxy }),
    })
  }, [id, task, workflow, canRerun])
  useEffect(() => () => useCommandPaletteStore.getState().setTaskContext(null), [id])

  if (!task) return <div className="p-6 text-fg-muted">Loading…</div>

  const canRemember = !!task.repo_id
  const rememberRepoName = repos.find((r) => r.id === task.repo_id)?.name

  const overviewSections = (inReview: boolean) => (
    <>
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
              transitions={workflow?.transitions ?? []}
              onMoveLabel={handleMoveLabel}
              onDuplicate={() => setDuplicating(true)}
              onShouldHaveAsked={lastGateExitByPolicy(labelHistory) && !task.archived ? () => setShaOpen(true) : undefined}
            />

            {!inReview && showBrief && <ReviewBriefCard taskId={task?.id} refreshKey={briefKey} />}

            {duplicating && task && (
              <NewTaskModal source={task} onClose={() => setDuplicating(false)} />
            )}

            <SubtasksPanel
              task={task}
              labels={workflow?.labels ?? []}
              onChanged={() => { if (id) api.tasks.get(id).then(setTask).catch(() => {}) }}
            />

            {!inReview && (
            <RunHistoryList
              runs={runs}
              agentConfigs={agentConfigs}
              providerConfigs={providerConfigs}
              selectedRun={selectedRun}
              onSelectRun={handleSelectRun}
              isRunning={!!isRunning}
              canRerun={canRerun}
              onStop={handleStop}
              onRerun={handleRerun}
              actionPending={actionPending}
            />
            )}

            <SourceUpdateNotice task={task} onChanged={setTask} />

            <HumanDecisionsList decisions={humanDecisions} onRemember={canRemember ? setRememberText : undefined} />

            <DetailsDisclosure>
              {(task.always_ask || (workflow?.labels ?? []).some((l) => l.autonomy && l.autonomy.mode !== 'ask')) && (
                <AlwaysAskSwitch checked={!!task.always_ask} onChange={handleToggleAlwaysAsk} disabled={alwaysAskPending} />
              )}
              <DependenciesPanel
                task={task}
                onChanged={() => { if (id) api.tasks.get(id).then(setTask).catch(() => {}) }}
              />
              <ForceMoveControl
                task={task}
                labels={workflow?.labels ?? []}
                onMoved={(updated) => { setTask(updated); refreshRuns(); refreshLabelHistory() }}
              />
              <LabelHistoryList history={labelHistory} labels={workflow?.labels} onRemember={canRemember ? setRememberText : undefined} onShouldHaveAsked={() => setShaOpen(true)} />
              <SourceCommentsList comments={sourceComments} />
            </DetailsDisclosure>
    </>
  )

  const onGate = isStrictHumanGate(workflow ?? undefined, task.label)
  // Escalated waiting_human runs need the Reply UI, so they keep the tabbed layout.
  const useReviewLayout = onGate && !needsHuman
  const reviewVariant: 'desktop' | 'tablet' | 'phone' = isWide ? 'desktop' : isPhone ? 'phone' : 'tablet'
  // Heuristic: plan-review labels (e.g. review-plan) show the plan; all other gates show the diff.
  const reviewMode: 'plan' | 'diff' = /plan/i.test(task.label) ? 'plan' : 'diff'
  const approveTarget = humanPathTargetLabel(workflow ?? undefined, task.label, 'success')
  const approveLabel = approveTarget?.create_pr ? 'Approve and open PR' : 'Approve'

  const tabs: { id: Tab; label: string }[] = [
    { id: 'overview', label: 'Overview' },
    { id: 'logs', label: 'Logs' },
    { id: 'diff', label: 'Diff' },
  ]

  const reviewLayoutNode = (
    <ReviewLayout
      variant={reviewVariant}
      task={task}
      repoName={rememberRepoName}
      runs={runs}
      agentConfigs={agentConfigs}
      providerConfigs={providerConfigs}
      selectedRun={selectedRun}
      onSelectRun={setSelectedRun}
      isRunning={!!isRunning}
      canRerun={canRerun}
      onStop={handleStop}
      onRerun={handleRerun}
      labelHistory={labelHistory}
      canApprove={humanActions.canApprove}
      canReject={humanActions.canReject}
      approveLabel={approveLabel}
      onApprove={handleApprove}
      rejectNote={rejectNote}
      setRejectNote={setRejectNote}
      onReject={handleReject}
      openCommentsCount={openComments.length}
      actionError={actionError}
      actionPending={actionPending}
      diffComments={diffComments}
      onAddComment={handleAddComment}
      onRemoveComment={handleRemoveComment}
      onReopenComment={handleReopenComment}
      onCreatePR={handleCreatePR}
      creatingPR={creatingPR}
      verificationKey={verificationKey}
      briefKey={briefKey}
      labels={workflow?.labels ?? []}
      autoApproving={autoPending?.due_at ? { dueAt: autoPending.due_at } : undefined}
      onHold={handleHold}
      alwaysAsk={!!task.always_ask}
      mode={reviewMode}
      onBack={() => navigate('/board')}
      onHelp={() => setShowHelp(true)}
      details={overviewSections(true)}
    />
  )

  return (
    <div className="flex h-full overflow-hidden flex-col min-w-0">
      {useReviewLayout ? (
        <div className="flex-1 min-h-0">{reviewLayoutNode}</div>
      ) : (
      <>
      {/* Tab bar */}
      <div className="shrink-0 flex items-center gap-2 border-b border-border px-4 pt-3 w-full overflow-x-hidden">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setActiveTab(t.id)}
            className={`flex-grow min-w-0 md:min-w-[100px] px-3 py-1.5 text-xs font-medium rounded-t transition-colors ${
              activeTab === t.id
                ? 'bg-surface-raised text-accent-fg border-b-2 border-accent'
                : 'text-fg-muted hover:text-fg'
            }`}
          >
            {t.label}
          </button>
        ))}
        <HelpButton onClick={() => setShowHelp(true)} title="About this task" />
      </div>

      </>
      )}

      {showHelp && (
        <HelpModal title="About Task Detail" onClose={() => setShowHelp(false)}>
          <TaskDetailHelp />
        </HelpModal>
      )}

      {!useReviewLayout && (
      <div className="flex-1 overflow-hidden">
        {/* Overview tab */}
        {activeTab === 'overview' && (
          <div className="h-full overflow-y-auto overflow-x-hidden p-3 md:p-5 flex flex-col gap-4 min-w-0">
            {overviewSections(false)}
          </div>
        )}

        {/* Logs tab */}
        {activeTab === 'logs' && (
          <RunLogPane
            taskId={id}
            runId={selectedRun}
            isRunning={!!isRunning}
            run={activeRun}
            agentConfigs={agentConfigs}
            providerConfigs={providerConfigs}
            labelHistory={labelHistory}
            onRetry={needsHuman ? handleRetryEscalation : canRerun ? handleRerun : undefined}
            onReply={needsHuman ? focusReply : undefined}
            onOpenDiff={() => setActiveTab('diff')}
          />
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
            verificationRefreshKey={verificationKey}
            showBrief={showBrief}
            briefRefreshKey={briefKey}
          />
        )}
      </div>
      )}

      {/* Cost early-warning banner — crossed the configurable warn_ratio threshold
          (default 80%) of the effective cost budget, but not yet exhausted. See
          GET/PUT /settings/cost-warning and docs/agents.md#cost-budgets. */}
      {costWarning && (
        <div className="mb-4 px-4 py-3 rounded-lg bg-warning-soft border border-warning/40 text-warning text-sm">
          <Icon icon={CircleDollarSign} size="sm" className="inline -mt-0.5 mr-1" />Cost warning: this task has spent ${costWarning.spentUsd.toFixed(2)} of its ${costWarning.budgetUsd.toFixed(2)} budget.
        </div>
      )}

      {/* Approval panel — shown when agent needs human or task is at a human-gate label */}
      {!useReviewLayout && (needsHuman || isHumanGateLabel) && (
        <TaskActions
          activeRun={activeRun}
          needsHuman={!!needsHuman}
          openCommentsCount={openComments.length}
          replyText={replyText}
          setReplyText={setReplyText}
          onReply={handleReply}
          onRetry={handleRetryEscalation}
          rejectNote={rejectNote}
          setRejectNote={setRejectNote}
          onReject={handleReject}
          onApprove={handleApprove}
          canApprove={humanActions.canApprove}
          canReject={humanActions.canReject}
          actionError={actionError}
          actionPending={actionPending}
          onJumpToDiffTab={() => setActiveTab('diff')}
        />
      )}

      {shaOpen && (
        <ShouldHaveAskedDialog
          taskId={task.id}
          taskTitle={task.title}
          done={!!workflow?.labels.find((l) => l.name === task.label)?.is_terminal}
          workflow={workflow ?? undefined}
          repoName={rememberRepoName}
          onClose={() => setShaOpen(false)}
          onDone={(r) => { setTask(r.task); refreshLabelHistory(); refreshRuns() }}
        />
      )}
      {rememberText !== null && canRemember && (
        <RememberLessonModal
          repoId={task.repo_id}
          repoName={rememberRepoName}
          taskId={task.id}
          text={rememberText}
          onClose={() => setRememberText(null)}
          onSaved={() => {
            setRememberText(null)
            setToast(`Lesson saved for ${rememberRepoName ?? 'this repo'}`)
          }}
        />
      )}
      {stopConfirmOpen && (
        <ConfirmDialog
          destructive
          title="Stop agent run?"
          confirmLabel="Stop run"
          onCancel={() => setStopConfirmOpen(false)}
          onConfirm={performStop}
        >
          <p>
            The running agent is cancelled and the task is paused so it isn&apos;t immediately re-dispatched. Work the agent
            hasn&apos;t committed yet may be lost.
          </p>
        </ConfirmDialog>
      )}
      {deleteConfirmOpen && (
        <ConfirmDialog
          destructive
          title="Delete task?"
          confirmLabel="Delete task"
          onCancel={() => setDeleteConfirmOpen(false)}
          onConfirm={async () => {
            await api.tasks.delete(task.id)
            navigate('/board')
          }}
        >
          <p>
            &quot;{task.title}&quot; and its agent runs, logs and review comments will be permanently deleted. This
            can&apos;t be undone.
          </p>
        </ConfirmDialog>
      )}
      {toast && <Toast message={toast} onDismiss={() => setToast(null)} />}
    </div>
  )
}
