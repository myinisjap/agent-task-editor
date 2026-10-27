import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import HumanDecisionsList from './HumanDecisionsList'
import type { TaskHumanDecision } from '../../api/client'

const d: TaskHumanDecision = {
  run_id: 'r1', label: 'work', escalation_reason: 'ask', question: 'Which?', reply: 'Use tabs',
  replied_at: '2026-01-01T00:00:00Z', asked_at: '2026-01-01T00:00:00Z', agent_name: 'coder',
}

describe('HumanDecisionsList', () => {
  it('renders nothing when empty', () => {
    const { container } = render(<HumanDecisionsList decisions={[]} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders replies and expands the question', () => {
    render(<HumanDecisionsList decisions={[d]} />)
    expect(screen.getByText('Use tabs')).toBeInTheDocument()
    expect(screen.queryByText('Which?')).not.toBeInTheDocument()
    fireEvent.click(screen.getByText(/question/))
    expect(screen.getByText('Which?')).toBeInTheDocument()
  })

  it('calls onRemember with the reply text', () => {
    const onRemember = vi.fn()
    render(<HumanDecisionsList decisions={[d]} onRemember={onRemember} />)
    fireEvent.click(screen.getByRole('button', { name: 'Remember for this repo' }))
    expect(onRemember).toHaveBeenCalledWith('Use tabs')
  })

  it('hides Remember without onRemember', () => {
    render(<HumanDecisionsList decisions={[d]} />)
    expect(screen.queryByRole('button', { name: 'Remember for this repo' })).not.toBeInTheDocument()
  })
})
