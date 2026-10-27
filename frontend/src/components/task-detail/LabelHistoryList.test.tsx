import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import LabelHistoryList from './LabelHistoryList'
import type { TaskLabelHistoryEntry } from '../../api/client'

function entry(overrides: Partial<TaskLabelHistoryEntry>): TaskLabelHistoryEntry {
  return {
    id: 'h1',
    task_id: 't1',
    from_label: 'todo',
    to_label: 'work',
    trigger: 'human',
    actor_id: '',
    note: '',
    created_at: new Date().toISOString(),
    ...overrides,
  } as TaskLabelHistoryEntry
}

describe('LabelHistoryList', () => {
  it('shows "by you" for a human move with no actor', () => {
    render(<LabelHistoryList history={[entry({})]} />)
    expect(screen.getByText(/by you/)).toBeInTheDocument()
    expect(screen.queryByText(/unknown/)).not.toBeInTheDocument()
  })

  it('shows the named actor for a human move', () => {
    render(<LabelHistoryList history={[entry({ actor_id: 'alice' })]} />)
    expect(screen.getByText(/by alice/)).toBeInTheDocument()
  })

  it('keeps the short run id for agent entries and no "by" text', () => {
    render(<LabelHistoryList history={[entry({ trigger: 'agent', actor_id: 'abcdef1234567890' })]} />)
    expect(screen.getByText(/abcdef12/)).toBeInTheDocument()
    expect(screen.queryByText(/by /)).not.toBeInTheDocument()
  })
})

describe('LabelHistoryList — Remember for this repo', () => {
  it('offers Remember only on human entries with a note', () => {
    const onRemember = vi.fn()
    render(
      <LabelHistoryList
        onRemember={onRemember}
        history={[
          entry({ id: 'a', note: 'needs tests' }),
          entry({ id: 'b', note: '' }),
          entry({ id: 'c', trigger: 'agent', note: 'agent note' }),
        ]}
      />,
    )
    const buttons = screen.getAllByRole('button', { name: 'Remember for this repo' })
    expect(buttons).toHaveLength(1)
    fireEvent.click(buttons[0])
    expect(onRemember).toHaveBeenCalledWith('needs tests')
  })

  it('hides Remember without the callback', () => {
    render(<LabelHistoryList history={[entry({ note: 'needs tests' })]} />)
    expect(screen.queryByRole('button', { name: 'Remember for this repo' })).not.toBeInTheDocument()
  })
})
