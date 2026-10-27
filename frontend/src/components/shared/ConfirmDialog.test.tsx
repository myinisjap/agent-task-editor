import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import ConfirmDialog from './ConfirmDialog'

function setup(props: Partial<React.ComponentProps<typeof ConfirmDialog>> = {}) {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  render(
    <ConfirmDialog title="Delete thing?" confirmLabel="Delete" onConfirm={onConfirm} onCancel={onCancel} {...props}>
      <p>It will be gone.</p>
    </ConfirmDialog>,
  )
  return { onConfirm, onCancel }
}

describe('ConfirmDialog', () => {
  it('renders a dialog named by its title with the body', () => {
    setup()
    expect(screen.getByRole('dialog', { name: 'Delete thing?' })).toBeInTheDocument()
    expect(screen.getByText('It will be gone.')).toBeInTheDocument()
  })

  it('Cancel and Escape call onCancel; Confirm calls onConfirm', () => {
    const { onConfirm, onCancel } = setup()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(2)
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it('requireText keeps confirm disabled until the exact text is typed', () => {
    const { onConfirm } = setup({ requireText: 'my-repo' })
    const btn = screen.getByRole('button', { name: 'Delete' })
    expect(btn).toBeDisabled()
    const input = screen.getByLabelText('Type my-repo to confirm')
    fireEvent.change(input, { target: { value: 'my-rep' } })
    expect(btn).toBeDisabled()
    fireEvent.change(input, { target: { value: 'my-repo' } })
    expect(btn).toBeEnabled()
    fireEvent.click(btn)
    expect(onConfirm).toHaveBeenCalled()
  })

  it('shows a rejected onConfirm message inline and stays open', async () => {
    setup({ onConfirm: () => Promise.reject(new Error('repo has tasks')) })
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('repo has tasks')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled())
  })

  it('disables buttons while confirming', async () => {
    let resolve!: () => void
    setup({ onConfirm: () => new Promise<void>((r) => { resolve = r }) })
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled())
    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
    resolve()
  })
})
