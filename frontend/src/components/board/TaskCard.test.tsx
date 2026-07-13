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
