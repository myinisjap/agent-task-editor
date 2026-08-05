// Regression guard for #147 — the edit/pause/archive/delete controls on a
// TaskCard are hover-only (`opacity-0 group-hover:opacity-100`), which makes
// them effectively unusable on touch devices (no hover state). jsdom has no
// real hover/pointer simulation, so this test cannot fully reproduce a touch
// interaction; the best it can do is confirm the controls are always present
// in the DOM (not conditionally rendered) and reachable via accessible
// queries regardless of hover/selected state, so they're at least a fixable
// target for a CSS-only touch fix. True touch-usability verification (a
// control is visibly tappable without a prior hover) belongs in a visual/E2E
// layer (see task notes — Playwright E2E deferred).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import TaskCard from './TaskCard'
import type { Task } from '../../api/client'
import { useTasksStore } from '../../stores/tasks'
import { useReposStore } from '../../stores/repos'

vi.mock('../../api/client', async () => {
  const actual = await vi.importActual<typeof import('../../api/client')>('../../api/client')
  return {
    ...actual,
    api: {
      tasks: {
        setPaused: vi.fn(),
        setArchived: vi.fn(),
        update: vi.fn(),
      },
    },
  }
})

function baseTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    title: 'Do the thing',
    description: '',
    type: 'feature',
    label: 'todo',
    repo_id: 'r1',
    workflow_id: 'w1',
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  }
}

describe('TaskCard hover-only controls (#147)', () => {
  beforeEach(() => {
    useTasksStore.setState({ tasks: [], loading: false, error: null })
    useReposStore.setState({ repos: [], loading: false, error: null })
  })

  it('renders pause/archive/edit/delete controls in the DOM without a prior hover', () => {
    render(
      <MemoryRouter>
        <TaskCard task={baseTask()} isEditable onDelete={() => {}} />
      </MemoryRouter>,
    )

    // These are queryable via accessible title text regardless of the
    // opacity-0 CSS class applied when unselected/unhovered — i.e. they are
    // present and clickable in the DOM the whole time, which is the
    // underlying #147 complaint (icon-only, no persistent visible affordance
    // on touch). Still present but visually opacity-0 is the caveat noted
    // above.
    expect(screen.getByTitle('Pause task')).toBeInTheDocument()
    expect(screen.getByTitle('Archive task — hide from the board')).toBeInTheDocument()
    expect(screen.getByTitle('Edit task')).toBeInTheDocument()
    expect(screen.getByTitle('Delete task')).toBeInTheDocument()
  })

  it('keeps controls fully opaque (not opacity-0) once the card is selected', () => {
    render(
      <MemoryRouter>
        <TaskCard task={baseTask()} isEditable onDelete={() => {}} selected onToggleSelect={() => {}} />
      </MemoryRouter>,
    )

    // The checkbox itself switches from opacity-0 to opacity-100 when
    // `selected` — the icon buttons (pause/archive/edit/delete) do not have
    // an equivalent "selected" affordance and stay opacity-0 group-hover
    // regardless, which is the remaining part of #147 not covered by
    // `selected` state. This assertion documents that gap rather than
    // asserting a fix.
    const checkbox = screen.getByTitle('Select for bulk actions')
    expect(checkbox.className).toContain('opacity-100')

    const pauseButton = screen.getByTitle('Pause task')
    expect(pauseButton.className).toContain('opacity-0')
  })
})

describe('TaskCard duplicate action', () => {
  beforeEach(() => {
    useTasksStore.setState({ tasks: [], loading: false, error: null })
    useReposStore.setState({ repos: [], loading: false, error: null })
  })

  it('does not render a duplicate button when onDuplicate is not provided', () => {
    render(
      <MemoryRouter>
        <TaskCard task={baseTask()} />
      </MemoryRouter>,
    )

    expect(screen.queryByTitle('Duplicate task')).not.toBeInTheDocument()
  })

  it('renders a duplicate button when onDuplicate is provided and invokes it with the task', async () => {
    const user = userEvent.setup()
    const onDuplicate = vi.fn()
    const task = baseTask({ id: 'task-42' })

    render(
      <MemoryRouter>
        <TaskCard task={task} onDuplicate={onDuplicate} />
      </MemoryRouter>,
    )

    const dupButton = screen.getByTitle('Duplicate task')
    expect(dupButton).toBeInTheDocument()

    await user.click(dupButton)
    expect(onDuplicate).toHaveBeenCalledWith(task)
  })
})

// Regression guard for #249 — the "Agent running" pulse dot must actually
// render when a task is flagged as running, and must not render otherwise.
describe('TaskCard running indicator (#249)', () => {
  beforeEach(() => {
    useTasksStore.setState({ tasks: [], loading: false, error: null })
    useReposStore.setState({ repos: [], loading: false, error: null })
  })

  it('renders the "Agent running" dot when isRunning is true', () => {
    render(
      <MemoryRouter>
        <TaskCard task={baseTask()} isRunning />
      </MemoryRouter>,
    )

    expect(screen.getByTitle('Agent running')).toBeInTheDocument()
  })

  it('does not render the "Agent running" dot when isRunning is false or omitted', () => {
    render(
      <MemoryRouter>
        <TaskCard task={baseTask()} />
      </MemoryRouter>,
    )

    expect(screen.queryByTitle('Agent running')).not.toBeInTheDocument()
  })
})

// #353 — the block_reason badge surfaces why a pickup-eligible task isn't
// dispatching, and takes precedence over the queue_position badge when both
// are somehow present (block_reason is the "stuck" signal).
describe('TaskCard block_reason badge (#353)', () => {
  beforeEach(() => {
    useTasksStore.setState({ tasks: [], loading: false, error: null })
    useReposStore.setState({ repos: [], loading: false, error: null })
  })

  it('renders a badge with the reason label when block_reason is set', () => {
    render(
      <MemoryRouter>
        <TaskCard
          task={baseTask({
            block_reason: { code: 'cost_budget', message: 'budget exhausted: $5.00 of $5.00' },
          })}
        />
      </MemoryRouter>,
    )

    expect(screen.getByText(/Budget exhausted/)).toBeInTheDocument()
  })

  it('does not render a block_reason badge when block_reason is absent', () => {
    render(
      <MemoryRouter>
        <TaskCard task={baseTask()} />
      </MemoryRouter>,
    )

    expect(screen.queryByText(/Budget exhausted/)).not.toBeInTheDocument()
  })

  it('renders a countdown for transient reasons with clears_at', () => {
    const clearsAt = new Date(Date.now() + 5 * 60 * 1000).toISOString()
    render(
      <MemoryRouter>
        <TaskCard
          task={baseTask({
            block_reason: { code: 'rate_limited', message: 'all matching configs are rate-limited', clears_at: clearsAt },
          })}
        />
      </MemoryRouter>,
    )

    expect(screen.getByText(/Rate limited/)).toBeInTheDocument()
    expect(screen.getByText(/\(in \d+m\)/)).toBeInTheDocument()
  })

  it('prefers the block_reason badge over queue_position when both are present', () => {
    render(
      <MemoryRouter>
        <TaskCard
          task={baseTask({
            queue_position: 2,
            block_reason: { code: 'paused', message: 'task is paused' },
          })}
        />
      </MemoryRouter>,
    )

    expect(screen.getByText(/Paused/)).toBeInTheDocument()
    expect(screen.queryByText(/in queue/)).not.toBeInTheDocument()
  })

  it('falls back to the queue_position badge when block_reason is absent', () => {
    render(
      <MemoryRouter>
        <TaskCard task={baseTask({ queue_position: 0 })} />
      </MemoryRouter>,
    )

    expect(screen.getByText(/#1 in queue/)).toBeInTheDocument()
  })
})

// issue #350 — TaskCard is wrapped in React.memo so a board re-render
// triggered by an unrelated tasks-store change doesn't re-run useDraggable
// / useReposStore for every card. Assert the memo actually skips a
// re-render when props are referentially/structurally unchanged, and does
// re-render when the task prop genuinely changes.
describe('TaskCard memoization (#350)', () => {
  beforeEach(() => {
    useTasksStore.setState({ tasks: [], loading: false, error: null })
    useReposStore.setState({ repos: [], loading: false, error: null })
  })

  it('skips re-rendering (and re-invoking its store selectors) when re-rendered with an unchanged task prop', () => {
    const task = baseTask()
    // TaskCard calls useReposStore((s) => s.byId(task.repo_id)) on every
    // render — spy on the store's byId to detect whether TaskCard's own
    // render function actually re-ran, independent of its parent wrapper.
    const byIdSpy = vi.spyOn(useReposStore.getState(), 'byId')

    function Wrapper({ t }: { t: Task }) {
      return <TaskCard task={t} />
    }

    const { rerender } = render(
      <MemoryRouter>
        <Wrapper t={task} />
      </MemoryRouter>,
    )
    const callsAfterFirstRender = byIdSpy.mock.calls.length
    expect(callsAfterFirstRender).toBeGreaterThan(0)

    // Re-render the wrapper (simulating a parent re-render triggered by an
    // unrelated tasks-store change) with the exact same task object — memo
    // should bail out before TaskCard's body (and its selectors) re-run.
    rerender(
      <MemoryRouter>
        <Wrapper t={task} />
      </MemoryRouter>,
    )
    expect(byIdSpy.mock.calls.length).toBe(callsAfterFirstRender)

    byIdSpy.mockRestore()
  })

  it('re-renders and reflects updated content when the task prop changes', () => {
    const { rerender } = render(
      <MemoryRouter>
        <TaskCard task={baseTask({ title: 'Original title' })} />
      </MemoryRouter>,
    )
    expect(screen.getByText('Original title')).toBeInTheDocument()

    rerender(
      <MemoryRouter>
        <TaskCard task={baseTask({ title: 'Updated title' })} />
      </MemoryRouter>,
    )
    expect(screen.getByText('Updated title')).toBeInTheDocument()
    expect(screen.queryByText('Original title')).not.toBeInTheDocument()
  })
})
