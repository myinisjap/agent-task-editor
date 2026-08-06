import { useState } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import ModalShell from './ModalShell'

describe('ModalShell', () => {
  it('renders dialog role/aria-modal/aria-label', () => {
    render(
      <ModalShell onClose={vi.fn()} ariaLabel="Test Dialog">
        <p>content</p>
      </ModalShell>,
    )
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveAttribute('aria-label', 'Test Dialog')
  })

  it('calls onClose when Escape is pressed', async () => {
    const onClose = vi.fn()
    render(
      <ModalShell onClose={onClose} ariaLabel="Test Dialog">
        <p>content</p>
      </ModalShell>,
    )
    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('calls onClose when the backdrop is clicked', async () => {
    const onClose = vi.fn()
    render(
      <ModalShell onClose={onClose} ariaLabel="Test Dialog">
        <p>content</p>
      </ModalShell>,
    )
    await userEvent.click(screen.getByRole('dialog'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not call onClose when clicking inside the card', async () => {
    const onClose = vi.fn()
    render(
      <ModalShell onClose={onClose} ariaLabel="Test Dialog">
        <p>content</p>
      </ModalShell>,
    )
    await userEvent.click(screen.getByText('content'))
    expect(onClose).not.toHaveBeenCalled()
  })

  it('focuses the first focusable element on open when no initialFocusRef is given', () => {
    render(
      <ModalShell onClose={vi.fn()} ariaLabel="Test Dialog">
        <button>First</button>
        <button>Second</button>
      </ModalShell>,
    )
    expect(screen.getByText('First')).toHaveFocus()
  })

  it('focuses the initialFocusRef target on open when provided', () => {
    function Wrapper() {
      const ref = { current: null as HTMLElement | null }
      return (
        <ModalShell onClose={vi.fn()} ariaLabel="Test Dialog" initialFocusRef={ref}>
          <button>First</button>
          <input ref={(el) => { ref.current = el }} placeholder="Second" />
        </ModalShell>
      )
    }
    render(<Wrapper />)
    expect(screen.getByPlaceholderText('Second')).toHaveFocus()
  })

  it('traps Tab within the dialog, wrapping from the last focusable back to the first', async () => {
    render(
      <ModalShell onClose={vi.fn()} ariaLabel="Test Dialog">
        <button>First</button>
        <button>Second</button>
      </ModalShell>,
    )
    const first = screen.getByText('First')
    const second = screen.getByText('Second')
    expect(first).toHaveFocus()

    await userEvent.tab()
    expect(second).toHaveFocus()

    await userEvent.tab()
    expect(first).toHaveFocus()
  })

  it('traps Shift+Tab within the dialog, wrapping from the first focusable back to the last', async () => {
    render(
      <ModalShell onClose={vi.fn()} ariaLabel="Test Dialog">
        <button>First</button>
        <button>Second</button>
      </ModalShell>,
    )
    const first = screen.getByText('First')
    const second = screen.getByText('Second')
    expect(first).toHaveFocus()

    await userEvent.tab({ shift: true })
    expect(second).toHaveFocus()
  })

  it('restores focus to the previously-focused element when it unmounts', async () => {
    function Harness() {
      const [open, setOpen] = useState(false)
      return (
        <>
          <button onClick={() => setOpen(true)}>Open</button>
          {open && (
            <ModalShell onClose={() => setOpen(false)} ariaLabel="Test Dialog">
              <button>Inside</button>
            </ModalShell>
          )}
        </>
      )
    }
    render(<Harness />)
    const openButton = screen.getByText('Open')
    openButton.focus()
    expect(openButton).toHaveFocus()

    await userEvent.click(openButton)
    expect(screen.getByText('Inside')).toHaveFocus()

    await userEvent.keyboard('{Escape}')
    expect(openButton).toHaveFocus()
  })
})
