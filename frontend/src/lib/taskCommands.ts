import type { Task, Workflow } from '../api/client'
import type { TaskCommand } from '../stores/commandPalette'
import { canHumanMove } from './humanMoves'
import { humanActionsFor, humanPathTargetLabel } from './humanGate'
import { labelDisplayName } from './labelDisplay'

export interface TaskCommandHandlers {
  moveLabel: (label: string) => void | Promise<void>
  approve: () => void | Promise<void>
  /** Reject needs a note, so this focuses the note field rather than calling the API. */
  requestChanges: () => void
  rerun: () => void | Promise<void>
  createPR: () => void | Promise<void>
  openPR: (url: string) => void
  togglePause: () => void | Promise<void>
  toggleArchive: () => void | Promise<void>
}

export function buildTaskCommands(args: {
  task: Task
  workflow: Workflow | null | undefined
  canRerun: boolean
  handlers: TaskCommandHandlers
}): TaskCommand[] {
  const { task, workflow, canRerun, handlers } = args
  const cmds: TaskCommand[] = []
  const add = (id: string, label: string, run: TaskCommand['run'], keywords?: string) =>
    cmds.push({ id, label, group: 'Task', run, keywords })

  if (workflow) {
    const labels = [...(workflow.labels ?? [])].sort((a, b) => a.sort_order - b.sort_order)
    for (const l of labels) {
      if (canHumanMove(workflow.transitions ?? [], task.label, l.name)) {
        add(`move:${l.name}`, `Move to ${labelDisplayName(l)}`, () => handlers.moveLabel(l.name), 'move label column')
      }
    }
    const { canApprove, canReject } = humanActionsFor(workflow, task.label)
    if (canApprove) {
      const target = humanPathTargetLabel(workflow, task.label, 'success')
      add('approve', target?.create_pr ? 'Approve and open PR' : 'Approve', () => handlers.approve(), 'accept lgtm')
    }
    if (canReject) add('reject', 'Request changes', () => handlers.requestChanges(), 'reject')
  }
  if (canRerun) add('rerun', 'Rerun', () => handlers.rerun(), 'retry run agent')
  if (task.pr_url) {
    const url = task.pr_url
    add('open-pr', 'Open PR', () => handlers.openPR(url), 'pull request github')
  } else if (task.branch) {
    add('create-pr', 'Create PR', () => handlers.createPR(), 'pull request github')
  }
  add('pause', task.paused ? 'Resume' : 'Pause', () => handlers.togglePause(), task.paused ? 'unpause' : 'hold stop')
  add('archive', task.archived ? 'Unarchive' : 'Archive', () => handlers.toggleArchive(), 'hide')
  return cmds
}
