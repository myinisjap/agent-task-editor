import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import AutoMergeBadge from './AutoMergeBadge'

describe('AutoMergeBadge', () => {
  it('renders nothing without state', () => {
    const { container } = render(<AutoMergeBadge autoMerge={null} />)
    expect(container).toBeEmptyDOMElement()
  })
  it('shows the blocking reason', () => {
    render(<AutoMergeBadge autoMerge={{ state: 'waiting', reason: 'checks_pending', message: '2 check(s) still pending' }} />)
    expect(screen.getByText(/Auto-merge when green · 2 check\(s\) still pending/)).toBeInTheDocument()
  })
  it('shows merging', () => {
    render(<AutoMergeBadge autoMerge={{ state: 'merging', reason: '', message: '' }} />)
    expect(screen.getByText('Auto-merging…')).toBeInTheDocument()
  })
})
