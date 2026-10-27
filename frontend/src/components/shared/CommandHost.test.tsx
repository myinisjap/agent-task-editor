import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import CommandHost from './CommandHost'
import { api } from '../../api/client'
import { useCommandPaletteStore } from '../../stores/commandPalette'

vi.mock('../board/NewTaskModal', () => ({
  default: () => <div role="dialog" aria-label="New task modal" />,
}))

function Loc() {
  return <div data-testid="loc">{useLocation().pathname}</div>
}
function setup() {
  return render(
    <MemoryRouter initialEntries={['/inbox']}>
      <CommandHost />
      <Loc />
      <input data-testid="field" />
    </MemoryRouter>,
  )
}
const key = (k: string, init: KeyboardEventInit = {}, target: Element | Window = window) =>
  act(() => { fireEvent.keyDown(target, { key: k, ...init }) })

beforeEach(() => {
  useCommandPaletteStore.setState({ open: false, sheetOpen: false, newTaskOpen: false, taskContext: null })
  vi.spyOn(api.tasks, 'list').mockResolvedValue({ items: [{ id: 'abcdef12-0000-0000-0000-000000000000', title: 'Fix login', label: 'todo' }] } as never)
})

describe('CommandHost', () => {
  it('opens with Ctrl+K and closes with Escape', async () => {
    setup()
    key('k', { ctrlKey: true })
    expect(await screen.findByRole('dialog', { name: 'Search and commands' })).toBeInTheDocument()
    key('Escape', {}, screen.getByRole('combobox'))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('opens even while typing in an input', async () => {
    setup()
    key('k', { metaKey: true }, screen.getByTestId('field'))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })

  it('searches tasks and navigates on select', async () => {
    setup()
    key('k', { ctrlKey: true })
    const input = await screen.findByRole('combobox')
    fireEvent.change(input, { target: { value: 'login' } })
    const row = await screen.findByText('Fix login')
    expect(api.tasks.list).toHaveBeenCalledWith({ q: 'login' }, { limit: 10 })
    fireEvent.click(row)
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('/tasks/abcdef12-0000-0000-0000-000000000000'))
  })

  it('navigates to a page and opens create task from the palette', async () => {
    setup()
    key('k', { ctrlKey: true })
    fireEvent.click(await screen.findByText('Board'))
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('/board'))
    key('k', { ctrlKey: true })
    fireEvent.click(await screen.findByText('Create task'))
    expect(await screen.findByRole('dialog', { name: 'New task modal' })).toBeInTheDocument()
  })

  it('c opens new task, but not inside an input', async () => {
    setup()
    key('c', {}, screen.getByTestId('field'))
    expect(screen.queryByRole('dialog')).toBeNull()
    key('c')
    expect(await screen.findByRole('dialog', { name: 'New task modal' })).toBeInTheDocument()
  })

  it('g b navigates to the board', async () => {
    setup()
    key('g'); key('b')
    await waitFor(() => expect(screen.getByTestId('loc')).toHaveTextContent('/board'))
  })

  it('? opens the shortcut sheet including Inbox keys', async () => {
    setup()
    key('?', { shiftKey: true })
    expect(await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })).toBeInTheDocument()
    expect(screen.getByText('Request changes')).toBeInTheDocument()
  })

  it('shows and runs the current task commands', async () => {
    const run = vi.fn()
    useCommandPaletteStore.setState({ taskContext: { taskId: 't', title: 'T', commands: [{ id: 'x', label: 'Archive', group: 'Task', run }] } })
    setup()
    key('k', { ctrlKey: true })
    fireEvent.click(await screen.findByText('Archive'))
    await waitFor(() => expect(run).toHaveBeenCalled())
  })

  it('disables single-key shortcuts on touch devices', async () => {
    const mm = window.matchMedia
    window.matchMedia = ((q: string) => ({ ...mm(q), matches: q.includes('hover: none'), addEventListener() {}, removeEventListener() {} })) as never
    try {
      setup()
      key('c')
      expect(screen.queryByRole('dialog')).toBeNull()
      key('k', { ctrlKey: true })
      expect(await screen.findByRole('dialog')).toBeInTheDocument()
    } finally {
      window.matchMedia = mm
    }
  })
})
