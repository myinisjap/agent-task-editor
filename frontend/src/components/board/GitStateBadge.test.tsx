import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import GitStateBadge from './GitStateBadge'

describe('GitStateBadge', () => {
  it('renders nothing without a branch', () => {
    const { container } = render(<GitStateBadge branch="" gitState="pr_open" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('shows the git state icon with the branch in its tooltip', () => {
    render(<GitStateBadge branch="ate-fix-thing" gitState="pr_open" />)
    expect(screen.getByTitle('PR open (ate-fix-thing)')).toBeInTheDocument()
  })

  it('flags an open PR that conflicts with its base branch', () => {
    render(<GitStateBadge branch="ate-fix-thing" gitState="pr_open" prMergeable="conflicting" />)
    expect(screen.getByLabelText('merge conflict')).toBeInTheDocument()
  })

  it('does not flag a PR that still merges cleanly', () => {
    render(<GitStateBadge branch="ate-fix-thing" gitState="pr_open" prMergeable="mergeable" />)
    expect(screen.queryByLabelText('merge conflict')).not.toBeInTheDocument()
  })

  it('does not flag a stale conflicting verdict on a merged PR', () => {
    render(<GitStateBadge branch="ate-fix-thing" gitState="pr_merged" prMergeable="conflicting" />)
    expect(screen.queryByLabelText('merge conflict')).not.toBeInTheDocument()
  })
})
