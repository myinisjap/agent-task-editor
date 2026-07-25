import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import HelpModal from './HelpModal'

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
