import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { ComponentProps } from 'react'
import type { Task } from '../../api/client'
import TaskHeader from './TaskHeader'

const noop = () => {}
const task = (o: Partial<Task>): Task =>
  ({ id: 't1', title: 'T', description: '', type: 'feature', label: 'todo', repo_id: 'r1', workflow_id: 'w1', created_at: '', updated_at: '', ...o }) as Task

function renderHeader(t: Task) {
  const props = {
    task: t, repos: [], isStartingColumn: false, editingTask: false, editTitle: '', setEditTitle: noop, editDesc: '', setEditDesc: noop,
    editType: '', setEditType: noop, editRepoId: '', setEditRepoId: noop, editMaxCostUsd: '', setEditMaxCostUsd: noop, editPriority: 0,
    setEditPriority: noop, taskSaving: false, taskSaveError: '', onStartEdit: noop, onCancelEdit: noop, onTaskSave: noop, onDelete: noop,
    onTogglePause: noop, actionPending: false, onCreatePR: noop, creatingPR: false, onSyncGitState: noop, onBack: noop, labels: [], onMoveLabel: noop,
  } as unknown as ComponentProps<typeof TaskHeader>
  return render(<TaskHeader {...props} />)
}

describe('TaskHeader cost roll-up', () => {
  it('shows the change-set total for a multi-repo parent', () => {
    renderHeader(task({ cumulative_cost_usd: 1, rolled_up_cost_usd: 7, change_set_repo_count: 3 }))
    expect(screen.getByText('$7.00 total (change set) · $1.00 this task')).toBeInTheDocument()
  })

  it('leaves a single-repo parent with a rolled-up value unchanged', () => {
    renderHeader(task({ cumulative_cost_usd: 1, rolled_up_cost_usd: 7, change_set_repo_count: 0 }))
    expect(screen.getByText('$1.00')).toBeInTheDocument()
    expect(screen.queryByText(/total \(change set\)/)).toBeNull()
  })
})
