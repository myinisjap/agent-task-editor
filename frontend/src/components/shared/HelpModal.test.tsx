import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import HelpModal from './HelpModal'
import { ReposHelp } from './pageHelp'

describe('HelpModal', () => {
  it('renders title and child content', () => {
    render(
      <HelpModal title="About Widgets" onClose={vi.fn()}>
        <p>Widgets are great.</p>
      </HelpModal>,
    )
    expect(screen.getByText('About Widgets')).toBeInTheDocument()
    expect(screen.getByText('Widgets are great.')).toBeInTheDocument()
  })

  it('calls onClose when the close button is clicked', async () => {
    const onClose = vi.fn()
    render(
      <HelpModal title="About Widgets" onClose={onClose}>
        <p>content</p>
      </HelpModal>,
    )
    await userEvent.click(screen.getByLabelText('Close'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls onClose when Escape is pressed', async () => {
    const onClose = vi.fn()
    render(
      <HelpModal title="About Widgets" onClose={onClose}>
        <p>content</p>
      </HelpModal>,
    )
    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls onClose when the backdrop is clicked', async () => {
    const onClose = vi.fn()
    render(
      <HelpModal title="About Widgets" onClose={onClose}>
        <p>content</p>
      </HelpModal>,
    )
    await userEvent.click(screen.getByRole('dialog'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not call onClose when clicking inside the card', async () => {
    const onClose = vi.fn()
    render(
      <HelpModal title="About Widgets" onClose={onClose}>
        <p>content</p>
      </HelpModal>,
    )
    await userEvent.click(screen.getByText('content'))
    expect(onClose).not.toHaveBeenCalled()
  })
})

// issue #264 phase 4 — ReposHelp's "Issue sync" section used to describe
// import as create-only; these assert the rewritten copy plus the three new
// sections (update policy, gone action, comment sync) actually render.
describe('ReposHelp (issue sync content)', () => {
  function renderReposHelp() {
    render(
      <HelpModal title="About Repos" onClose={vi.fn()}>
        <ReposHelp />
      </HelpModal>,
    )
  }

  it('describes issue import as ongoing sync, not create-only', () => {
    renderReposHelp()
    expect(screen.getByText(/keeps them in\s*sync afterward/i)).toBeInTheDocument()
  })

  it('documents the update policy section and its three values', () => {
    renderReposHelp()
    expect(screen.getByText('Keeping tasks in sync (update policy)')).toBeInTheDocument()
    expect(screen.getByText(/gate/, { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByText(/always/, { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByText(/never/, { selector: 'strong' })).toBeInTheDocument()
  })

  it('documents the closed/unlabeled issue action section and the active-run exception', () => {
    renderReposHelp()
    expect(screen.getByText('Closed or unlabeled issue action')).toBeInTheDocument()
    expect(screen.getByText(/flag/, { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByText(/archive/, { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByText(/move/, { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByText(/always just flagged, never archived or moved/i)).toBeInTheDocument()
  })

  it('documents issue comment sync as opt-in, write-access-only, untrusted context', () => {
    renderReposHelp()
    expect(screen.getByText('Issue comment sync')).toBeInTheDocument()
    expect(screen.getByText(/off by default/i)).toBeInTheDocument()
    expect(screen.getByText(/write\s*access to the repo/i)).toBeInTheDocument()
    expect(screen.getByText(/untrusted context/i)).toBeInTheDocument()
  })
})
