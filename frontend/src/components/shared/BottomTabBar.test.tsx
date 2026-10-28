import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import BottomTabBar from './BottomTabBar'

const count = vi.hoisted(() => ({ n: 0 }))
vi.mock('../../stores/inbox', () => ({ useInboxCount: () => count.n }))

const setup = (path = '/board', onMore = vi.fn()) => {
  render(
    <MemoryRouter initialEntries={[path]}>
      <BottomTabBar onMore={onMore} />
    </MemoryRouter>,
  )
  return onMore
}

describe('BottomTabBar', () => {
  afterEach(() => { count.n = 0 })

  it('renders tabs with hrefs and is phone-only', () => {
    setup()
    expect(screen.getByRole('link', { name: /^Home/ })).toHaveAttribute('href', '/')
    expect(screen.getByRole('link', { name: 'Board' })).toHaveAttribute('href', '/board')
    expect(screen.getByRole('link', { name: 'Chat' })).toHaveAttribute('href', '/chat')
    expect(screen.getByRole('navigation').className).toContain('md:hidden')
    expect(screen.getByRole('navigation').className).toContain('safe-area-inset-bottom')
  })

  it('marks the active tab', () => {
    setup('/chat')
    expect(screen.getByRole('link', { name: 'Chat' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Board' })).not.toHaveAttribute('aria-current')
  })

  it('shows the Home badge only when count > 0', () => {
    setup()
    expect(screen.queryByLabelText(/items waiting/)).not.toBeInTheDocument()
  })

  it('shows the waiting count', () => {
    count.n = 3
    setup()
    expect(screen.getByLabelText('3 items waiting on you')).toHaveTextContent('3')
  })

  it('More calls onMore', () => {
    const onMore = setup()
    fireEvent.click(screen.getByRole('button', { name: 'More' }))
    expect(onMore).toHaveBeenCalled()
  })
})
