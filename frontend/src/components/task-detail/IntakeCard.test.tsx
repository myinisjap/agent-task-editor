import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import IntakeCard from './IntakeCard'
import { api } from '../../api/client'
import type { Task, Workflow } from '../../api/client'

const check = {
  task_id: 't1', status: 'done', trigger: 'manual', verdict: 'needs_detail', questions: ['Which page?'],
  suggested_type: 'bug', suggested_workflow_id: '', duplicate_ids: ['d1'], error: '', provider_config_id: null,
  cost_usd: 0.02, input_tokens: 1, output_tokens: 1, started_at: '2026-01-01T00:00:00Z', finished_at: null,
} as const

const task = {
  id: 't1', title: 'T', description: 'desc', type: 'feature', label: 'not_ready', archived: false,
  possible_duplicates: [{ id: 'd1', title: 'Other task', label: 'work', score: 0.8 }],
} as unknown as Task
const workflow = { labels: [{ name: 'not_ready', agent_ignore: true }] } as unknown as Workflow

describe('IntakeCard', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.spyOn(api.tasks, 'intakeCheck').mockResolvedValue({ ...check } as never)
    vi.spyOn(api.intakeAssistant, 'get').mockResolvedValue({ enabled: true, auto_check: true, provider_config_id: 'p' })
  })

  it('shows verdict, appends an answer, applies the type and archives as duplicate', async () => {
    const update = vi.spyOn(api.tasks, 'update').mockResolvedValue(task)
    const archive = vi.spyOn(api.tasks, 'setArchived').mockResolvedValue(task)
    const onChanged = vi.fn()
    render(<IntakeCard task={task} workflow={workflow} onChanged={onChanged} />)
    expect(await screen.findByText('Which page?')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Answer: Which page?'), 'login')
    await userEvent.click(screen.getByText('Add'))
    await waitFor(() => expect(update).toHaveBeenCalledWith('t1', { description: 'desc\n\nQ: Which page?\nA: login' }))
    await userEvent.click(screen.getByText('Apply'))
    expect(update).toHaveBeenCalledWith('t1', { type: 'bug' })
    await userEvent.click(screen.getByText('Archive as duplicate'))
    expect(archive).toHaveBeenCalledWith('t1', true)
  })
})
