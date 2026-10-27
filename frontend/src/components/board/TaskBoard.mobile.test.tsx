import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import TaskBoard from './TaskBoard'
import { columnSwipeDirection } from './columnSwipe'
import type { Task, WorkflowLabel } from '../../api/client'
import { useNeedsMeStore } from '../../stores/needsMe'

function label(name: string, sortOrder: number): WorkflowLabel {
  return {
    id: name, workflow_id: 'wf', name, color: '#000', sort_order: sortOrder,
    agent_ignore: 0, is_terminal: 0, create_pr: 0, wip_limit: null,
    wip_limit_hard: 0, verify_mode: 'inherit', verify_command: '',
  }
}
function task(id: string, lbl: string, extra: Partial<Task> = {}): Task {
  return {
    id, title: `T-${id}`, description: '', type: 'feature', label: lbl, repo_id: 'r1',
    workflow_id: 'wf', created_at: '', updated_at: '', ...extra,
  }
}

const labels = [label('todo', 0), label('doing', 1), label('done', 2)]

function Loc() {
  return <div data-testid="loc">{useLocation().search}</div>
}
function renderBoard(tasks: Task[], url = '/') {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <TaskBoard labels={labels} tasks={tasks} runningTaskIds={new Set()} />
      <Loc />
    </MemoryRouter>,
  )
}
function swipe(el: Element, dx: number, dy = 0) {
  fireEvent.touchStart(el, { touches: [{ clientX: 200, clientY: 200 }] })
  fireEvent.touchEnd(el, { changedTouches: [{ clientX: 200 + dx, clientY: 200 + dy }] })
}

describe('columnSwipeDirection', () => {
  it('accepts clearly horizontal swipes', () => {
    expect(columnSwipeDirection({ x: 200, y: 200 }, { x: 100, y: 210 }, 100)).toBe('left')
    expect(columnSwipeDirection({ x: 100, y: 200 }, { x: 200, y: 190 }, 100)).toBe('right')
  })
  it('rejects diagonal, short and slow gestures', () => {
    expect(columnSwipeDirection({ x: 200, y: 200 }, { x: 120, y: 260 }, 100)).toBeNull()
    expect(columnSwipeDirection({ x: 200, y: 200 }, { x: 180, y: 200 }, 100)).toBeNull()
    expect(columnSwipeDirection({ x: 200, y: 200 }, { x: 100, y: 200 }, 900)).toBeNull()
  })
})

describe('TaskBoard on phone', () => {
  const origMM = window.matchMedia
  beforeEach(() => {
    window.matchMedia = ((q: string) => ({
      matches: true, media: q, addEventListener: vi.fn(), removeEventListener: vi.fn(),
    })) as unknown as typeof window.matchMedia
    window.innerWidth = 400
    useNeedsMeStore.setState({ byTask: {} })
  })
  afterEach(() => {
    window.matchMedia = origMM
    window.innerWidth = 1024
  })

  it('shows chips with counts and jumps on tap, writing ?col', () => {
    renderBoard([task('1', 'todo'), task('2', 'doing'), task('3', 'doing')])
    const chips = screen.getByTestId('column-chips')
    expect(chips.textContent).toContain('Doing2')
    fireEvent.click(screen.getByText('Done'))
    expect(screen.getByTestId('loc').textContent).toBe('?col=done')
    expect(screen.getByText('done (3/3)')).toBeTruthy()
  })

  it('restores the column from the URL', () => {
    renderBoard([], '/?col=doing&q=x')
    expect(screen.getByText('doing (2/3)')).toBeTruthy()
  })

  it('swipes between columns and preserves other params', () => {
    renderBoard([], '/?q=x')
    const pane = screen.getByText('todo (1/3)').parentElement!.parentElement!.nextElementSibling!
    swipe(pane, -120, 5)
    expect(screen.getByText('doing (2/3)')).toBeTruthy()
    expect(screen.getByTestId('loc').textContent).toContain('q=x')
    swipe(pane, 120, 5)
    expect(screen.getByText('todo (1/3)')).toBeTruthy()
  })

  it('ignores vertical scrolls', () => {
    renderBoard([])
    const pane = screen.getByText('todo (1/3)').parentElement!.parentElement!.nextElementSibling!
    swipe(pane, -60, 120)
    expect(screen.getByText('todo (1/3)')).toBeTruthy()
  })

  it('highlights columns with a task needing a human', () => {
    renderBoard([task('2', 'doing', { active_run_status: 'waiting_human' })])
    expect(screen.getByText('Doing').closest('button')!.getAttribute('data-needs-human')).toBe('true')
    expect(screen.getByText('Done').closest('button')!.getAttribute('data-needs-human')).toBeNull()
  })
})
