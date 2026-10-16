import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import InstallAppBar from '../components/shared/InstallAppBar'

function fire(outcome: 'accepted' | 'dismissed' = 'accepted') {
  const e = new Event('beforeinstallprompt', { cancelable: true }) as Event & Record<string, unknown>
  e.prompt = vi.fn().mockResolvedValue(undefined)
  e.userChoice = Promise.resolve({ outcome })
  act(() => { window.dispatchEvent(e) })
  return e
}

afterEach(() => { vi.restoreAllMocks() })

describe('InstallAppBar', () => {
  it('is hidden until beforeinstallprompt fires', () => {
    render(<InstallAppBar />)
    expect(screen.queryByText('Install app')).toBeNull()
    fire()
    expect(screen.getByText('Install app')).toBeTruthy()
  })

  it('prompts on click and hides after accepting', async () => {
    render(<InstallAppBar />)
    const e = fire('accepted')
    await userEvent.click(screen.getByText('Install app'))
    expect(e.prompt).toHaveBeenCalled()
    expect(screen.queryByText('Install app')).toBeNull()
  })

  it('hides after dismissal (event is single-use)', async () => {
    render(<InstallAppBar />)
    fire('dismissed')
    await userEvent.click(screen.getByText('Install app'))
    expect(screen.queryByText('Install app')).toBeNull()
  })

  it('hides on appinstalled', () => {
    render(<InstallAppBar />)
    fire()
    act(() => { window.dispatchEvent(new Event('appinstalled')) })
    expect(screen.queryByText('Install app')).toBeNull()
  })

  it('stays hidden in standalone mode', () => {
    window.matchMedia = vi.fn().mockReturnValue({ matches: true }) as unknown as typeof window.matchMedia
    render(<InstallAppBar />)
    fire()
    expect(screen.queryByText('Install app')).toBeNull()
    // @ts-expect-error cleanup
    delete window.matchMedia
  })
})
