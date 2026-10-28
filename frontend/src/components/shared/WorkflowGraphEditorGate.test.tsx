import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import WorkflowGraphEditor from './WorkflowGraphEditor'
import { draftFromWorkflow } from '../../lib/workflowEdit'
import { gateWorkflow } from '../pipeline/testFixtures'

describe('WorkflowGraphEditor gate mode', () => {
  it('shows the gate mode read-only with a link to the gate panel', () => {
    const draft = draftFromWorkflow(gateWorkflow({ mode: 'auto', ask_on: [], hold: '600s' }))
    render(
      <MemoryRouter>
        <WorkflowGraphEditor draft={draft} onChange={() => {}} selectedLabel="review" onSelectLabel={() => {}} taskCounts={new Map()} tasksPending={false} savedLabelNames={new Set()} connectPrefill={null} />
      </MemoryRouter>,
    )
    expect(screen.getByText('Auto')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Change on the gate panel' })).toHaveAttribute('href', '/pipeline/overview?gate=review')
  })
})
