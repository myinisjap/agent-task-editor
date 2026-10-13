import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Task, WorkflowLabel } from '../../api/client'

const forceMock = vi.fn()
vi.mock('../../api/client', () => ({
  api: { tasks: { forceMoveLabel: (...a: unknown[]) => forceMock(...a) } },
}))

import ForceMoveControl from './ForceMoveControl'
import { forceMoveTargets } from '../../lib/forceMove'

const labels = [
  { name: 'done', sort_order: 3 },
  { name: 'not_ready', sort_order: 0 },
  { name: 'work', sort_order: 2 },
  { name: 'plan', sort_order: 1 },
] as unknown as WorkflowLabel[]
const task = { id: 't1', label: 'work' } as unknown as Task

describe('forceMoveTargets', () => {
  it('sorts by board order and excludes the current label', () => {
    expect(forceMoveTargets(labels, 'work').map((l) => l.name)).toEqual(['not_ready', 'plan', 'done'])
  })
})

describe('ForceMoveControl', () => {
  beforeEach(() => { forceMock.mockReset() })

  it('warns, forces the move with a note, and reports the updated task', async () => {
    const moved = { ...task, label: 'not_ready' }
    forceMock.mockResolvedValue(moved)
    const onMoved = vi.fn()
    render(<ForceMoveControl task={task} labels={labels} onMoved={onMoved} />)
    await userEvent.click(screen.getByRole('button', { name: 'Force move…' }))
    expect(screen.getByText(/Bypasses the workflow/)).toBeInTheDocument()
    const confirm = screen.getByRole('button', { name: 'Force move' })
    expect(confirm).toBeDisabled()
    await userEvent.selectOptions(screen.getByRole('combobox'), 'not_ready')
    await userEvent.type(screen.getByRole('textbox'), 'stuck')
    await userEvent.click(confirm)
    expect(forceMock).toHaveBeenCalledWith('t1', 'not_ready', 'stuck')
    expect(onMoved).toHaveBeenCalledWith(moved)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('shows API errors inline and keeps the dialog open', async () => {
    forceMock.mockImplementation(async () => { throw new Error("a run is currently active") })
    const onMoved = vi.fn()
    render(<ForceMoveControl task={task} labels={labels} onMoved={onMoved} />)
    await userEvent.click(screen.getByRole('button', { name: 'Force move…' }))
    await userEvent.selectOptions(screen.getByRole('combobox'), 'plan')
    await userEvent.click(screen.getByRole('button', { name: 'Force move' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('a run is currently active')
    expect(onMoved).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
