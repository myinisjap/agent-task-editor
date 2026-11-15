import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import MergeOrderBadge from './MergeOrderBadge'

const pending = [{ task_id: 'a', title: 'API', pr_url: 'https://x/1' }]

describe('MergeOrderBadge', () => {
  it('warns when a predecessor has not merged and the PR is open', () => {
    render(<MergeOrderBadge task={{ git_state: 'pr_open', merge_order_pending: pending }} />)
    const el = screen.getByTestId('merge-order-warning')
    expect(el).toHaveTextContent('Merge after: API')
    expect(el).toHaveAttribute('title', 'Change set order: merge API first')
  })
  it('renders nothing without pending predecessors or an open PR', () => {
    const { container, rerender } = render(<MergeOrderBadge task={{ git_state: 'pr_open', merge_order_pending: [] }} />)
    expect(container).toBeEmptyDOMElement()
    rerender(<MergeOrderBadge task={{ git_state: 'pr_merged', merge_order_pending: pending }} />)
    expect(container).toBeEmptyDOMElement()
  })
})
